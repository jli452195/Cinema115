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
		Rules:   model.AnimeRules{SchemaVersion: 1, MinimumEpisode: animePtr(1), ChapterFirstEpisode: animePtr(first)}}
	entry := model.AnimeEntry{Metadata: model.AnimeMetadata{AirDate: "2026-10-02", TotalEpisodes: animePtr(total)}}
	sub := model.AnimeSubscription{Naming: organizationNaming(source.ID)}
	sub.Naming.Target.Seasons[0].Episodes[0].Number = first
	sub.Naming.Target.Seasons[0].Episodes[0].Date = entry.Metadata.AirDate
	sub.Naming.Sources[0].Segments[0] = model.AnimeMappingSegment{First: 1, SourceBase: 1, TargetBase: 1, TargetSeason: 1}
	return sub, source, entry
}

func TestAnimeSubscriptionRange(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		first                               *int
		total, minimum, wantFirst, wantLast int
		known                               bool
	}{
		{"zero", animePtr(0), 13, 0, 0, 12, true},
		{"ordinary", animePtr(1), 12, 0, 1, 12, true},
		{"continuous", animePtr(49), 12, 0, 49, 60, true},
		{"download minimum", animePtr(0), 13, 7, 7, 12, true},
		{"unknown total", animePtr(49), 0, 0, 49, 0, false},
		{"unknown first", nil, 13, 0, -1, 0, false},
		{"overflow", animePtr(99999), 2, 0, 99999, 0, false},
		{"minimum after end", animePtr(0), 13, 13, 13, 12, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := model.AnimeSource{Rules: model.AnimeRules{ChapterFirstEpisode: tc.first, MinimumEpisode: animePtr(tc.minimum)}}
			first, last, known := animeCompletionRange(source, tc.total)
			if first != tc.wantFirst || last != tc.wantLast || known != tc.known {
				t.Fatal(first, last, known)
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
				if err != nil || parsed.Kind != row.Kind || row.Kind == "episode" && (parsed.Episode == nil || *parsed.Episode != row.Episode) || row.Kind != "episode" && parsed.Episode != nil {
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
			source.Rules.ChapterFirstEpisode = nil
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
		t.Run(map[bool]string{false: "explicit range", true: "range confirmation"}[manual], func(t *testing.T) {
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
				if _, err := f.store.db.Exec(`UPDATE anime_subscription_sources SET rules_json=json_remove(rules_json,'$.chapterFirstEpisode')`); err != nil {
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
