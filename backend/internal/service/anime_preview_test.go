package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func previewService(t *testing.T) (*AnimePreviewService, *bindingMikanFake, *storage.Store, animepreview.Config, time.Time) {
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
	c := animepreview.Config{Chapter: animepreview.Chapter{ID: entry.ID, BangumiID: entry.BangumiID, EditionKey: entry.EditionKey, Revision: entry.Revision}, Settings: settings, Sources: []model.AnimeSourceConfig{{
		Ref: "main", Role: "main", Status: "paused", Provider: "mikan", ItemID: bindingPtr("2259"), GroupID: bindingPtr("45"), GroupName: bindingPtr("untrusted client name"),
		Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: bindingPtr(25), Last: bindingPtr(150)}, ChapterCorrespondence: &model.AnimeChapterCorrespondence{SourceEpisode: 25, ChapterEpisode: 1}},
		Rules:   model.AnimeRules{SchemaVersion: 1, MinimumEpisode: bindingPtr(30), Resolution: bindingPtr("1080p"), InitialMode: "from_episode"},
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

func servicePreviewRef(b animepreview.Batch, index int) storage.AnimePreviewRef {
	s := b.Sources[index]
	return storage.AnimePreviewRef{BatchID: b.ID, SourceRef: s.Ref, PreviewID: s.ID, LoadRevision: s.LoadRevision}
}

func TestAnimePreviewServiceCachedSnapshotAndServerMetadata(t *testing.T) {
	s, f, _, c, at := previewService(t)
	ctx := context.Background()
	c.Settings.AccountID = bindingPtr("browser-account")
	c.Settings.UID = bindingPtr("browser-uid")
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

func TestAnimePreviewServiceLoadAndSelectAcrossPages(t *testing.T) {
	s, f, _, c, at := previewService(t)
	ctx := context.Background()
	phase := 0
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(in)
		if in.Kind == "entry" {
			return p, nil
		}
		p.ObservedAt = at.Add(time.Duration(phase) * time.Second)
		p.Completeness, p.StopReason = "partial", "more_available"
		count := 50
		if phase > 0 {
			count = 53
			p.Completeness, p.StopReason = "visible_end", ""
		}
		p.RawCount = count
		p.Releases = make([]mikan.SourceRelease, 0, count)
		for i := 0; i < count; i++ {
			published := at
			if i >= 50 {
				published = p.ObservedAt
			}
			p.Releases = append(p.Releases, mikan.SourceRelease{PostKey: fmt.Sprintf("%040x", i+1), Title: fmt.Sprintf("[组] 动画 [%d][1080p]", 30+i), PublishedRaw: published.Format(time.RFC3339)})
		}
		return p, nil
	}
	out, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"})
	if err != nil {
		t.Fatal(err)
	}
	phase = 5
	s.now = func() time.Time { return at.Add(7 * time.Second) }
	loaded, err := s.Load(ctx, "admin-a", servicePreviewRef(*out.Batch, 0), 100)
	if err != nil {
		t.Fatal(err)
	}
	source := loaded.Batch.Sources[0]
	if len(source.Items) != 53 || len(source.SelectedIDs) != 50 || len(source.Pending) != 0 {
		t.Fatal("new releases silently added to initial choices", source.Summary(s.timestamp()))
	}
	if source.Items[52].Timing != "post_preview_new" {
		t.Fatal("new release timing", source.Items[52])
	}
	selected := []string{source.Items[0].Release.ID, source.Items[52].Release.ID}
	dismissed := make([]string, 0, 49)
	for i := 1; i < 50; i++ {
		dismissed = append(dismissed, source.Items[i].Release.ID)
	}
	updated, err := s.Select(ctx, "admin-a", servicePreviewRef(*loaded.Batch, 0), selected, dismissed)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Releases(ctx, "admin-a", updated.ID, "main", 2, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || len(page.SelectedIDs) != 2 || len(page.Pending) != 0 || page.Summary.SelectedCount != 2 {
		t.Fatal("paged choices", page)
	}
	if _, err := s.Load(ctx, "admin-a", servicePreviewRef(*out.Batch, 0), 150); !errors.Is(err, animepreview.ErrConflict) {
		t.Fatal("stale loader accepted", err)
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
	failed, err := s.Load(ctx, "admin-a", ref, 100)
	if err != nil || len(failed.Issues) != 1 || failed.Batch.Sources[0].LoadRevision != ref.LoadRevision || failed.Sources[0].SelectedCount != 2 {
		t.Fatal("failed load erased choices", err, failed)
	}
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
	if _, err := s.Load(ctx, "admin-b", ref, 100); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("actor leak", err)
	}
	s.now = func() time.Time { return at.Add(15*time.Minute + 2*time.Second) }
	if _, err := s.Load(ctx, "admin-a", ref, 100); !errors.Is(err, animepreview.ErrExpired) {
		t.Fatal("expiry boundary", err)
	}
	c.Settings.Revision++
	if _, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main"}); !errors.Is(err, animepreview.ErrConflict) {
		t.Fatal("settings stale accepted", err)
	}
	if f.reads != calls {
		t.Fatal("invalid/expired operations read upstream")
	}
}

func TestAnimePreviewServiceSharedRawBudgetBeforeNetwork(t *testing.T) {
	s, f, _, c, at := previewService(t)
	ctx := context.Background()
	backup := c.Sources[0]
	backup.Ref, backup.Role, backup.Position, backup.GroupID = "backup", "standby", 1, bindingPtr("28")
	c.Sources = append(c.Sources, backup)
	read := f.read
	f.read = func(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		p, err := read(ctx, in)
		if in.Kind == "history" {
			p.RawCount = 1990
			p.Completeness, p.StopReason = "partial", "invalid_rows"
			p.ObservedAt = at
		}
		return p, err
	}
	main, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "main", Take: 2000})
	if err != nil {
		t.Fatal(err)
	}
	calls := f.reads
	ref := servicePreviewRef(*main.Batch, 0)
	out, err := s.Generate(ctx, "admin-a", AnimePreviewInput{Config: c, SourceRef: "backup", Reuse: []storage.AnimePreviewRef{ref}, Take: 50})
	if err != nil {
		t.Fatal(err)
	}
	if f.reads != calls || out.Budget.TotalRawCount != 1990 || out.Budget.Remaining != 10 || out.Issues[0].Code != "PREVIEW_ROW_BUDGET" || len(out.Sources) != 1 {
		t.Fatal("shared budget", out, f.reads, calls)
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
	if _, err := s.Load(ctx, "admin-a", ref, 100); !errors.Is(err, animepreview.ErrConflict) {
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
