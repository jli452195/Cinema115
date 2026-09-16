package storage

import (
	"context"
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
	if ready, err := s.MediaUpdatesReady(ctx, scope, "one"); err != nil || ready {
		t.Fatal("未完成全量发布时不应标记时间线就绪", ready, err)
	}
	if err := s.EmbyStage(ctx, scope, "one", items); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyPublish(ctx, scope, "one", 0); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.MediaUpdatesReady(ctx, scope, "one"); err != nil || ready {
		t.Fatal("主索引发布不应阻塞等待时间线", ready, err)
	}
	for {
		done, err := s.EmbyBuildMediaUpdates(ctx, scope, "one", 2)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if ready, err := s.MediaUpdatesReady(ctx, scope, "one"); err != nil || !ready {
		t.Fatal("分批构建后时间线未就绪", ready, err)
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
