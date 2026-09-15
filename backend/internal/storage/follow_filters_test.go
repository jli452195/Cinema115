package storage

import (
	"context"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func TestFollowStatusFilterPaginationCountsAndReopening(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for id := 1; id <= 29; id++ {
		if err := s.Subscribe(ctx, "alice", model.Follow{Kind: "movie", TMDBID: id, Title: "测试电影"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE follow_requests SET status='completed' WHERE kind='movie'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(ctx, "alice", model.Follow{Kind: "tv", TMDBID: 99, Title: "追平剧集"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE follow_checks SET state='caught_up' WHERE kind='tv'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(ctx, "bob", model.Follow{Kind: "tv", TMDBID: 99, Title: "追平剧集"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe(ctx, "bob", model.Follow{Kind: "movie", TMDBID: 100, Title: "他人的电影"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelFollow(ctx, "alice", "movie", 25); err != nil {
		t.Fatal(err)
	}
	ongoing, err := s.Follows(ctx, "alice", "", "ongoing", 0)
	if err != nil || len(ongoing) != 1 || ongoing[0].TMDBID != 99 {
		t.Fatal("追平应仍属追更中", ongoing, err)
	}
	first, err := s.Follows(ctx, "alice", "", "completed", 0)
	if err != nil || len(first) != 25 {
		t.Fatal("完成状态筛选应先于分页", len(first), err)
	}
	last, err := s.Follows(ctx, "alice", "movie", "completed", 24)
	if err != nil || len(last) != 4 {
		t.Fatal("完成状态分页或取消隔离失败", len(last), err)
	}
	counts, err := s.FollowCounts(ctx, "alice", "", "")
	if err != nil || counts["completed"] != 28 || counts["ongoing"] != 1 {
		t.Fatal("个人数量不应包含他人的申请", counts, err)
	}
	counts, err = s.FollowCounts(ctx, "", "tv", "追平")
	if err != nil || counts["pending"] != 1 {
		t.Fatal("管理员应按影视而非人数统计", counts, err)
	}
	counts, err = s.FollowCounts(ctx, "alice", "movie", "100")
	if err != nil || counts["ongoing"] != 0 || counts["completed"] != 0 {
		t.Fatal("类型或关键词过滤不正确", counts, err)
	}
	// 自动完成后归档；后续移除资源再回到进行中，无需删除或重建订阅。
	if err := s.RecheckFollows(ctx); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	var job model.FollowCheck
	for _, j := range jobs {
		if j.Kind == "movie" && j.TMDBID == 1 {
			job = j
		}
	}
	if job.TMDBID == 0 {
		t.Fatal("未找到检查任务")
	}
	if err := s.SaveFollowCheck(ctx, job, model.FollowProgress{State: "absent", CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	counts, err = s.FollowCounts(ctx, "alice", "", "")
	if err != nil || counts["completed"] != 27 || counts["ongoing"] != 2 {
		t.Fatal("删除后未回到进行中", counts, err)
	}
}
