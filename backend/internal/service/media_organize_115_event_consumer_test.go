package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

type media115EventFailingStore struct {
	mediaOrganizeStorage
	fail bool
}

func (s *media115EventFailingStore) SaveMediaOrganize(ctx context.Context, rules string, updates []storage.MediaOrganizeRunRow, removed []string) error {
	if s.fail {
		return errors.New("模拟扫描交接事务失败")
	}
	return s.mediaOrganizeStorage.SaveMediaOrganize(ctx, rules, updates, removed)
}

type media115EventTestDrive struct {
	*mediaCleanupDrive
	calls  int
	before func(string, string) error
}

func (d *media115EventTestDrive) ReadDirectory(ctx context.Context, id, cookie string) (p115.DriveDirectory, error) {
	d.calls++
	if d.before != nil {
		if err := d.before(id, cookie); err != nil {
			return p115.DriveDirectory{}, err
		}
	}
	return d.mediaCleanupDrive.ReadDirectory(ctx, id, cookie)
}

func newMedia115EventConsumerTest(t *testing.T) (*MediaOrganizer, *media115EventTestDrive, MediaOrganizeRule) {
	t.Helper()
	s, base, rule := newMediaCleanupCloud(t)
	rule.RealtimeEnabled = true
	s.doc.Rules[0] = rule
	if err := s.persistLocked(); err != nil {
		t.Fatal(err)
	}
	drive := &media115EventTestDrive{mediaCleanupDrive: base}
	s.drive = drive
	s.EnableLifeEventConsumer()
	return s, drive, rule
}

func TestMedia115EventConsumerPersistsAndMergesQueuedScopes(t *testing.T) {
	s, drive, rule := newMedia115EventConsumerTest(t)
	drive.paths["30"], drive.paths["31"] = "/inbox/First", "/inbox/Second"
	ctx := context.Background()
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"30", "30"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"31"}, nil); err != nil {
		t.Fatal(err)
	}
	if drive.calls != 2 || len(s.doc.Runs) != 1 || len(s.doc.Runs[0].scanScopes) != 2 {
		t.Fatal("同目录应去重，未开始的扫描应合并两个作品范围", drive.calls, s.doc.Runs)
	}
	rows, err := s.db.LoadMediaOrganize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored := &MediaOrganizer{}
	if err := restored.decodeSQLite(rows); err != nil {
		t.Fatal(err)
	}
	if len(restored.doc.Runs) != 1 || !reflect.DeepEqual(restored.doc.Runs[0].scanScopes, s.doc.Runs[0].scanScopes) {
		t.Fatal("确认消费前必须保存局部扫描范围", restored.doc.Runs)
	}
}

func TestMedia115EventConsumerDoesNotAcknowledgeActiveOldScan(t *testing.T) {
	s, drive, rule := newMedia115EventConsumerTest(t)
	drive.paths["30"], drive.paths["31"] = "/inbox/First", "/inbox/Second"
	ctx := context.Background()
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"30"}, nil); err != nil {
		t.Fatal(err)
	}
	run, _, ok := s.takeQueuedRunLocked()
	if !ok {
		t.Fatal("缺少已交接任务")
	}
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"31"}, nil); !errors.Is(err, ErrMediaOrganizeBusy) {
		t.Fatal("运行中旧任务不能确认新变更", err)
	}
	if len(run.scanScopes) != 1 || run.scanScopes[0].ID != "30" || len(s.cloudMonitors[rule.ID].pending) != 0 {
		t.Fatal("新变更由消费重试保留，不能触发调度器反复补扫", run.scanScopes)
	}
}

func TestMedia115EventConsumerRetriesFailedQueueCommit(t *testing.T) {
	s, drive, rule := newMedia115EventConsumerTest(t)
	drive.paths["30"] = "/inbox/Work"
	store := s.db.(*testMediaOrganizeStore).source.(*failingAccountStore)
	store.err = errors.New("模拟队列写入失败")
	ctx := context.Background()
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"30"}, nil); err == nil {
		t.Fatal("队列保存失败不能确认消费")
	}
	if len(s.actions) != 0 || len(s.doc.Runs) != 0 || len(s.cloudMonitors[rule.ID].pending) != 0 {
		t.Fatal("失败后应由消费重试保留事件并恢复任务视图", s.doc.Runs, s.actions)
	}
	store.err = nil
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"30"}, nil); err != nil {
		t.Fatal(err)
	}
	if drive.calls != 1 || len(s.actions) != 1 {
		t.Fatal("重试应复用已经核验的目录，仅重新保存任务", drive.calls, s.actions)
	}
}

func TestMedia115EventConsumerReceiptKeepsPartialRuleHandoff(t *testing.T) {
	s, drive, rule := newMedia115EventConsumerTest(t)
	other := rule
	other.ID, other.SourceID, other.Source, other.Classify = "nested-rule", "30", "/inbox/Work", true
	s.doc.Rules = append(s.doc.Rules, other)
	s.EnableLifeEventConsumer()
	drive.paths["30"] = "/inbox/Work"
	ctx := context.Background()
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"30"}, nil, "batch-1"); !errors.Is(err, ErrClassificationUnavailable) {
		t.Fatal("第二条规则应等待分类服务，第一条已经安全交接", err)
	}
	run, _, ok := s.takeQueuedRunLocked()
	if !ok || run.Rule.ID != rule.ID {
		t.Fatal("缺少第一条规则的持久任务", run)
	}
	s.doc.Rules[1].Classify = false
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"30"}, nil, "batch-1"); err != nil {
		t.Fatal("相同批次不能被已交接规则的活动任务阻塞", err)
	}
	if len(s.doc.Runs) != 2 || len(s.cloudMonitors[rule.ID].pending) != 0 {
		t.Fatal("第一条规则不能重复安排扫描", s.doc.Runs)
	}
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"30"}, nil, "batch-2"); !errors.Is(err, ErrMediaOrganizeBusy) {
		t.Fatal("同目录的新批次不能复用旧批次确认", err)
	}
}

func TestMedia115EventConsumerVerifiesRedirectedMissingDirectory(t *testing.T) {
	s, _, rule := newMedia115EventConsumerTest(t)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/files":
			_, _ = w.Write([]byte(`{"state":true,"cid":0,"path":[{"cid":0}],"data":[]}`))
		case "/files/get_info":
			_, _ = w.Write([]byte(`{"state":false,"code":20018}`))
		default:
			t.Errorf("不应扫描未知目录内容: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := p115.NewShareClient()
	client.HTTP, client.WebURL, client.RequestInterval = server.Client(), server.URL, 0
	s.drive = client
	if err := s.consume115EventDirectories(context.Background(), rule.AccountID, []string{"90"}, nil); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/files", "/files/get_info"}) || len(s.actions) != 0 {
		t.Fatal("只在精确身份接口确认删除后忽略，不触发补扫", paths, s.actions)
	}
}

func TestMedia115EventConsumerInvalidatesKnownDescendants(t *testing.T) {
	s, drive, rule := newMedia115EventConsumerTest(t)
	runtime := s.cloudMonitors[rule.ID]
	runtime.directories["30"], runtime.directories["31"] = "Old", "Old/Season 01"
	drive.paths["31"] = "/inbox/New/Season 01"
	if err := s.consume115EventDirectories(context.Background(), rule.AccountID, []string{"31"}, []string{"30"}); err != nil {
		t.Fatal(err)
	}
	if drive.calls != 1 || len(s.doc.Runs[0].scanScopes) != 1 || s.doc.Runs[0].scanScopes[0].Relative != "New/Season 01" {
		t.Fatal("目录移动后子目录应重新核验，不能沿用旧相对路径", drive.calls, s.doc.Runs)
	}
}

func TestMedia115EventConsumerCachesOnlyVerifiedDirectories(t *testing.T) {
	s, drive, rule := newMedia115EventConsumerTest(t)
	now := time.Now()
	s.monitorOptions.now = func() time.Time { return now }
	drive.paths["90"] = "/outside/Other"
	ctx := context.Background()
	for range 2 {
		if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"90"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if drive.calls != 1 || len(s.doc.Runs) != 0 {
		t.Fatal("范围外成功核验应短期复用，不创建扫描", drive.calls)
	}
	now = now.Add(61 * time.Second)
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"90"}, nil); err != nil || drive.calls != 2 {
		t.Fatal("目录缓存到期后应重新核验", err, drive.calls)
	}
	drive.paths["90"] = "/inbox/Moved"
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"90"}, []string{"90"}); err != nil {
		t.Fatal(err)
	}
	if drive.calls != 3 || len(s.doc.Runs) != 1 {
		t.Fatal("移动事件必须使范围外缓存失效", drive.calls, s.doc.Runs)
	}
	drive.before = func(string, string) error { return context.DeadlineExceeded }
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"91"}, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("网络异常不能当成目录不存在", err)
	}
	drive.before = nil
	drive.paths["91"] = "/outside/Recovered"
	if err := s.consume115EventDirectories(ctx, rule.AccountID, []string{"91"}, nil); err != nil || drive.calls != 5 {
		t.Fatal("网络异常不得写入负缓存", err, drive.calls)
	}
}

func TestMedia115EventConsumerRejectsStaleResolutionAndChangedAccount(t *testing.T) {
	for _, scenario := range []string{"invalidate", "account", "rule"} {
		t.Run(scenario, func(t *testing.T) {
			s, drive, rule := newMedia115EventConsumerTest(t)
			drive.paths["30"] = "/inbox/Work"
			drive.before = func(string, string) error {
				switch scenario {
				case "invalidate":
					s.invalidate115EventDirectories(rule.AccountID, []string{"30"})
				case "account":
					s.accounts.mu.Lock()
					s.accounts.value.Accounts[0].Revision++
					s.accounts.mu.Unlock()
				case "rule":
					s.mu.Lock()
					s.doc.Rules[0].Source = "/other"
					s.mu.Unlock()
				}
				return nil
			}
			err := s.consume115EventDirectories(context.Background(), rule.AccountID, []string{"30"}, nil)
			if scenario == "invalidate" && !errors.Is(err, ErrMediaOrganizeBusy) || scenario == "account" && !errors.Is(err, ErrAccountChanged) {
				t.Fatal("旧核验结果不能提交", err)
			}
			if len(s.doc.Runs) != 0 {
				t.Fatal("旧来源或旧规则核验结果不能创建任务", s.doc.Runs)
			}
		})
	}
}

func TestMedia115EventConsumerAvoidsStartupFullScanAndInitializesNewRules(t *testing.T) {
	s, _, rule := newMedia115EventConsumerTest(t)
	now := time.Now()
	s.refreshCloudMonitorsLocked(now)
	runtime := s.cloudMonitors[rule.ID]
	if runtime.full || !runtime.due.IsZero() {
		t.Fatal("接入共享游标后已有规则不能每次启动全扫", runtime)
	}
	newRule := rule
	newRule.ID, newRule.SourceID, newRule.Source = "new-rule", "30", "/another"
	s.doc.Rules = append(s.doc.Rules, newRule)
	s.refreshCloudMonitorsLocked(now)
	if next := s.cloudMonitors[newRule.ID]; !next.full || next.due.IsZero() {
		t.Fatal("新来源仍需要首次补扫", next)
	}
}

func TestMedia115EventConsumerSQLiteRestartRestoresQueuedScopes(t *testing.T) {
	s, _, rule := newMedia115EventConsumerTest(t)
	disk, db, dir := newMediaSQLiteTest(t)
	disk.accounts, disk.drive, disk.doc.Rules = s.accounts, s.drive, []MediaOrganizeRule{rule}
	disk.EnableLifeEventConsumer()
	runtime := disk.cloudMonitors[rule.ID]
	runtime.directories["30"] = "Work"
	if err := disk.consume115EventDirectories(context.Background(), rule.AccountID, []string{"30"}, nil); err != nil {
		t.Fatal(err)
	}
	queuedID := disk.doc.Runs[0].ID
	restarted, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, organizeDefaultTemplates{}, nil, disk.accounts, disk.drive, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.EnableLifeEventConsumer()
	if restarted.actions[queuedID] != "auto" || restarted.doc.Runs[0].Status != "queued" || len(restarted.doc.Runs[0].scanScopes) != 1 || restarted.cloudMonitors[rule.ID].full {
		t.Fatal("仅恢复未开始的持久化局部扫描", restarted.doc.Runs, restarted.actions)
	}
}

func TestMedia115EventConsumerSQLiteRestoresQueuedManualAndScheduleHandoff(t *testing.T) {
	for _, trigger := range []string{"manual", "schedule"} {
		t.Run(trigger, func(t *testing.T) {
			s, _, rule := newMedia115EventConsumerTest(t)
			disk, db, dir := newMediaSQLiteTest(t)
			disk.accounts, disk.drive, disk.doc.Rules = s.accounts, s.drive, []MediaOrganizeRule{rule}
			disk.EnableLifeEventConsumer()
			run, err := disk.queueTriggeredScanLocked(rule, trigger)
			if err != nil {
				t.Fatal(err)
			}
			if err := disk.consume115EventDirectories(context.Background(), rule.AccountID, []string{rule.SourceID}, nil); err != nil {
				t.Fatal(err)
			}
			restarted, err := OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, organizeDefaultTemplates{}, nil, disk.accounts, disk.drive, nil)
			if err != nil {
				t.Fatal(err)
			}
			if restarted.actions[run.ID] != "auto" || restarted.doc.Runs[0].Status != "queued" {
				t.Fatal("覆盖事件的未开始扫描应安全恢复", restarted.doc.Runs, restarted.actions)
			}
		})
	}
}

func TestMedia115EventConsumerSQLiteInterruptedScanKeepsRepairUntilQueued(t *testing.T) {
	s, _, rule := newMedia115EventConsumerTest(t)
	disk, db, dir := newMediaSQLiteTest(t)
	disk.accounts, disk.drive, disk.doc.Rules = s.accounts, s.drive, []MediaOrganizeRule{rule}
	old := MediaOrganizeRun{ID: "interrupted-event", Rule: rule, Trigger: "realtime", Status: "scanning", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	disk.doc.Runs = []MediaOrganizeRun{old}
	if err := disk.persistLocked(old.ID); err != nil {
		t.Fatal(err)
	}
	var restarted *MediaOrganizer
	for range 2 {
		var err error
		restarted, err = OpenMediaOrganizerSQLite(context.Background(), db, dir, "test", nil, organizeDefaultTemplates{}, nil, disk.accounts, disk.drive, nil)
		if err != nil {
			t.Fatal(err)
		}
		restarted.EnableLifeEventConsumer()
		if runtime := restarted.cloudMonitors[rule.ID]; !runtime.full || len(runtime.eventRecoveryIDs) != 1 {
			t.Fatal("补扫持久化前再次退出也必须保留恢复证据", runtime)
		}
	}
	failing := &media115EventFailingStore{mediaOrganizeStorage: restarted.db, fail: true}
	restarted.db = failing
	if err := restarted.schedule(); err == nil {
		t.Fatal("补扫交接失败必须保留恢复意图")
	}
	if len(restarted.doc.Runs) != 1 || restarted.doc.Runs[0].Status != "interrupted" {
		t.Fatal("事务回滚不能把原磁盘活动记录误认为仍在运行", restarted.doc.Runs)
	}
	failing.fail = false
	if err := restarted.schedule(); err != nil {
		t.Fatal(err)
	}
	if runtime := restarted.cloudMonitors[rule.ID]; runtime.full || len(runtime.eventRecoveryIDs) != 0 || len(restarted.actions) != 1 {
		t.Fatal("安全补扫入队后才解除恢复标记", runtime, restarted.actions)
	}
}

func TestMedia115EventConsumerRecoverySurvivesHistoryCleanup(t *testing.T) {
	s, _, rule := newMedia115EventConsumerTest(t)
	disk, db, dir := newMediaSQLiteTest(t)
	disk.accounts, disk.drive, disk.doc.Rules = s.accounts, s.drive, []MediaOrganizeRule{rule}
	old := mediaRecordFixture("event-recovery", "running", 1)
	old.Rule, old.Trigger = rule, "realtime"
	old.Entries[0].Status, old.Entries[0].Result = "failed", nil
	disk.doc.Runs = []MediaOrganizeRun{old}
	if err := disk.persistLocked(old.ID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	restarted, err := OpenMediaOrganizerSQLite(ctx, db, dir, "test", nil, organizeDefaultTemplates{}, nil, disk.accounts, disk.drive, nil)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, err := restarted.DeleteHistory(ctx, MediaOrganizeDelete{Status: "failed"}); err != nil || deleted != 0 {
		t.Fatal("补扫交接前批量清理不能删除恢复证据", deleted, err)
	}
	if _, err := restarted.DeleteHistory(ctx, MediaOrganizeDelete{Items: []MediaOrganizeDeleteItem{{RunID: old.ID, FileID: old.Entries[0].File.ID}}}); !errors.Is(err, ErrMediaOrganizeBusy) {
		t.Fatal("补扫交接前单条清理应明确拒绝", err)
	}
	if err := restarted.persistLocked(old.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err = OpenMediaOrganizerSQLite(ctx, db, dir, "test", nil, organizeDefaultTemplates{}, nil, disk.accounts, disk.drive, nil)
	if err != nil {
		t.Fatal(err)
	}
	if runtime := restarted.cloudMonitors[rule.ID]; !runtime.full || len(runtime.eventRecoveryIDs) != 1 {
		t.Fatal("历史保存或清理后再次启动仍应安排来源补扫", runtime)
	}
	if err := restarted.schedule(); err != nil {
		t.Fatal(err)
	}
	if restarted.eventRecoveryPendingLocked(old.ID) || len(restarted.actions) != 1 {
		t.Fatal("替代扫描安全入队后才解除专用历史保护", restarted.actions)
	}
}
