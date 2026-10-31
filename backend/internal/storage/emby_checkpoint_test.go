package storage

import (
	"context"
	"testing"

	"strm115/backend/internal/model"
)

func TestEmbyRecoveryPreservesOnlyCompletedStagingSnapshot(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const scope = "checkpoint-scope"
	if err := s.EmbyEnsure(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyBeginSync(ctx, scope, "complete", 12); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyStage(ctx, scope, "complete", []model.EmbyItem{{ID: "movie", Type: "Movie", TMDB: "1", Season: -1, Episode: -1, EndEpisode: -1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyCompleteStage(ctx, scope, "complete", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyFailSync(ctx, scope, "complete", "发布失败", false); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyRecover(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := s.EmbyState(ctx, scope)
	if err != nil || state.Status != "queued" || !state.StagingComplete || state.StagingGeneration != "complete" || state.StagingCutoff != 12 {
		t.Fatalf("完整暂存快照未保留: %+v %v", state, err)
	}
	if err := s.EmbyPublish(ctx, scope, "complete", state.StagingCutoff); err != nil {
		t.Fatal(err)
	}
	state, err = s.EmbyState(ctx, scope)
	if err != nil || state.Active != "complete" || state.StagingGeneration != "" || state.UpdatesGeneration != "complete" || !state.UpdatesReady {
		t.Fatalf("重试发布状态错误: %+v %v", state, err)
	}

	if err := s.EmbyBeginSync(ctx, scope, "incomplete", 13); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyStage(ctx, scope, "incomplete", []model.EmbyItem{{ID: "other", Type: "Movie", TMDB: "2", Season: -1, Episode: -1, EndEpisode: -1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EmbyRecover(ctx); err != nil {
		t.Fatal(err)
	}
	state, err = s.EmbyState(ctx, scope)
	if err != nil || state.Status != "failed" || state.StagingGeneration != "" || state.Active != "complete" {
		t.Fatalf("未完成暂存快照恢复错误: %+v %v", state, err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM emby_snapshots WHERE scope=? AND generation='incomplete'`, scope).Scan(&count); err != nil || count != 0 {
		t.Fatal("未完成暂存快照没有清理", count, err)
	}
}
