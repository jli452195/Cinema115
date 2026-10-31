package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestMediaUpdatesPublishPreservesCreatedTimeAndGroupsSeasons(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const scope = "updates-scope"
	items := []model.EmbyItem{
		{ID: "movie", LibraryID: "movies", Type: "Movie", TMDB: "10", Name: "电影", DateCreated: "2026-09-17T01:00:00Z", Season: -1, Episode: -1, EndEpisode: -1},
		{ID: "series", LibraryID: "tv", Type: "Series", TMDB: "20", Name: "剧集", DateCreated: "2020-01-01T00:00:00Z", Season: -1, Episode: -1, EndEpisode: -1},
		{ID: "ep1", LibraryID: "tv", Type: "Episode", SeriesID: "series", ParentID: "season1", Name: "第一集", DateCreated: "2026-09-17T02:00:00Z", Season: 1, Episode: 1, EndEpisode: -1},
		{ID: "ep2", LibraryID: "tv", Type: "Episode", SeriesID: "series", ParentID: "season1", Name: "第二集", DateCreated: "2026-09-17T03:00:00Z", Season: 1, Episode: 2, EndEpisode: 3},
	}
	if err := s.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if state, err := s.EmbyState(ctx, scope); err != nil || state.UpdatesReady {
		t.Fatal("未完成全量发布时不应标记时间线就绪", state, err)
	}
	if err := s.EmbyStage(ctx, scope, "one", items); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, scope, "one", 0); err != nil {
		t.Fatal(err)
	}
	if state, err := s.EmbyState(ctx, scope); err != nil || !state.UpdatesReady || state.UpdatesGeneration != "one" {
		t.Fatal("主索引和时间线应同时就绪", state, err)
	}
	start := time.Date(2026, 9, 17, 0, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60)).Unix()
	groups, err := s.MediaUpdateGroups(ctx, scope, start, start+24*60*60, 10, 0)
	if err != nil || len(groups) != 2 {
		t.Fatalf("聚合结果错误: %+v %v", groups, err)
	}
	if groups[0].Kind != "tv" || groups[0].Season != 1 || len(groups[0].Episodes) != 2 || groups[1].Kind != "movie" {
		t.Fatalf("未按电影和季聚合: %+v", groups)
	}
	original := groups[0].UpdatedAt

	items[2].DateCreated = ""
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := upsertMediaUpdates(ctx, tx, scope, items[2:3], original+3600); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	groups, err = s.MediaUpdateGroups(ctx, scope, start, start+24*60*60, 10, 0)
	if err != nil || groups[0].UpdatedAt != original {
		t.Fatal("元数据更新改变了原始入库时间", groups, err)
	}

	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := deleteMediaUpdates(ctx, tx, scope, "season1", original+7200); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	groups, err = s.MediaUpdateGroups(ctx, scope, start, start+24*60*60, 10, 0)
	if err != nil || len(groups) != 1 || groups[0].Kind != "movie" {
		t.Fatalf("删除季后仍显示分集: %+v %v", groups, err)
	}
}

func TestMediaUpdatesFullPublishReconcilesWithoutChangingStableDates(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const scope = "stable-dates"
	if err := s.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	items := []model.EmbyItem{
		{ID: "stable", Type: "Movie", TMDB: "1", DateCreated: "2026-09-01T01:00:00Z"},
		{ID: "corrected", Type: "Movie", TMDB: "2"},
		{ID: "removed", Type: "Movie", TMDB: "3", DateCreated: "2026-09-02T01:00:00Z"},
	}
	publish := func(generation string, items []model.EmbyItem) {
		t.Helper()
		if err := s.EmbyStage(ctx, scope, generation, items); err != nil {
			t.Fatal(err)
		}
		if err := s.EmbyPublish(ctx, scope, generation, 0); err != nil {
			t.Fatal(err)
		}
		if state, err := s.EmbyState(ctx, scope); err != nil || !state.UpdatesReady || state.UpdatesGeneration != generation {
			t.Fatal("published timeline not ready", state, err)
		}
	}
	publish("one", items)
	var original, removedOriginal int64
	if err := s.db.QueryRowContext(ctx, `SELECT added_at FROM media_update_items WHERE scope=? AND item_id='stable'`, scope).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT added_at FROM media_update_items WHERE scope=? AND item_id='removed'`, scope).Scan(&removedOriginal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE media_update_items SET first_seen_at=100,last_seen_at=200 WHERE scope=? AND item_id='stable'`, scope); err != nil {
		t.Fatal(err)
	}
	// A later full scan must not move unchanged content or known dates to today.
	items[0].DateCreated = "2026-10-01T01:00:00Z"
	items[1].DateCreated = "2026-09-03T01:00:00Z"
	publish("two", items[:2])
	var added, first, last int64
	var date string
	if err := s.db.QueryRowContext(ctx, `SELECT added_at,date_created,first_seen_at,last_seen_at FROM media_update_items WHERE scope=? AND item_id='stable'`, scope).Scan(&added, &date, &first, &last); err != nil || added != original || date != "2026-09-01T01:00:00Z" || first != 100 || last != 200 {
		t.Fatal("full rescan rewrote stable timeline", added, date, first, last, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT date_created FROM media_update_items WHERE scope=? AND item_id='corrected'`, scope).Scan(&date); err != nil || date != items[1].DateCreated {
		t.Fatal("full rescan lost corrected creation date", date, err)
	}
	var deleted int64
	if err := s.db.QueryRowContext(ctx, `SELECT deleted_at FROM media_update_items WHERE scope=? AND item_id='removed'`, scope).Scan(&deleted); err != nil || deleted == 0 {
		t.Fatal("missing item stayed visible", deleted, err)
	}
	// Re-adding a deleted item uses its new entry date instead of the old one.
	items[2].DateCreated = "2026-10-03T01:00:00Z"
	publish("three", items)
	if err := s.db.QueryRowContext(ctx, `SELECT added_at,deleted_at,date_created FROM media_update_items WHERE scope=? AND item_id='removed'`, scope).Scan(&added, &deleted, &date); err != nil || added <= removedOriginal || deleted != 0 || date != items[2].DateCreated {
		t.Fatal("re-added item retained deletion or stale date", added, deleted, date, err)
	}
}

func TestMediaUpdatesPublicationFailurePreservesCompleteOldTimeline(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const scope = "atomic-publication"
	if err := s.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyStage(ctx, scope, "old", []model.EmbyItem{{ID: "old-item", Type: "Movie", TMDB: "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, scope, "old", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyBeginSync(ctx, scope, "new", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyStage(ctx, scope, "new", []model.EmbyItem{{ID: "new-item", Type: "Movie", TMDB: "2"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyCompleteStage(ctx, scope, "new", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_timeline_publication BEFORE UPDATE OF active ON emby_state WHEN NEW.active='new' BEGIN SELECT RAISE(ABORT,'test publication failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, scope, "new", 0); err == nil {
		t.Fatal("publication should fail after deriving the replacement timeline")
	}
	if err := s.EmbyFailSync(ctx, scope, "new", "test publication failure", false); err != nil {
		t.Fatal(err)
	}
	state, err := s.EmbyState(ctx, scope)
	if err != nil || state.Active != "old" || !state.UpdatesReady || state.UpdatesGeneration != "old" || state.StagingGeneration != "new" || !state.StagingComplete {
		t.Fatal("failed publication replaced active state or discarded staging", state, err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_update_items WHERE scope=? AND item_id='old-item' AND deleted_at=0`, scope).Scan(&count); err != nil || count != 1 {
		t.Fatal("failed publication hid old timeline", count, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_update_items WHERE scope=? AND item_id='new-item'`, scope).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed publication leaked replacement timeline", count, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_timeline_publication`); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyRecover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, scope, "new", 0); err != nil {
		t.Fatal(err)
	}
	state, err = s.EmbyState(ctx, scope)
	if err != nil || state.Active != "new" || !state.UpdatesReady || state.StagingGeneration != "" {
		t.Fatal("retry did not atomically publish timeline", state, err)
	}
}

func TestMediaUpdatesRetryAndMetadataUpdates(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const scope = "retry-timeline"
	if err := s.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyStage(ctx, scope, "gen", []model.EmbyItem{{ID: "movie", Type: "Movie", TMDB: "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, scope, "gen", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM media_update_items WHERE scope=?`, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE emby_state SET updates_ready=0,updates_error='interrupted' WHERE scope=?`, scope); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_timeline_retry BEFORE UPDATE OF updates_ready ON emby_state WHEN NEW.updates_ready=1 BEGIN SELECT RAISE(ABORT,'test retry failure'); END`); err != nil {
		t.Fatal(err)
	}
	if done, err := s.EmbyBuildMediaUpdates(ctx, scope, "gen"); err == nil || done {
		t.Fatal("failed retry should not report completion", done, err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_update_items WHERE scope=?`, scope).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed retry leaked partially rebuilt rows", count, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_timeline_retry`); err != nil {
		t.Fatal(err)
	}
	if done, err := s.EmbyBuildMediaUpdates(ctx, scope, "gen"); err != nil || !done {
		t.Fatal("retry did not finish in one call", done, err)
	}
	state, err := s.EmbyState(ctx, scope)
	if err != nil || !state.UpdatesReady || state.UpdatesError != "" {
		t.Fatal(state, err)
	}
	if err := s.EmbyEnqueue(ctx, scope, "movie", "metadata"); err != nil {
		t.Fatal(err)
	}
	event, err := s.EmbyEventForItem(ctx, scope, "movie")
	if err != nil || event == nil {
		t.Fatal(event, err)
	}
	date := "2026-09-17T02:00:00Z"
	if err := s.EmbyApplyMetadataEvent(ctx, scope, "gen", *event, []model.EmbyItem{{ID: "movie", Type: "Movie", Name: "metadata title", DateCreated: date}}, "missing TMDB"); err != nil {
		t.Fatal(err)
	}
	var gotDate, title string
	if err := s.db.QueryRowContext(ctx, `SELECT date_created,name FROM media_update_items WHERE scope=? AND item_id='movie'`, scope).Scan(&gotDate, &title); err != nil || gotDate != date || title != "metadata title" {
		t.Fatal("metadata retry did not refresh timeline", gotDate, title, err)
	}
}

func BenchmarkMediaUpdatesFullPublish(b *testing.B) {
	const total = 141958
	for _, mode := range []string{"first", "unchanged"} {
		b.Run(mode, func(b *testing.B) {
			ctx := context.Background()
			s, err := Open(ctx, filepath.Join(b.TempDir(), "timeline.db"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if err := s.Close(); err != nil {
					b.Error(err)
				}
			})
			if err := s.EmbyEnsure(ctx, "bench"); err != nil {
				b.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO emby_snapshots(scope,generation) VALUES('bench','gen')`); err != nil {
				b.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `WITH RECURSIVE entries(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM entries WHERE n<?)
 INSERT INTO emby_items(snapshot_id,id,library_id,type,tmdb,series_id,parent_id,name,season,episode,end_episode,date_created)
 SELECT (SELECT id FROM emby_snapshots WHERE scope='bench' AND generation='gen'),
 printf('%08d',n),'tv',CASE WHEN n<=1406 THEN 'Series' ELSE 'Episode' END,
 CASE WHEN n<=1406 THEN CAST(100000+n AS TEXT) ELSE '' END,
 CASE WHEN n<=1406 THEN '' ELSE printf('%08d',(n-1407)/100+1) END,
 '',printf('Media %d',n),1,(n-1407)%100+1,-1,'2026-10-31T01:00:00Z' FROM entries`, total); err != nil {
				b.Fatal(err)
			}
			if mode == "unchanged" {
				if err := s.EmbyPublish(ctx, "bench", "gen", 0); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				if mode == "first" && n > 0 {
					b.StopTimer()
					if _, err := s.db.ExecContext(ctx, `DELETE FROM media_update_items`); err != nil {
						b.Fatal(err)
					}
					b.StartTimer()
				}
				if err := s.EmbyPublish(ctx, "bench", "gen", 0); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(total, "items/op")
		})
	}
}
