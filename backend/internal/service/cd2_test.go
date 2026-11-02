package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/clouddrive2"
)

type cd2TestClient struct {
	config clouddrive2.Config
	closed int
	files  map[string]clouddrive2.File
}

func (c *cd2TestClient) List(_ context.Context, directory string, _ bool) ([]clouddrive2.File, error) {
	files := make([]clouddrive2.File, 0, len(c.files))
	for _, f := range c.files {
		if f.Path != directory && filepath.ToSlash(filepath.Dir(f.Path)) == directory {
			files = append(files, f)
		}
	}
	return files, nil
}
func (c *cd2TestClient) Stat(_ context.Context, full string, _ bool) (clouddrive2.File, error) {
	f, ok := c.files[full]
	if !ok {
		return f, clouddrive2.ErrNotFound
	}
	return f, nil
}
func (c *cd2TestClient) CreateDirectory(context.Context, string, string) (clouddrive2.File, error) {
	return clouddrive2.File{}, errors.New("测试不执行写入")
}
func (c *cd2TestClient) Rename(context.Context, string, string) (string, error) {
	return "", errors.New("测试不执行写入")
}
func (c *cd2TestClient) Move(context.Context, []string, string) ([]string, error) {
	return nil, errors.New("测试不执行写入")
}
func (c *cd2TestClient) Delete(context.Context, []string) error {
	return errors.New("测试不执行写入")
}
func (c *cd2TestClient) Probe(context.Context) clouddrive2.Probe {
	return clouddrive2.Probe{Reachable: true, Authorized: true, Listable: true, Version: "1.1.2", Message: "只读检查通过"}
}
func (c *cd2TestClient) Close() error { c.closed++; return nil }

func cd2Fixture(t *testing.T, store accountSecretStore) (*CD2Connections, *[]*cd2TestClient) {
	t.Helper()
	s, err := NewCD2Connections(store)
	if err != nil {
		t.Fatal(err)
	}
	created := make([]*cd2TestClient, 0, 4)
	s.newClient = func(config clouddrive2.Config) (cd2Client, error) {
		client := &cd2TestClient{config: config, files: map[string]clouddrive2.File{
			"/":             {ID: "root", Name: "/", Path: "/", Directory: true},
			"/OtherCloud":   {ID: "other-root", Name: "OtherCloud", Path: "/OtherCloud", Directory: true},
			"/OtherAccount": {ID: "other-account", Name: "OtherAccount", Path: "/OtherAccount", Directory: true, ReadOnly: true},
		}}
		created = append(created, client)
		return client, nil
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, &created
}

func cd2Save(t *testing.T, s *CD2Connections, token string) CD2View {
	t.Helper()
	view, err := s.Save(context.Background(), CD2Input{Enabled: true, URL: "http://cd2.example:19798", Token: &token})
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func TestCD2EncryptedPersistenceAndSecretOmission(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "cd2.enc")
	store, err := NewSessionStore(filename, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := cd2Fixture(t, store)
	view := cd2Save(t, s, "saved-private-token")
	raw, _ := json.Marshal(view)
	disk, err := os.ReadFile(filename)
	if err != nil || strings.Contains(string(raw), "saved-private-token") || strings.Contains(string(disk), "saved-private-token") || !view.TokenConfigured {
		t.Fatal("连接凭据未正确加密或脱敏", err)
	}
	reloaded, err := NewSessionStore(filename, "test-key")
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := cd2Fixture(t, reloaded)
	if restarted.View() != view {
		t.Fatal("重启未保留连接配置")
	}
	retained, err := s.Save(context.Background(), CD2Input{Enabled: true, URL: view.URL})
	if err != nil || retained.Revision != view.Revision || !retained.TokenConfigured {
		t.Fatal("未传Token应保留配置", err)
	}
	disabled, err := s.Save(context.Background(), CD2Input{Enabled: false, URL: view.URL})
	if err != nil || disabled.Revision != view.Revision {
		t.Fatal("启停不应改变目录绑定版本", err)
	}
	empty := ""
	cleared, err := s.Save(context.Background(), CD2Input{Enabled: false, URL: view.URL, Token: &empty})
	if err != nil || cleared.TokenConfigured || cleared.Revision != view.Revision+1 {
		t.Fatal("显式清除Token失败", err)
	}
}

func TestCD2LeaseKeepsFrozenConnectionAndRetiresAfterRelease(t *testing.T) {
	s, created := cd2Fixture(t, &failingAccountStore{})
	view := cd2Save(t, s, "old-token")
	first, err := s.Acquire(context.Background(), view.ID, view.Revision)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Acquire(context.Background(), view.ID, view.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if first.Client != second.Client || len(*created) != 1 {
		t.Fatal("同一连接未复用")
	}
	updated := cd2Save(t, s, "new-token")
	if updated.Revision != view.Revision+1 || (*created)[0].closed != 0 {
		t.Fatal("配置变更中断了运行中的任务")
	}
	if _, err := s.Acquire(context.Background(), view.ID, view.Revision); !errors.Is(err, ErrCD2Changed) {
		t.Fatal("旧目录版本仍可创建新任务", err)
	}
	third, err := s.Acquire(context.Background(), updated.ID, updated.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if len(*created) != 2 || (*created)[0].config.Token != "old-token" || (*created)[1].config.Token != "new-token" {
		t.Fatal("任务连接快照发生漂移")
	}
	_ = first.Close()
	if (*created)[0].closed != 0 {
		t.Fatal("共享连接提前关闭")
	}
	_ = second.Close()
	_ = second.Close()
	if (*created)[0].closed != 1 {
		t.Fatal("旧连接未按引用释放")
	}
	_ = third.Close()
	_ = s.Close()
	if (*created)[1].closed != 1 {
		t.Fatal("空闲连接未关闭")
	}
	if _, err := s.Acquire(context.Background(), updated.ID, updated.Revision); !errors.Is(err, ErrCD2Unavailable) {
		t.Fatal(err)
	}
}

func TestCD2SaveFailureDoesNotPublishAndDraftTestDoesNotSave(t *testing.T) {
	store := &failingAccountStore{}
	s, created := cd2Fixture(t, store)
	view := cd2Save(t, s, "old-token")
	saved := store.data
	store.err = errors.New("磁盘保存失败")
	if _, err := s.Save(context.Background(), CD2Input{Enabled: true, URL: "http://other.example:19798"}); err == nil || s.View() != view || store.data != saved {
		t.Fatal("保存失败改变了运行配置")
	}
	result, err := s.Test(context.Background(), CD2Input{URL: "http://draft.example:19798"})
	if err != nil || !result.Authorized || s.View() != view || store.data != saved || len(*created) != 1 || (*created)[0].config.URL != "http://draft.example:19798" || (*created)[0].config.Token != "old-token" || (*created)[0].closed != 1 {
		t.Fatal("草稿测试改变持久配置或未释放连接", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Save(ctx, CD2Input{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCD2NativeDirectoryBrowsingWithout115(t *testing.T) {
	s, _ := cd2Fixture(t, &failingAccountStore{})
	view := cd2Save(t, s, "native-token")
	page, err := s.Directories(context.Background(), "/", view.Revision)
	if err != nil || page.ParentPath != nil || page.ConnectionID != view.ID || len(page.Directories) != 2 {
		t.Fatal(page, err)
	}
	page, err = s.Directories(context.Background(), "/OtherAccount", view.Revision)
	if err != nil || !page.ReadOnly || page.ParentPath == nil || *page.ParentPath != "/" {
		t.Fatal(page, err)
	}
	if _, err := s.Directories(context.Background(), "/", view.Revision+1); !errors.Is(err, ErrCD2Changed) {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), CD2Input{Enabled: false, URL: view.URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Directories(context.Background(), "/", view.Revision); !errors.Is(err, ErrCD2Unavailable) {
		t.Fatal(err)
	}
}
