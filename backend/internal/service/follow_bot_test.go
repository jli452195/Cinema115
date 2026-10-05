package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestFollowResourceMatches(t *testing.T) {
	target := MediaIdentity{Kind: "tv", TMDBID: 30983, Title: "名侦探柯南", Year: "1996"}
	missing := []EpisodeRef{{Season: 1, Episode: 1198, Label: "S01E1198"}}
	tests := []struct {
		name   string
		action tguser.Action
		want   bool
	}{
		{name: "same TMDB", action: tguser.Action{Title: "[日番] 名侦探柯南 (1996) {tmdb-30983} S01E1198 1.43G", TMDBID: "30983"}, want: true},
		{name: "same TMDB with alternate title", action: tguser.Action{Title: "[日番] 柯南电视版 (2026) {tmdb-30983}", TMDBID: "30983"}, want: true},
		{name: "different TMDB", action: tguser.Action{Title: "[日韩电影] 名侦探柯南：百万美元的五棱星 (2024) {tmdb-1209217}", TMDBID: "1209217"}},
		{name: "same title and year without ID", action: tguser.Action{Title: "[动漫] 名侦探柯南（1996）1080p 10G"}, want: true},
		{name: "semantic subtitle", action: tguser.Action{Title: "[动画电影] 名侦探柯南：章节 ONE (2017) 1.19G"}},
		{name: "year mismatch", action: tguser.Action{Title: "[动漫] 名侦探柯南 (2024) 10G"}},
		{name: "kind conflict", action: tguser.Action{Title: "[动画电影] 名侦探柯南 (1996) {tmdb-30983}", TMDBID: "30983"}},
		{name: "type word inside title is not a conflict", action: tguser.Action{Title: "[日番] 电影少女 (1996) {tmdb-30983}", TMDBID: "30983"}, want: true},
		{name: "missing year and ID", action: tguser.Action{Title: "[动漫] 名侦探柯南 1080p"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchResource(target, missing, ResourceCandidate{ID: tt.action.ID, Title: tt.action.Title, TMDBID: tt.action.TMDBID})
			if got.Matched != tt.want {
				t.Fatalf("MatchResource(%q) = %+v, want matched=%v", tt.action.Title, got, tt.want)
			}
		})
	}
}

func TestFollowResourceMatchesChineseTitleAndYear(t *testing.T) {
	target := MediaIdentity{Kind: "tv", TMDBID: 237233, Title: "昴宿七星", Year: "2018"}
	missing := []EpisodeRef{{Season: 1, Episode: 1, Label: "S01E01"}}
	if !MatchResource(target, missing, ResourceCandidate{Title: "[动漫] 昴宿七星 (2018) 3.85G"}).Matched {
		t.Fatal("没有用中文名和年份匹配无 TMDB 编号的资源")
	}
	if MatchResource(target, missing, ResourceCandidate{Title: "[动漫] 昴宿七星外传 (2018) 3.85G"}).Matched {
		t.Fatal("使用了中文标题包含匹配")
	}
}

type followBotFake struct {
	result     tguser.SearchResult
	selections map[string]tguser.SearchResult
}

type followResourceMatcherFake struct {
	err error
}

func (f followResourceMatcherFake) Match(_ context.Context, kind string, id int, candidates []ResourceCandidate) (ResourceMatchBatch, error) {
	if f.err != nil {
		results := make([]ResourceMatchDecision, 0, len(candidates))
		for _, candidate := range candidates {
			results = append(results, ResourceMatchDecision{ID: candidate.ID, Status: "unavailable"})
		}
		return ResourceMatchBatch{Status: "unavailable", Target: MediaIdentity{Kind: kind, TMDBID: id}, Results: results}, f.err
	}
	results := make([]ResourceMatchDecision, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, ResourceMatchDecision{ID: candidate.ID, Status: "matched", Matched: candidate.ID == "right"})
	}
	return ResourceMatchBatch{Status: "available", Target: MediaIdentity{Kind: kind, TMDBID: id}, Missing: []EpisodeRef{{Season: 1, Episode: 1, Label: "S01E01"}}, Results: results}, nil
}

func (f *followBotFake) Search(context.Context, string, bool) (tguser.SearchResult, error) {
	return f.result, nil
}
func (f *followBotFake) Select(_ context.Context, id string) (tguser.SearchResult, error) {
	return f.selections[id], nil
}

func TestFollowBotLinksRetriesWhenMissingEpisodesUnavailable(t *testing.T) {
	bot := &followBotFake{result: tguser.SearchResult{Items: []tguser.Action{{ID: "right", Kind: "resource", Title: "[日番] 测试剧集 (2020) S01"}}}}
	s := &FollowService{BotSearch: bot, ResourceMatcher: followResourceMatcherFake{err: ErrResourceMatchUnavailable}}
	links, err := s.followBotLinks(context.Background(), model.FollowBotJob{Kind: "tv", TMDBID: 9, Title: "测试剧集"})
	if !errors.Is(err, ErrResourceMatchUnavailable) || len(links) != 0 {
		t.Fatalf("unavailable match should be retried: links=%v err=%v", links, err)
	}
}

type followAccountFake struct{}

func (followAccountFake) ValidateSelection(id string) error {
	if id != "follow-account" {
		return ErrAccountMissing
	}
	return nil
}
func (followAccountFake) CookieFor(id string) string {
	if id == "follow-account" {
		return "UID=42_A; SEID=follow-cookie"
	}
	return ""
}

type followBatchFake struct {
	inputs    []GenerateInput
	accountID string
	cookie    string
}

type followNotificationFake struct {
	singleCount int
	batchCount  int
	botID       string
	inputCount  int
}

func (f *followNotificationFake) NotifyShareSuccess(context.Context, string, string, GenerateInput, *GenerationResult, error) error {
	f.singleCount++
	return nil
}

func (f *followNotificationFake) NotifyShareBatchSuccess(_ context.Context, botID, _ string, inputs []GenerateInput, _ []BatchOutcome) error {
	f.batchCount++
	f.botID = botID
	f.inputCount = len(inputs)
	return nil
}

func (f *followBatchFake) Generate(context.Context, GenerateInput) (*GenerationResult, error) {
	return nil, nil
}
func (f *followBatchFake) GenerateBatch(ctx context.Context, inputs []GenerateInput) ([]BatchOutcome, error) {
	f.inputs = append([]GenerateInput(nil), inputs...)
	f.accountID = ctx.Value(automationContextKey{}).(AutomationSettings).AccountID
	f.cookie = generationCookie(ctx, nil)
	result := make([]BatchOutcome, len(inputs))
	for i := range result {
		result[i].Result = &GenerationResult{Written: 1, Files: []GeneratedSTRM{{Path: "episode.strm"}}}
	}
	return result, nil
}

func TestFollowBotJobGeneratesAndWaitsForLibrary(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "follow-bot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveFollowSettings(ctx, model.FollowSettings{AutoEnabled: true, TelegramEnabled: true, BotID: "follow-bot"}); err != nil {
		t.Fatal(err)
	}
	follow := model.Follow{Kind: "tv", TMDBID: 237233, Title: "昴宿七星", Date: "2018-07-06"}
	if err := db.SubscribeWithAutomation(ctx, "alice", follow, nil, true); err != nil {
		t.Fatal(err)
	}
	checks, err := db.DueFollowChecks(ctx, time.Now().Unix())
	if err != nil || len(checks) != 1 {
		t.Fatal("没有先执行入库检查", checks, err)
	}
	if err := db.SaveFollowCheck(ctx, checks[0], model.FollowProgress{State: "absent", CheckedAt: time.Now().Unix()}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	job, err := db.NextFollowBotJob(ctx, testNow())
	if err != nil || job == nil {
		t.Fatal("没有领取自动搜索任务", err)
	}
	bot := &followBotFake{result: tguser.SearchResult{Items: []tguser.Action{
		{ID: "wrong", Kind: "resource", Title: "[动漫] 其他影视 (2018) 3G"},
		{ID: "right", Kind: "resource", Title: "[动漫] 昴宿七星 (2018) 3.85G"},
	}}, selections: map[string]tguser.SearchResult{"right": {Links: []string{"https://115.com/s/example?password=ayss", "https://115.com/s/second?password=ayss"}}}}
	automation, err := NewAutomationStore(filepath.Join(t.TempDir(), "automation.json"), t.TempDir(), "http://example.com")
	if err != nil {
		t.Fatal(err)
	}
	automation.ValidateAccount = followAccountFake{}.ValidateSelection
	automationSettings := automation.Value()
	automationSettings.Generation.OutputDir = "strm"
	automationSettings.AccountID = "follow-account"
	automationSettings.TGBatchWaitSeconds = 0
	if err := automation.Save(automationSettings); err != nil {
		t.Fatal(err)
	}
	generator := &followBatchFake{}
	notifications := &followNotificationFake{}
	shareTasks := &ShareTasks{
		Generator: generator, Store: db, Settings: automation, AccountCookie: followAccountFake{}.CookieFor,
		Notifier: notifications,
	}
	runCtx, stopRuns := context.WithCancel(ctx)
	runs := NewShareRuns(runCtx, shareTasks)
	done := make(chan struct{})
	go func() { defer close(done); runs.Run() }()
	defer func() { stopRuns(); <-done }()
	s := &FollowService{Store: db, BotSearch: bot, ResourceMatcher: followResourceMatcherFake{}, ShareRuns: runs, GenerationSettings: automation}
	if err := s.processFollowBotJob(ctx, *job); err != nil {
		t.Fatal(err)
	}
	if len(generator.inputs) != 2 || generator.inputs[0].URL != "https://115.com/s/example?password=ayss" || generator.inputs[1].URL != "https://115.com/s/second?password=ayss" || generator.accountID != "follow-account" || generator.cookie != "UID=42_A; SEID=follow-cookie" {
		t.Fatalf("生成任务没有固定追更配置：%+v account=%q cookie=%q", generator.inputs, generator.accountID, generator.cookie)
	}
	if notifications.singleCount != 0 || notifications.batchCount != 1 || notifications.botID != "follow-bot" || notifications.inputCount != 2 {
		t.Fatalf("订阅生成没有合并通知明确绑定的 Bot：%+v", notifications)
	}
	got, err := db.Follow(ctx, "alice", "tv", 237233)
	if err != nil || got == nil || got.Status != "processing" || got.Note != "资源待确认" {
		t.Fatalf("追更没有停在资源待确认：%+v %v", got, err)
	}
	if unread, err := db.WebsiteUnread(ctx, "alice"); err != nil || unread != 1 {
		t.Fatalf("资源待确认消息没有发送一次：%d %v", unread, err)
	}
}

func testNow() time.Time { return time.Now().Add(time.Second) }
