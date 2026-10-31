package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestP115AccountsRequireExplicitSelection(t *testing.T) {
	disk := &failingAccountStore{}
	s, err := newP115Accounts(disk, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatal("新安装不能自动创建默认账号")
	}
	id, err := s.Add("唯一账号")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; SEID=private"
	if err := s.Update(id, 1, "唯一账号", &cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(""); !errors.Is(err, ErrAccountMissing) {
		t.Fatal("空编号不能选择唯一账号")
	}
	if s.CookieFor("") != "" || s.ValidateSelection("") == nil {
		t.Fatal("功能必须明确选择账号")
	}
	views, err := json.Marshal(s.List())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(views, []byte("active")) || bytes.Contains(views, []byte("SEID")) {
		t.Fatal("列表不能包含全局选择或凭据")
	}
	if err := s.Update(id, 1, "旧页面", nil); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("必须阻止旧版本覆盖")
	}
	if err := s.Delete(id, 2); err != nil {
		t.Fatal(err)
	}
	restarted, err := newP115Accounts(disk, "")
	if err != nil || len(restarted.List()) != 0 {
		t.Fatal("删空后不能重新创建默认账号")
	}
}

type failingAccountStore struct {
	data string
	err  error
}

func (s *failingAccountStore) Value() string { return s.data }
func (s *failingAccountStore) Save(data string) error {
	if s.err != nil {
		return s.err
	}
	s.data = data
	return nil
}

func TestP115AccountsWriteFailurePreservesMemory(t *testing.T) {
	disk := &failingAccountStore{}
	s, err := newP115Accounts(disk, "original")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Add("备用账号")
	if err != nil {
		t.Fatal(err)
	}
	other := "other"
	if err := s.Update(id, 1, "备用账号", &other); err != nil {
		t.Fatal(err)
	}
	before := disk.data
	disk.err = errors.New("磁盘不可写")
	for _, mutate := range []func() error{
		func() error { return s.Update("default", 1, "新名称", &other) },
		func() error { return s.Delete(id, 2) },
		func() error { _, err := s.Add("无法保存"); return err },
	} {
		if err := mutate(); !errors.Is(err, disk.err) {
			t.Fatal("未保留存储错误")
		}
		if disk.data != before || s.CookieFor("default") != "original" || len(s.List()) != 2 || s.List()[0].Name != "账号 1" {
			t.Fatal("保存失败改变了内存或磁盘状态")
		}
	}
}

func TestShareTaskPinsAccountAtStart(t *testing.T) {
	for _, initial := range []string{"account-A", ""} {
		t.Run(initial, func(t *testing.T) {
			active := initial
			calls := 0
			current := func() string { calls++; return active }
			s, db, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
				// 模拟生成途中切换账号，后续步骤仍需沿用启动时的凭据（包括空值）。
				active = "account-B"
				if generationCookie(ctx, current) != initial {
					t.Fatal("生成任务中途切换了凭据")
				}
				return &GenerationResult{}, nil
			}))
			s.Cookie = current
			if _, err := s.Generate(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			var saved shareTaskRequest
			if err := json.Unmarshal(taskHistory(t, db)[0].Request, &saved); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || saved.Settings.AccountID != s.Settings.ShareValue().AccountID {
				t.Fatal("任务没有固定读取一次凭据并保存原账号编号")
			}
		})
	}
}
