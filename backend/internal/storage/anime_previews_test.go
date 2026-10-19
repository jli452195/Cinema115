package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
)

func previewConfig(t *testing.T, s *Store) animepreview.Config {
	t.Helper()
	entry := createAnimeEntry(t, s, animePtr("123"))
	settings, err := s.GetAnimeSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := animepreview.Config{Chapter: animepreview.Chapter{ID: entry.ID, BangumiID: entry.BangumiID, EditionKey: entry.EditionKey, Revision: entry.Revision}, Settings: settings, Sources: animeConfig(entry.ID).Sources}
	c, err = animepreview.Normalize(c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func previewPage(n int, at int64) mikan.SourcePage {
	p := mikan.SourcePage{Kind: "history", Source: mikan.Source{ItemID: "2259", GroupID: "45"}, ObservedAt: time.UnixMilli(at), RawCount: n, RequestedTake: 50, Completeness: "visible_end", Releases: make([]mikan.SourceRelease, 0, n)}
	for i := 0; i < n; i++ {
		p.Releases = append(p.Releases, mikan.SourceRelease{PostKey: fmt.Sprintf("%040x", i+1), Title: fmt.Sprintf("[组] 动画 [%d][1080p][HEVC]", 30+i)})
	}
	return p
}

func createPreview(t *testing.T, s *Store, c animepreview.Config, p mikan.SourcePage, now int64) animepreview.Batch {
	t.Helper()
	b, err := s.CreateAnimePreview(context.Background(), "admin-a", c, []AnimePreviewSeed{{SourceRef: "main", Page: &p}}, now)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func previewRef(b animepreview.Batch, index int) AnimePreviewRef {
	source := b.Sources[index]
	return AnimePreviewRef{BatchID: b.ID, SourceRef: source.Ref, PreviewID: source.ID, LoadRevision: source.LoadRevision}
}

func TestAnimePreviewPersistenceAndNoExecution(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	now := time.Now().UnixMilli()
	b := createPreview(t, s, c, previewPage(2, now-10000), now)
	if len(b.Sources[0].SelectedIDs) != 2 {
		t.Fatal("initial choices")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetAnimePreview(ctx, "admin-a", b.ID)
	if err != nil || !reflect.DeepEqual(b, got) {
		t.Fatal("snapshot changed on restart", err)
	}
	for _, table := range []string{"anime_source_baselines", "anime_baseline_releases", "anime_release_decisions", "anime_subscriptions"} {
		var count int
		if err := reopened.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("preview authorized work", table, count, err)
		}
	}
	if _, err := reopened.GetAnimePreview(ctx, "admin-b", b.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("actor leak", err)
	}
}

func TestAnimePreviewCASSelectionAndLoad(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	now := time.Now().UnixMilli()
	b := createPreview(t, s, c, previewPage(2, now), now)
	ref := previewRef(b, 0)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.SelectAnimePreview(ctx, "admin-a", ref, []string{b.Sources[0].Items[i].Release.ID}, nil, now+1)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, animepreview.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("CAS did not select one winner", success, conflict)
	}
	current, err := s.GetAnimePreview(ctx, "admin-a", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := previewPage(3, now+100)
	p.RequestedTake = 100
	loaded, err := s.LoadAnimePreview(ctx, "admin-a", previewRef(current, 0), p, now+100)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Sources[0].LoadRevision != 3 || len(loaded.Sources[0].SelectedIDs) != 1 || len(loaded.Sources[0].DismissedIDs) != 1 || loaded.Sources[0].ExpiresAt != b.Sources[0].ExpiresAt || loaded.Sources[0].T0 != b.Sources[0].T0 {
		t.Fatal("load changed explicit intent or renewed lease", loaded)
	}
	if _, err := s.LoadAnimePreview(ctx, "admin-a", ref, p, now+200); !errors.Is(err, animepreview.ErrConflict) {
		t.Fatal("stale loader won", err)
	}
}

func TestAnimePreviewExpiryReuseAndScope(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	now := time.Now().UnixMilli()
	b := createPreview(t, s, c, previewPage(0, now-120000), now)
	ref := previewRef(b, 0)
	reused, err := s.CreateAnimePreview(ctx, "admin-a", c, []AnimePreviewSeed{{SourceRef: "main", Previous: &ref}}, now+899999)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Sources[0].GeneratedAt != now || reused.Sources[0].ExpiresAt != now+900000 || reused.Sources[0].Page.ObservedAt.UnixMilli() != now-120000 {
		t.Fatal("reuse refreshed clocks")
	}
	if _, err := s.SelectAnimePreview(ctx, "admin-a", ref, nil, nil, now+900000); !errors.Is(err, animepreview.ErrExpired) {
		t.Fatal("exact expiry accepted", err)
	}
	if _, err := s.CreateAnimePreview(ctx, "admin-a", c, []AnimePreviewSeed{{SourceRef: "main", Previous: &ref}}, now+900000); !errors.Is(err, animepreview.ErrExpired) {
		t.Fatal("expired reuse", err)
	}
	other := c
	other.Chapter.DraftKey = "other"
	other.Chapter.ID = ""
	other.Chapter.Revision = 0
	if _, err := s.CreateAnimePreview(ctx, "admin-a", other, []AnimePreviewSeed{{SourceRef: "main", Previous: &ref}}, now+1); !errors.Is(err, animepreview.ErrInput) {
		t.Fatal("cross-chapter reuse", err)
	}
	// Expired data is still readable for form recovery.
	if got, err := s.GetAnimePreview(ctx, "admin-a", b.ID); err != nil || len(got.Sources) != 1 {
		t.Fatal(err)
	}
}

func TestAnimePreviewRegenerateRetainsUnresolvedSelections(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	now := time.Now().UnixMilli()
	old := createPreview(t, s, c, previewPage(3, now), now)
	p := previewPage(2, now+1000)
	p.Releases[0].Title = "[组] 动画 [30v2][1080p][HEVC]"
	ref := previewRef(old, 0)
	next, err := s.CreateAnimePreview(ctx, "admin-a", c, []AnimePreviewSeed{{SourceRef: "main", Page: &p, Previous: &ref}}, now+1000)
	if err != nil {
		t.Fatal(err)
	}
	source := next.Sources[0]
	if len(source.Pending) != 2 || len(source.SelectedIDs) != 1 || source.T0 != old.Sources[0].T0 || source.GeneratedAt == old.Sources[0].GeneratedAt {
		t.Fatal("re-preview intent", source)
	}
	if _, err := s.SelectAnimePreview(ctx, "admin-a", previewRef(next, 0), []string{old.Sources[0].Items[2].Release.ID}, nil, now+2000); !errors.Is(err, animepreview.ErrInput) {
		t.Fatal("ghost accepted", err)
	}
	if !errors.Is(source.ValidateSelection(now+2000), animepreview.ErrReview) {
		t.Fatal("unresolved source accepted")
	}
}

func TestAnimePreviewBudgetAndFailureRollback(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	now := time.Now().UnixMilli()
	standby := c.Sources[0]
	standby.Ref, standby.Role, standby.Position, standby.GroupID = "backup", "standby", 1, animePtr("28")
	c.Sources = append(c.Sources, standby)
	first := previewPage(1, now)
	first.RawCount, first.RequestedTake, first.Completeness, first.StopReason = 1990, 1990, "partial", "more_available"
	second := previewPage(1, now)
	second.Source.GroupID = "28"
	second.RawCount = 11
	_, err := s.CreateAnimePreview(ctx, "admin-a", c, []AnimePreviewSeed{{SourceRef: "main", Page: &first}, {SourceRef: "backup", Page: &second}}, now)
	if !errors.Is(err, mikan.ErrLimit) {
		t.Fatal("over-budget accepted", err)
	}
	for _, table := range []string{"anime_preview_batches", "anime_source_previews", "anime_preview_items", "anime_releases"} {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial transaction", table, count, err)
		}
	}
	second.RawCount = 10
	b, err := s.CreateAnimePreview(ctx, "admin-a", c, []AnimePreviewSeed{{SourceRef: "main", Page: &first}, {SourceRef: "backup", Page: &second}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if b.Budget().TotalRawCount != 2000 || b.Budget().Remaining != 0 || len(b.Sources[0].Items) != 1 {
		t.Fatal("dedup used as budget")
	}
	first.RawCount, first.RequestedTake = 1991, 2000
	if _, err := s.LoadAnimePreview(ctx, "admin-a", previewRef(b, 0), first, now+1); !errors.Is(err, mikan.ErrLimit) {
		t.Fatal("load exceeded shared budget", err)
	}
	got, err := s.GetAnimePreview(ctx, "admin-a", b.ID)
	if err != nil || got.Sources[0].LoadRevision != 1 {
		t.Fatal("failed load changed state", err)
	}
}

func TestAnimePreviewVersionsAndIndependentSourceReuse(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	now := time.Now().UnixMilli()
	b := createPreview(t, s, c, previewPage(1, now), now)
	ref := previewRef(b, 0)
	standby := c.Sources[0]
	standby.Ref, standby.Role, standby.Position, standby.GroupID = "backup", "standby", 1, animePtr("28")
	standby.Rules.Include = []string{"different"}
	c.Sources = append(c.Sources, standby)
	if _, err := s.CreateAnimePreview(ctx, "admin-a", c, []AnimePreviewSeed{{SourceRef: "main", Previous: &ref}}, now+1); err != nil {
		t.Fatal("draft standby invalidated main", err)
	}
	c.Sources[0].Rules.MinimumEpisode = animePtr(31)
	if _, err := s.CreateAnimePreview(ctx, "admin-a", c, []AnimePreviewSeed{{SourceRef: "main", Previous: &ref}}, now+1); !errors.Is(err, animepreview.ErrConflict) {
		t.Fatal("changed rules reused", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE anime_settings SET revision=revision+1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectAnimePreview(ctx, "admin-a", ref, nil, nil, now+2); !errors.Is(err, animepreview.ErrConflict) {
		t.Fatal("stale directory settings accepted", err)
	}
}

func TestAnimePreviewFrozenObservation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	now := time.Now().UnixMilli()
	b := createPreview(t, s, c, previewPage(1, now), now)
	r := b.Sources[0].Items[0].Release
	r.Title = "newer upstream content"
	r.ObservedAt++
	r.ContentFingerprint = "newer"
	if _, err := s.SaveAnimeReleases(ctx, []model.AnimeRelease{r}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAnimePreview(ctx, "admin-a", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sources[0].Items[0].Release.Title == r.Title {
		t.Fatal("preview read mutable release")
	}
}
