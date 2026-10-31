package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func previewService(t *testing.T) (*AnimePreviewService, *bindingMikanFake, *storage.Store, preview.Config, time.Time) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "preview.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	entry, err := store.SaveAnimeEntry(ctx, model.AnimeEntry{BangumiID: bindingPtr("123"), EditionKey: "original", Title: "测试篇章", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := store.GetAnimeSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bindings, f, _ := bindingTestService()
	bindings.Entries = store
	c := preview.Config{Chapter: preview.Chapter{ID: entry.ID, BangumiID: entry.BangumiID, EditionKey: entry.EditionKey, Revision: entry.Revision}, Settings: settings, Sources: []model.AnimeSourceConfig{{
		Ref: "main", Role: "main", Status: "paused", Provider: "mikan", ItemID: bindingPtr("2259"), GroupID: bindingPtr("45"), GroupName: bindingPtr("untrusted client name"),
		Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: bindingPtr(25), Last: bindingPtr(150)}, ChapterCorrespondence: &model.AnimeChapterCorrespondence{SourceEpisode: 25, ChapterEpisode: 1}},
		Rules:   model.AnimeRules{SchemaVersion: 1, MinimumEpisode: bindingPtr(30), Match: []string{"(?i)1080p"}},
	}}}
	at := time.Now().UTC().Truncate(time.Second).Add(-time.Second)
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(in)
		if in.Kind != "entry" {
			p.ObservedAt = at
			p.RawCount = 2
			p.Completeness = "visible_end"
			p.StopReason = ""
			p.Releases = []mikan.SourceRelease{{PostKey: fmt.Sprintf("%040x", 1), Title: "[组] 动画 [30][1080p]", PublishedRaw: at.Add(-time.Minute).Format(time.RFC3339)}, {PostKey: fmt.Sprintf("%040x", 2), Title: "[组] 动画 [31][1080p]", PublishedRaw: at.Add(-time.Minute).Format(time.RFC3339)}}
		}
		return p, nil
	}
	s := &AnimePreviewService{Repository: store, Bindings: bindings, now: func() time.Time { return at.Add(2 * time.Second) }}
	return s, f, store, c, at
}

func servicePreviewRef(b preview.Batch, index int) storage.AnimePreviewRef {
	s := b.Sources[index]
	return storage.AnimePreviewRef{BatchID: b.ID, SourceRef: s.Ref, PreviewID: s.ID, LoadRevision: s.LoadRevision}
}

func TestAnimePreviewServiceCachedSnapshotAndServerMetadata(t *testing.T) {
	s, f, _, c, at := previewService(t)
	ctx := context.Background()
	c.Settings.AccountID = bindingPtr("browser-account")
	out, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if f.reads != 2 || len(out.Sources) != 1 || out.Sources[0].MatchedCount != 2 || out.Batch.Config.Settings.AccountID != nil || *out.Batch.Config.Sources[0].GroupName != "字幕组甲" {
		t.Fatal("untrusted metadata or extra reads", out, f.reads)
	}
	first := out.Batch.Sources[0]
	ref := servicePreviewRef(*out.Batch, 0)
	s.now = func() time.Time { return at.Add(5 * time.Minute) }
	again, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main", Previous: &ref})
	if err != nil {
		t.Fatal(err)
	}
	if f.reads != 2 || again.Batch.Sources[0].T0 != at.UnixMilli() || again.Batch.Sources[0].Page.ObservedAt != first.Page.ObservedAt || len(again.Batch.Sources[0].SelectedIDs) != 2 {
		t.Fatal("cache moved T0/lost choices", again, f.reads)
	}
}

func TestAnimePreviewServiceReadFailureKeepsSuccess(t *testing.T) {
	s, f, _, c, _ := previewService(t)
	ctx := context.Background()
	out, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	read := f.read
	f.read = func(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if in.Kind != "entry" {
			return mikan.SourcePage{}, errors.New("upstream failed")
		}
		return read(ctx, in)
	}
	ref := servicePreviewRef(*out.Batch, 0)
	regenerated, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main", Previous: &ref, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(regenerated.Issues) != 1 || regenerated.Batch.Sources[0].GeneratedAt != out.Batch.Sources[0].GeneratedAt {
		t.Fatal("failed refresh renewed expiry", regenerated)
	}
	_, err = s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main", Refresh: true})
	if err == nil {
		t.Fatal("failed initial read created successful empty preview")
	}
}

func TestAnimePreviewServiceFullHistoryIncludesZero(t *testing.T) {
	s, f, _, c, at := previewService(t)
	c.Sources[0].Binding = model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}
	c.Sources[0].Rules.MinimumEpisode = bindingPtr(0)
	historyReads := 0
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(in)
		if in.Kind == "history" {
			historyReads++
			if in.Take != 2000 {
				t.Fatalf("history window: %d", in.Take)
			}
			p.ObservedAt, p.RawCount, p.Completeness, p.StopReason = at, 67, "visible_end", ""
			p.Releases = make([]mikan.SourceRelease, 67)
			for i := range p.Releases {
				title := fmt.Sprintf("[组] 动画 [%02d][1080p][简体内嵌]", i)
				if i >= 65 {
					title = "[桜都字幕组] 无职转生～到了异世界就拿出真本事～ S2 / Mushoku Tensei S2 [00v2][1080p][简体内嵌]"
				}
				p.Releases[i] = mikan.SourceRelease{PostKey: fmt.Sprintf("%040x", i+1), Title: title}
			}
		}
		return p, nil
	}
	out, err := s.Generate(context.Background(), "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	rows := out.Batch.Sources[0].Releases(s.timestamp())
	if len(rows.Items) != 67 || rows.Summary.MatchedCount != 65 || rows.Items[0].Selectable || !rows.Items[65].Selectable || rows.Items[66].Selectable || rows.Items[65].Release.Parse.Episode == nil || *rows.Items[65].Release.Parse.Episode != 0 || historyReads != 1 {
		t.Fatal("full snapshot or zero version selection", rows.Summary, historyReads)
	}
	ref := servicePreviewRef(*out.Batch, 0)
	if _, err := s.Generate(context.Background(), "admin-a", AnimePreviewInput{Config: c, SourceRef: "main", Previous: &ref}); err != nil || historyReads != 1 {
		t.Fatal("cached full history was read again", err, historyReads)
	}
}

func TestAnimePreviewServiceUncensoredReleasesRemainMatched(t *testing.T) {
	s, f, _, c, at := previewService(t)
	c.Sources[0].Binding = model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}
	c.Sources[0].Rules = model.AnimeRules{SchemaVersion: 1, MinimumEpisode: bindingPtr(0), Match: []string{
		"(?i)1080p|1920[x×]1080", "(?i)简|簡|chs|sc|gb", "(?i)内嵌|內嵌|硬字幕|hardsub", "(?i)h[. ]?265|x265|hevc",
	}}
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(in)
		if in.Kind == "history" {
			p.ObservedAt, p.RawCount, p.Completeness, p.StopReason = at, 39, "visible_end", ""
			p.Releases = make([]mikan.SourceRelease, 0, 39)
			// The real Mikan feed 3060/611 has three subtitle specifications per
			// episode; only 00–02 have the uncensored label that triggered this bug.
			for episode := 0; episode <= 12; episode++ {
				for _, subtitles := range []string{"简体内嵌", "繁体内嵌", "简繁内封"} {
					title := fmt.Sprintf("[北宇治字幕组] 无职转生Ⅱ ～到了异世界就拿出真本事～/ Mushoku Tensei - Season 2 [%02d][WebRip][1080p][HEVC_AAC][%s]", episode, subtitles)
					if episode <= 2 {
						title += "[无修正]"
					}
					p.Releases = append(p.Releases, mikan.SourceRelease{PostKey: fmt.Sprintf("%040x", len(p.Releases)+1), Title: title})
				}
			}
		}
		return p, nil
	}
	out, err := s.Generate(context.Background(), "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	rows := out.Batch.Sources[0].Releases(s.timestamp())
	if len(rows.Items) != 39 || rows.Summary.MatchedCount != 13 || rows.Summary.ReviewCount != 0 || rows.Summary.SelectedCount != 13 {
		t.Fatal("uncensored releases missing from matched preview", rows.Summary)
	}
	episodes := make(map[int]bool, 13)
	for _, row := range rows.Items {
		if row.Selectable && row.Release.Parse.Episode != nil {
			episodes[*row.Release.Parse.Episode] = true
		}
	}
	for episode := 0; episode <= 12; episode++ {
		if !episodes[episode] {
			t.Fatalf("episode %02d missing from matched preview", episode)
		}
	}
}

func TestAnimePreviewServicePartialFailureAndIndependentExpiry(t *testing.T) {
	s, f, _, c, at := previewService(t)
	ctx := context.Background()
	backup := c.Sources[0]
	backup.Ref, backup.Role, backup.Position, backup.GroupID = "backup", "standby", 1, bindingPtr("28")
	c.Sources = append(c.Sources, backup)
	main, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	read := f.read
	f.read = func(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if in.Source.GroupID == "28" {
			return mikan.SourcePage{}, errors.New("backup failed")
		}
		return read(ctx, in)
	}
	ref := servicePreviewRef(*main.Batch, 0)
	partial, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "backup", Reuse: []storage.AnimePreviewRef{ref}})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial.Batch.Sources) != 1 || partial.Batch.Sources[0].Ref != "main" || partial.Issues[0].SourceRef != "backup" || partial.Budget.TotalRawCount != 2 {
		t.Fatal("partial success", partial)
	}
	f.read = read
	s.now = func() time.Time { return at.Add(15*time.Minute + 2*time.Second) }
	partial, err = s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "backup", Reuse: []storage.AnimePreviewRef{ref}, Refresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial.Sources) != 1 || partial.Sources[0].SourceRef != "backup" || len(partial.Issues) != 1 || partial.Issues[0].Code != "PREVIEW_EXPIRED" {
		t.Fatal("expired source blocked other source", partial)
	}
}

func TestAnimePreviewServiceOwnershipExpiryAndVersionBeforeRead(t *testing.T) {
	s, f, _, c, at := previewService(t)
	ctx := context.Background()
	out, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	ref := servicePreviewRef(*out.Batch, 0)
	calls := f.reads
	if _, err := s.Select(ctx, "admin-b", ref, nil, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("actor leak", err)
	}
	s.now = func() time.Time { return at.Add(15*time.Minute + 2*time.Second) }
	if _, err := s.Select(ctx, "admin-a", ref, nil, nil); !errors.Is(err, preview.ErrExpired) {
		t.Fatal("expiry boundary", err)
	}
	c.Settings.Revision++
	if _, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"}); !errors.Is(err, preview.ErrConflict) {
		t.Fatal("settings stale accepted", err)
	}
	if f.reads != calls {
		t.Fatal("invalid/expired operations read upstream")
	}
}

func TestAnimePreviewServiceSharedRawBudget(t *testing.T) {
	for _, count := range []int{1990, 2000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, f, _, c, at := previewService(t)
			ctx := context.Background()
			backup := c.Sources[0]
			backup.Ref, backup.Role, backup.Position, backup.GroupID = "backup", "standby", 1, bindingPtr("28")
			c.Sources = append(c.Sources, backup)
			read := f.read
			f.read = func(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
				p, err := read(ctx, in)
				if in.Kind == "history" {
					p.RawCount = count
					if in.Source.GroupID == "28" {
						if in.Take != 2000-count {
							t.Fatalf("wrong remaining window: %d", in.Take)
						}
						p.RawCount = in.Take
					}
					p.Completeness, p.StopReason, p.ObservedAt = "partial", "invalid_rows", at
				}
				return p, err
			}
			main, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
			if err != nil {
				t.Fatal(err)
			}
			calls := f.reads
			ref := servicePreviewRef(*main.Batch, 0)
			out, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "backup", Reuse: []storage.AnimePreviewRef{ref}})
			if err != nil || out.Budget.TotalRawCount != 2000 || out.Budget.Remaining != 0 {
				t.Fatal("shared budget", out, err)
			}
			if count == 2000 {
				if f.reads != calls || len(out.Issues) != 1 || out.Issues[0].Code != "PREVIEW_ROW_BUDGET" || len(out.Sources) != 1 {
					t.Fatal("exhausted budget read upstream", out, f.reads, calls)
				}
			} else if f.reads != calls+1 || len(out.Issues) != 0 || len(out.Sources) != 2 {
				t.Fatal("remaining budget not used", out, f.reads, calls)
			}
		})
	}
}

func TestAnimePreviewServiceLateResponseCannotOverwriteChoices(t *testing.T) {
	s, f, store, c, at := previewService(t)
	ctx := context.Background()
	out, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	ref := servicePreviewRef(*out.Batch, 0)
	chosen := out.Batch.Sources[0].Items[0].Release.ID
	read := f.read
	f.read = func(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if in.Kind == "history" {
			if _, err := s.Select(ctx, "admin-a", ref, []string{chosen}, nil); err != nil {
				return mikan.SourcePage{}, err
			}
		}
		return read(ctx, in)
	}
	if _, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main", Previous: &ref, Refresh: true}); !errors.Is(err, preview.ErrConflict) {
		t.Fatal("late loader overwrote selection", err)
	}
	got, err := store.GetAnimePreview(ctx, "admin-a", ref.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sources[0].LoadRevision != 2 || len(got.Sources[0].SelectedIDs) != 1 || got.Sources[0].SelectedIDs[0] != chosen || got.Sources[0].GeneratedAt != at.Add(2*time.Second).UnixMilli() {
		t.Fatal("late response changed state", got)
	}
}
