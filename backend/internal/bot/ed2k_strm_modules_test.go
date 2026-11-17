package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

type ed2kSTRMModuleFake struct {
	targets     []model.ED2KSTRMTarget
	assignments int
	imports     int
}

func (f *ed2kSTRMModuleFake) Targets() []model.ED2KSTRMTarget {
	return append([]model.ED2KSTRMTarget(nil), f.targets...)
}
func (f *ed2kSTRMModuleFake) Target(id string) (model.ED2KSTRMTarget, bool) {
	for _, target := range f.targets {
		if target.ID == id {
			return target, true
		}
	}
	return model.ED2KSTRMTarget{}, false
}
func (f *ed2kSTRMModuleFake) AssignTarget(_ context.Context, id, botID string) error {
	for i := range f.targets {
		if f.targets[i].ID == id {
			f.assignments++
			f.targets[i].AssignedBotID = botID
			return nil
		}
	}
	return errors.New("目标不存在")
}
func (f *ed2kSTRMModuleFake) ImportToTarget(context.Context, model.ED2KSTRMTarget, []string, string) (model.ED2KSTRMImportResult, error) {
	f.imports++
	return model.ED2KSTRMImportResult{}, nil
}

func TestED2KSTRMModuleAssignmentEnablesOnlyAssignedBotPolling(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	h, err := NewHub(dir, "test-ed2k-modules", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.Add("ED2K Bot")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := h.Profiles().Get(id)
	profile.Token, profile.AllowedChatIDs = "123:test", "42"
	if err := h.Profiles().Update(profile, profile.Revision); err != nil {
		t.Fatal(err)
	}
	profile, _ = h.Profiles().Get(id)
	fake := &ed2kSTRMModuleFake{targets: []model.ED2KSTRMTarget{botED2KTarget("target-a", "电影", "ed2k/movies", ""), botED2KTarget("target-b", "剧集", "ed2k/tv", "another-bot")}}
	h.ED2KSTRM = fake
	m := h.newManager(profile, false)
	worker, err := m.newWorker(m.config)
	if err != nil {
		t.Fatal(err)
	}
	if m.config.Enabled || worker.ED2KSTRM != nil || len(worker.ED2KSTRMTargets) != 0 || pollsMessages(profile, "", false, false, len(h.ed2kSTRMTargets(id)) > 0) {
		t.Fatal("全局模块使未分配 Bot 开启了轮询或导入")
	}
	for len(h.wake) > 0 {
		<-h.wake
	}
	moduleID := ed2kSTRMModuleID("target-a")
	if err := h.SetModule(ctx, moduleID, "", id); err != nil {
		t.Fatal(err)
	}
	if fake.assignments != 1 {
		t.Fatal("分配未通过模块的持久化入口")
	}
	select {
	case <-h.wake:
	default:
		t.Fatal("分配后没有刷新 Bot 运行实例")
	}
	m = h.newManager(profile, false)
	worker, err = m.newWorker(m.config)
	if err != nil {
		t.Fatal(err)
	}
	if !m.config.Enabled || worker.ED2KSTRM == nil || len(worker.ED2KSTRMTargets) != 1 || worker.ED2KSTRMTargets[0].ID != "target-a" || worker.BotID != id || !pollsMessages(profile, "", false, false, true) {
		t.Fatal("独立 ED2K STRM 目标没有激活对应 Bot")
	}
	restarted, err := NewHub(dir, "test-ed2k-modules", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.ED2KSTRM = fake
	modules, err := restarted.Modules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, module := range modules {
		if module.ID == moduleID {
			found = true
			if module.Kind != "ed2k_strm" || module.AssignedBotID != id {
				t.Fatalf("模块没有从持久目标读取分配与 kind：%+v", module)
			}
		}
	}
	if !found {
		t.Fatal("功能列表缺少 ED2K STRM 目标")
	}
	if err := h.SetModule(ctx, moduleID, "", ""); !errors.Is(err, ErrModuleChanged) {
		t.Fatalf("过期分配未被拒绝：%v", err)
	}
	if fake.assignments != 1 {
		t.Fatal("过期分配覆盖了已保存目标")
	}
	if err := h.SetModule(ctx, moduleID, id, ""); err != nil {
		t.Fatal(err)
	}
	m = h.newManager(profile, false)
	worker, err = m.newWorker(m.config)
	if err != nil {
		t.Fatal(err)
	}
	if m.config.Enabled || worker.ED2KSTRM != nil || fake.assignments != 2 {
		t.Fatal("解除唯一 ED2K STRM 分配后仍提供导入功能")
	}
}

func TestED2KSTRMModuleDeletionRequiresAndPersistsRelease(t *testing.T) {
	ctx := context.Background()
	h, err := NewHub(t.TempDir(), "test-ed2k-delete", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := h.Add("ED2K Bot")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := h.Profiles().Get(id)
	fake := &ed2kSTRMModuleFake{targets: []model.ED2KSTRMTarget{botED2KTarget("target-a", "电影", "ed2k/movies", id)}}
	h.ED2KSTRM = fake
	if err := h.DeleteWithModules(ctx, id, profile.Revision, false); !errors.Is(err, ErrModuleAssigned) {
		t.Fatalf("删除没有提示仍绑定的目标：%v", err)
	}
	if err := h.DeleteWithModules(ctx, id, profile.Revision, true); err != nil {
		t.Fatal(err)
	}
	if fake.targets[0].AssignedBotID != "" || fake.assignments != 1 {
		t.Fatal("删除 Bot 未持久解除输出目标分配")
	}
	if _, err := h.Profiles().Get(id); !errors.Is(err, ErrBotMissing) {
		t.Fatal("Bot 没有删除")
	}
}

func TestED2KSTRMRunningWorkerRejectsReassignedTarget(t *testing.T) {
	h := &Hub{dataDir: t.TempDir(), settings: newBatchTestBot(t, nil).Settings}
	fake := &ed2kSTRMModuleFake{targets: []model.ED2KSTRMTarget{botED2KTarget("target-a", "电影", "ed2k/movies", "bot-a")}}
	h.ED2KSTRM = fake
	profile := BotProfile{ID: "bot-a", Token: "123:test", AllowedChatIDs: "42"}
	m := h.newManager(profile, false)
	worker, err := m.newWorker(m.config)
	if err != nil {
		t.Fatal(err)
	}
	state := telegramState{}
	update := cloudUpdate(8, 42, "private", "/ed2kstrm "+botED2KLink)
	worker.enqueueUpdates(context.Background(), &state, []telegramUpdate{update}, time.Now())
	if len(state.Queue) != 1 {
		t.Fatal("目标分配时未入队")
	}
	state.Batch, state.Queue = state.Queue[0], nil
	if err := fake.AssignTarget(context.Background(), "target-a", "bot-b"); err != nil {
		t.Fatal(err)
	}
	if err := worker.processBatchLink(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if fake.imports != 0 || state.Pending == nil {
		t.Fatal("运行中的旧 Bot 使用了已重新分配的目标")
	}
}
