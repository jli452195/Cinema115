package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/strm"
)

type embyDeleteTestDrive struct {
	calls [][]string
	reads int
	err   error
}

func (d *embyDeleteTestDrive) DeleteEntriesBatch(_ context.Context, parent string, ids []string, cookie string) error {
	d.calls = append(d.calls, append([]string{parent, cookie}, ids...))
	return d.err
}
func (d *embyDeleteTestDrive) ReadDirectoryFiles(context.Context, string, string) (p115.DriveDirectory, []p115.ShareFile, error) {
	d.reads++
	return p115.DriveDirectory{}, nil, nil
}

func embyDeleteSetup(t *testing.T) (*EmbyService, *embyDeleteTestDrive, []string) {
	t.Helper()
	drive, db, _, mapping, root := driveSTRMTestSetup(t)
	secret, err := NewSessionStore(filepath.Join(root, "emby.enc"), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewEmbyService(secret, db)
	if err != nil {
		t.Fatal(err)
	}
	s.config = EmbyConfig{URL: "http://emby.example", APIKey: "private", ServerID: "server", WebhookSecret: "hook", DeepDeleteEnabled: true}
	client := &embyDeleteTestDrive{}
	s.DeepDeleteAccounts, s.DeepDeleteDrive = drive.Accounts, client
	links := make([]string, 0, 3)
	records := make([]model.DriveSTRMFile, 0, 3)
	for i := 1; i <= 3; i++ {
		file := strm.Drive{FileID: fmt.Sprint(i), PickCode: "abcdef123456", SHA1: strings.Repeat("A", 40), Size: 100, Name: fmt.Sprintf("episode%d.mkv", i)}
		link, err := strm.DriveURL("https://cinema.example", file)
		if err != nil {
			t.Fatal(err)
		}
		parent := "200"
		if i == 3 {
			parent = "300"
		}
		records = append(records, model.DriveSTRMFile{MappingID: mapping.ID, FileID: file.FileID, ParentCID: parent, Path: file.Name + ".strm", Signature: strings.Repeat("A", 40) + ":100", Digest: driveSTRMDigest([]byte(link + "\n"))})
		links = append(links, link)
	}
	if err := db.SaveDriveSTRMFiles(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	return s, client, links
}

func embyDeleteNotify(t *testing.T, s *EmbyService, token, event, server string, links []string) (int, error) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"Event": event, "Server": map[string]string{"Id": server}, "Item": map[string]string{"Id": "item"}, "Description": "Item Name:\n测试\n\nItem Path:\n/library/test.strm\n\nMount Paths:\n" + strings.Join(links, "\r\n")})
	if err != nil {
		t.Fatal(err)
	}
	return s.Webhook(context.Background(), token, body)
}

func TestEmbyDeepDeleteBatchAndPersistentDedup(t *testing.T) {
	s, drive, links := embyDeleteSetup(t)
	ctx := context.Background()
	if code, err := embyDeleteNotify(t, s, "hook", "deep.delete", "server", append(links, links[0])); code != 202 || err != nil {
		t.Fatal(code, err)
	}
	if len(drive.calls) != 0 || drive.reads != 0 {
		t.Fatal("通知入队请求了115")
	}
	if err := s.processDeepDeletes(ctx); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"200", "UID=42_A", "1", "2"}, {"300", "UID=42_A", "3"}}
	if !reflect.DeepEqual(drive.calls, want) || drive.reads != 0 {
		t.Fatal("正常路径未按父目录合并或产生额外读取", drive.calls, drive.reads)
	}
	restored, err := NewEmbyService(s.secrets, s.db)
	if err != nil {
		t.Fatal(err)
	}
	restored.config = s.config
	restored.DeepDeleteAccounts, restored.DeepDeleteDrive = s.DeepDeleteAccounts, drive
	if code, err := embyDeleteNotify(t, restored, "hook", "deep.delete", "server", links); code != 202 || err != nil {
		t.Fatal(code, err)
	}
	if err := restored.processDeepDeletes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(drive.calls) != 2 {
		t.Fatal("重复通知重新发送了删除请求", drive.calls)
	}
}

func TestEmbyDeepDeleteRejectsUnmatchedAndNonIntentEvents(t *testing.T) {
	s, drive, links := embyDeleteSetup(t)
	for _, tc := range []struct {
		token, event, server string
		code                 int
	}{
		{"bad", "deep.delete", "server", 401}, {"hook", "deep.delete", "other", 403}, {"hook", "library.deleted", "server", 202},
	} {
		if code, _ := embyDeleteNotify(t, s, tc.token, tc.event, tc.server, links); code != tc.code {
			t.Fatal(tc, code)
		}
	}
	if err := s.processDeepDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(drive.calls) != 0 {
		t.Fatal("普通删除事件或未授权通知触发115删除")
	}
	invalid := []string{strings.Replace(links[0], "cinema.example", "other.example", 1), strings.Replace(links[0], "/100/", "/101/", 1), "https://cinema.example/play115/a", links[0] + "?token=secret", "/library/movie.mkv"}
	if code, err := embyDeleteNotify(t, s, "hook", "deep.delete", "server", invalid); code != 202 || err != nil {
		t.Fatal(code, err)
	}
	if err := s.processDeepDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(drive.calls) != 0 {
		t.Fatal("没有完整本地记录的链接触发删除")
	}
	for _, desc := range []string{"", "Item Path:\n" + links[0], "Mount Paths:\n", "Mount Paths:\nMount Paths:\n" + links[0]} {
		if _, err := embyDeepDeleteLinks(desc); err == nil {
			t.Fatal("接受无效通知", desc)
		}
	}
}

func TestEmbyDeepDeleteConfigAndQueuedIdentityChanges(t *testing.T) {
	for _, change := range []string{"disabled", "server", "credentials", "record"} {
		t.Run(change, func(t *testing.T) {
			s, drive, links := embyDeleteSetup(t)
			if code, err := embyDeleteNotify(t, s, "hook", "deep.delete", "server", links[:1]); code != 202 || err != nil {
				t.Fatal(code, err)
			}
			switch change {
			case "disabled":
				s.config.DeepDeleteEnabled = false
			case "server":
				s.config.URL = "http://other.example"
			case "credentials":
				mapping, _ := s.db.FindDriveSTRMDeleteCandidates(context.Background(), "1")
				m, _ := s.db.GetDriveSTRM(context.Background(), mapping[0].MappingID)
				a, _ := s.DeepDeleteAccounts.Get(m.AccountID)
				cookie := "UID=42_B"
				if err := s.DeepDeleteAccounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
					t.Fatal(err)
				}
			case "record":
				files, _ := s.db.FindDriveSTRMDeleteCandidates(context.Background(), "1")
				files[0].ParentCID = "999"
				if err := s.db.SaveDriveSTRMFiles(context.Background(), files); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.processDeepDeletes(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(drive.calls) != 0 {
				t.Fatal("配置变化后仍执行删除", drive.calls)
			}
		})
	}
	s, _, _ := embyDeleteSetup(t)
	if err := s.SaveDeepDeleteConfig(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	restored, err := NewEmbyService(s.secrets, s.db)
	if err != nil || !restored.Config().DeepDeleteEnabled {
		t.Fatal("开关未持久保存", err)
	}
	f := newEmbyFixture(t)
	old := f.service.snapshot()
	if err := f.service.SaveDeepDeleteConfig(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Save(context.Background(), old); err != nil || !f.service.Config().DeepDeleteEnabled {
		t.Fatal("旧连接表单覆盖独立开关", err)
	}
}

func TestEmbyDeepDeleteUncertainNeverReplays(t *testing.T) {
	s, drive, links := embyDeleteSetup(t)
	drive.err = errors.New("模拟超时")
	if code, err := embyDeleteNotify(t, s, "hook", "deep.delete", "server", links[:2]); code != 202 || err != nil {
		t.Fatal(code, err)
	}
	if err := s.processDeepDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(drive.calls) != 1 || drive.reads != 1 {
		t.Fatal("异常未合并核对", drive.calls, drive.reads)
	}
	if _, err := s.db.RecoverEmbyDeepDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := embyDeleteNotify(t, s, "hook", "deep.delete", "server", links[:2]); err != nil {
		t.Fatal(err)
	}
	if err := s.processDeepDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(drive.calls) != 1 || drive.reads != 1 {
		t.Fatal("结果未知的删除被重放")
	}
}

func TestEmbyDeepDeleteChangedRecordDoesNotDiscardOtherFiles(t *testing.T) {
	s, drive, links := embyDeleteSetup(t)
	ctx := context.Background()
	if code, err := embyDeleteNotify(t, s, "hook", "deep.delete", "server", links[:2]); code != 202 || err != nil {
		t.Fatal(code, err)
	}
	files, err := s.db.FindDriveSTRMDeleteCandidates(ctx, "1")
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	files[0].Digest = "changed"
	if err := s.db.SaveDriveSTRMFiles(ctx, files); err != nil {
		t.Fatal(err)
	}
	if err := s.processDeepDeletes(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(drive.calls, [][]string{{"200", "UID=42_A", "2"}}) {
		t.Fatal("一个失效目标影响了同目录其他有效视频", drive.calls)
	}
}

// 使用真实115客户端与模拟HTTP服务，验证正常批次确实只有一次网络请求。
func TestEmbyDeepDeleteOneHTTPRequest(t *testing.T) {
	s, _, links := embyDeleteSetup(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.Path != "/rb/delete" || r.Header.Get("Cookie") != "UID=42_A" {
			t.Error("请求目标错误", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("pid") != "200" || r.Form.Get("fid[0]") != "1" || r.Form.Get("fid[1]") != "2" {
			t.Error("批量身份错误", r.Form)
		}
		fmt.Fprint(w, `{"state":true}`)
	}))
	defer server.Close()
	client := p115.NewShareClient()
	client.WebURL = server.URL
	s.DeepDeleteDrive = client
	if code, err := embyDeleteNotify(t, s, "hook", "deep.delete", "server", links[:2]); code != 202 || err != nil {
		t.Fatal(code, err)
	}
	if err := s.processDeepDeletes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("正常删除产生多余请求", requests)
	}
}
