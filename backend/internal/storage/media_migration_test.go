package storage

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"strm115/backend/internal/model"
)

func TestCompactMediaMigrationPreservesVersionsAndScopes(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	// 构造升级前的真实表形状，包含同集多版本、特别篇和连续多集文件。
	_, err := s.db.ExecContext(ctx, `DROP TABLE follow_bot_jobs; DROP TABLE website_notifications; DROP TABLE follow_checks; DROP TABLE follow_settings; DROP TABLE user_follows; DROP TABLE follow_requests; DROP TABLE website_users; DROP TABLE telegram_notifications; DROP TABLE p115_feature_migration; DROP TABLE p115_account_state; DROP TABLE p115_accounts; DROP TABLE tmdb_cache; DROP TABLE emby_items; DROP TABLE emby_snapshots;
 CREATE TABLE emby_items(scope TEXT NOT NULL,generation TEXT NOT NULL,id TEXT NOT NULL,library_id TEXT NOT NULL,type TEXT NOT NULL,tmdb TEXT NOT NULL,series_id TEXT NOT NULL,parent_id TEXT NOT NULL,record TEXT NOT NULL,PRIMARY KEY(scope,generation,id)); PRAGMA user_version=3;`)
	if err != nil {
		t.Fatal(err)
	}
	input := []model.EmbyItem{
		{ID: "series", Type: "Series", TMDB: "1396", Name: "测试剧集", Season: -1, Episode: -1, EndEpisode: -1},
		{ID: "v1", Type: "Episode", SeriesID: "series", Season: 0, Episode: 0, EndEpisode: 0, Name: "特别篇"},
		{ID: "v2", Type: "Episode", SeriesID: "series", Season: 0, Episode: 0, EndEpisode: 2, Name: "多个版本"},
	}
	for _, scope := range []string{"范围一", "范围二"} {
		for _, item := range input {
			data, _ := json.Marshal(item)
			_, err := s.db.ExecContext(ctx, `INSERT INTO emby_items VALUES(?,?,?,?,?,?,?,?,?)`, scope, "旧快照", item.ID, item.LibraryID, item.Type, item.TMDB, item.SeriesID, item.ParentID, string(data))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"范围一", "范围二"} {
		got, err := s.EmbyMatch(ctx, scope, "旧快照", "tv", "1396", true)
		if err != nil || !reflect.DeepEqual(got, input) {
			t.Fatal("迁移丢失版本或范围", got, err)
		}
	}
	var snapshots, count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_snapshots`).Scan(&snapshots); err != nil || snapshots != 2 {
		t.Fatal(snapshots, err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_items`).Scan(&count); err != nil || count != 6 {
		t.Fatal(count, err)
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM emby_snapshots WHERE scope='范围一'`); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_items`).Scan(&count); err != nil || count != 3 {
		t.Fatal("快照清理应只删除对应范围", count, err)
	}
}
