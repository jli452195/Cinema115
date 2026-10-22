package storage

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/model"
)

// Only numbering metadata is synthetic. Feed fixtures below retain actual
// upstream titles and URLs, without magnets, credentials or download effects.
func chapterProgressModels(first, total int) (model.AnimeSubscription, model.AnimeSource, model.AnimeEntry) {
	source := model.AnimeSource{ID: "source", CheckedAt: animePtr(int64(1)),
		Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "4106", GroupID: "382"}},
		Rules:   model.AnimeRules{SchemaVersion: 1, MinimumEpisode: animePtr(1)}}
	entry := model.AnimeEntry{Metadata: model.AnimeMetadata{AirDate: "2026-10-02", TotalEpisodes: animePtr(total)}}
	sub := model.AnimeSubscription{Naming: organizationNaming(source.ID)}
	sub.Naming.Target.Seasons[0].Episodes[0].Number = first
	sub.Naming.Target.Seasons[0].Episodes[0].Date = entry.Metadata.AirDate
	sub.Naming.Sources[0].Segments[0] = model.AnimeMappingSegment{First: 1, SourceBase: 1, TargetBase: 1, TargetSeason: 1}
	return sub, source, entry
}

func TestAnimeChapterRangeUsesExplicitOrUniqueLocalEvidence(t *testing.T) {
	for _, mode := range []string{"continuous", "ordinary", "source one to TMDB 49", "source 49 to TMDB one", "manual", "correspondence", "full range", "minimum", "unknown total", "partial range", "missing date", "partial date", "different date", "same day episodes", "special season", "multiple segments", "override", "missing target", "wrong source", "out of segment", "no naming", "overflow", "minimum past end"} {
		t.Run(mode, func(t *testing.T) {
			sub, source, entry := chapterProgressModels(49, 12)
			segment := &sub.Naming.Sources[0].Segments[0]
			wantFirst, wantLast, wantKnown := 49, 60, true
			switch mode {
			case "continuous":
			case "ordinary":
				sub.Naming.Target.Seasons[0].Episodes[0].Number = 1
				wantFirst, wantLast = 1, 12
			case "source one to TMDB 49":
				segment.TargetBase = 49
				wantFirst, wantLast = 1, 12
			case "source 49 to TMDB one":
				sub.Naming.Target.Seasons[0].Episodes[0].Number = 1
				segment.SourceBase, segment.First = 49, 49
			case "manual":
				source.Rules.ChapterFirstEpisode = animePtr(25)
				wantFirst, wantLast = 25, 36
			case "correspondence":
				source.Binding.ChapterCorrespondence = &model.AnimeChapterCorrespondence{SourceEpisode: 27, ChapterEpisode: 3}
				wantFirst, wantLast = 25, 36
			case "full range":
				source.Binding.Scope = &model.AnimeScope{Kind: "range", First: animePtr(25), Last: animePtr(36)}
				wantFirst, wantLast = 25, 36
			case "minimum":
				source.Rules.MinimumEpisode = animePtr(50)
				wantFirst = 50
			case "unknown total":
				entry.Metadata.TotalEpisodes = animePtr(0)
				wantLast, wantKnown = 0, false
			case "partial range":
				entry.Metadata.AirDate = ""
				source.Binding.Scope = &model.AnimeScope{Kind: "range", First: animePtr(50), Last: animePtr(55)}
				wantFirst, wantLast, wantKnown = 50, 0, false
			case "overflow":
				source.Rules.ChapterFirstEpisode = animePtr(99999)
				wantFirst, wantLast, wantKnown = 99999, 0, false
			case "minimum past end":
				source.Rules.MinimumEpisode = animePtr(61)
				wantFirst, wantKnown = 61, false
			default:
				wantFirst, wantLast, wantKnown = 0, 0, false
				switch mode {
				case "missing date":
					entry.Metadata.AirDate = ""
				case "partial date":
					entry.Metadata.AirDate = "2026-10"
				case "different date":
					entry.Metadata.AirDate = "2026-10-03"
				case "same day episodes":
					sub.Naming.Target.Seasons[0].Episodes = append(sub.Naming.Target.Seasons[0].Episodes, model.AnimeNamingEpisode{Number: 50, Date: entry.Metadata.AirDate})
				case "special season":
					segment.TargetSeason, sub.Naming.Target.Seasons[0].Number = 0, 0
				case "multiple segments":
					sub.Naming.Sources[0].Segments = append(sub.Naming.Sources[0].Segments, *segment)
				case "override":
					sub.Naming.Sources[0].Overrides = []model.AnimeMappingOverride{{RawKind: "episode", RawNumber: "49", Action: "ignore"}}
				case "missing target":
					sub.Naming.Target.Seasons = nil
				case "wrong source":
					sub.Naming.Sources[0].SourceRef = "another-source"
				case "out of segment":
					segment.Last = animePtr(48)
				case "no naming":
					sub.Naming = nil
				}
			}
			first, last, known := animeCompletionRange(sub, source, entry, *entry.Metadata.TotalEpisodes)
			if first != wantFirst || last != wantLast || known != wantKnown {
				t.Fatalf("range (%d,%d,%v), want (%d,%d,%v)", first, last, known, wantFirst, wantLast, wantKnown)
			}
		})
	}
}

func TestAnimeSourceProgressRealThreeSiteFeeds(t *testing.T) {
	var feeds []struct {
		Name, URL    string
		First, Total int
		Rows         []struct {
			Title, Kind string
			Episode     int
		}
	}
	raw, err := os.ReadFile("testdata/anime_progress_feeds.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &feeds); err != nil {
		t.Fatal(err)
	}
	if len(feeds) != 14 {
		t.Fatal("lost three-site sample coverage", len(feeds))
	}
	for _, feed := range feeds {
		t.Run(feed.Name, func(t *testing.T) {
			sub, source, entry := chapterProgressModels(feed.First, feed.Total)
			observed, organized := make([]string, 0, len(feed.Rows)), make([]string, 0, len(feed.Rows))
			episodes, published := make(map[int]bool), 0
			for _, row := range feed.Rows {
				parsed, err := animeparse.Title(row.Title)
				if err != nil || parsed.Kind != row.Kind || row.Episode == 0 && parsed.Episode != nil || row.Episode > 0 && (parsed.Episode == nil || *parsed.Episode != row.Episode) {
					t.Fatalf("%s: %q: %+v, %v; want %s/%d", feed.URL, row.Title, parsed, err, row.Kind, row.Episode)
				}
				decision := model.AnimeDecisionSnapshot{Title: row.Title, Rules: source.Rules, Parse: parsed}
				data, err := animeJSON(decision)
				if err != nil {
					t.Fatal(err)
				}
				observed = append(observed, data)
				data, err = animeJSON(model.AnimeDownloadSnapshot{Decision: decision, Release: model.AnimeRelease{Title: row.Title, Parse: parsed}})
				if err != nil {
					t.Fatal(err)
				}
				organized = append(organized, data)
				if row.Episode >= feed.First && row.Episode <= feed.First+feed.Total-1 {
					episodes[row.Episode] = true
					published = max(published, row.Episode)
				}
			}
			for _, missingOne := range []bool{false, true} {
				receipts := organized
				count := len(episodes)
				if missingOne && count > 0 {
					receipts = nil
					for _, data := range organized {
						var snapshot model.AnimeDownloadSnapshot
						if err := decodeAnimeJSON(data, &snapshot); err != nil {
							t.Fatal(err)
						}
						if n := snapshot.Release.Parse.Episode; n == nil || *n != published {
							receipts = append(receipts, data)
						}
					}
					count--
				}
				got, err := animeSourceProgress(context.Background(), source, sub, entry, observed, receipts)
				missing, state := max(0, published-feed.First+1-count), "ongoing"
				if missing > 0 {
					state = "missing"
				} else if published == feed.First+feed.Total-1 {
					state = "complete"
				}
				if err != nil || got.State != state || got.FirstEpisode != feed.First || got.PublishedThrough != published || got.OrganizedCount != count || got.MissingCount != missing {
					t.Fatalf("oneMissing=%v: %+v, %v; want %s through=%d organized=%d missing=%d", missingOne, got, err, state, published, count, missing)
				}
			}
			entry.Metadata.AirDate = ""
			if got, err := animeSourceProgress(context.Background(), source, sub, entry, observed, organized); err != nil || got.State != "numbering_unknown" || got.MissingCount != 0 {
				t.Fatal("unknown chapter start must not guess RSS minimum or episode 1", got, err)
			}
			if feed.Name == "mikan-rezero-dual" {
				for _, pattern := range []string{`\[(\d+)\]\[3rd`, `\[3rd - (\d+)\]`} {
					rules, err := animeparse.CompileRules(model.AnimeRules{EpisodePattern: pattern, EpisodeGroup: 1})
					if err != nil {
						t.Fatal(err)
					}
					for _, row := range feed.Rows {
						parsed, err := rules.Parse(row.Title)
						if err != nil || parsed.Kind != "episode" || parsed.Episode == nil || pattern == `\[(\d+)\]\[3rd` && (*parsed.Episode < 59 || *parsed.Episode > 66) || pattern != `\[(\d+)\]\[3rd` && (*parsed.Episode < 9 || *parsed.Episode > 16) {
							t.Fatal("explicit dual-numbering rule failed", row.Title, parsed, err)
						}
					}
				}
			}
		})
	}
}

func TestAnimeContinuousChapterCompletionWaitsForEverySource(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "local premiere evidence", true: "manual fallback"}[manual], func(t *testing.T) {
			f, now := lifecycleFixture(t)
			if _, err := f.store.db.Exec(`UPDATE anime_settings SET organize_cid='900',organize_path='/organized'`); err != nil {
				t.Fatal(err)
			}
			sub, source, entry := chapterProgressModels(49, 12)
			sub.Naming.Sources = nil
			for _, saved := range f.sub.Sources {
				sub.Naming.Sources = append(sub.Naming.Sources, model.AnimeSourceMapping{SourceRef: saved.ID, Segments: []model.AnimeMappingSegment{{First: 1, SourceBase: 1, TargetBase: 1, TargetSeason: 1}}})
				binding, _ := animeJSON(source.Binding)
				rules, _ := animeJSON(source.Rules)
				if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET binding_json=?,rules_json=? WHERE id=?`, binding, rules, saved.ID); err != nil {
					t.Fatal(err)
				}
			}
			naming, _ := animeJSON(sub.Naming)
			if _, err := f.store.db.Exec(`UPDATE anime_subscriptions SET naming_json=? WHERE id=?`, naming, f.sub.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.db.Exec(`UPDATE anime_entries SET metadata_json=json_set(metadata_json,'$.airDate',?)`, entry.Metadata.AirDate); err != nil {
				t.Fatal(err)
			}
			var err error
			f.sub, err = f.store.GetAnimeSubscription(context.Background(), f.sub.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			for group := 0; group < 2; group++ {
				for n := 49; n <= 60; n++ {
					if group == 1 && n == 55 {
						continue
					}
					progressEpisode(t, f, group, n, true)
				}
				if got := evaluateLifecycle(t, f, now); got.Status != "enabled" {
					t.Fatal("unfinished standby chapter archived", got.Status)
				}
			}
			if got := refreshProgress(t, f)[f.sub.Sources[1].ID]; got.FirstEpisode != 49 || got.MissingCount != 1 || got.Missing[0].First != 55 {
				t.Fatal("continuous source lost episode 55", got)
			}
			progressEpisode(t, f, 1, 55, true)
			if manual {
				if _, err := f.store.db.Exec(`UPDATE anime_entries SET metadata_json=json_remove(metadata_json,'$.airDate')`); err != nil {
					t.Fatal(err)
				}
				if got := evaluateLifecycle(t, f, now); got.Status != "enabled" {
					t.Fatal("ambiguous numbering auto-archived", got.Status)
				}
				if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_set(rules_json,'$.chapterFirstEpisode',49)`); err != nil {
					t.Fatal(err)
				}
			}
			if got := evaluateLifecycle(t, f, now); got.Status != "archived" {
				t.Fatal("49-60 organized by both sources failed to archive", got.Status)
			}
		})
	}
}
