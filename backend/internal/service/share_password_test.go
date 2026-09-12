package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixedSharePasswordControlsTransfer(t *testing.T) {
	for _, tc := range []struct {
		name, fixed, urlPassword, explicit string
		bypass                             bool
	}{
		{"链接密码匹配", "ab12", "ab12", "", true},
		{"手动密码优先匹配", "ab12", "other", " ab12 ", true},
		{"手动密码覆盖链接密码", "ab12", "ab12", "zz99", false},
		{"不同密码转存", "ab12", "zz99", "", false},
		{"没有固定密码随机", "", "", "", false},
		{"大小写精确匹配", "Ab12", "ab12", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &transferFake{}
			w, input := setupWorkflow(t, fake)
			settings := w.Settings.Value()
			settings.FixedSharePassword = tc.fixed
			if err := w.Settings.Save(settings); err != nil {
				t.Fatal(err)
			}
			input.URL = "https://115.com/s/source?password=" + tc.urlPassword
			input.Password = tc.explicit
			if tc.bypass {
				w.Generator = generateFunc(func(_ context.Context, in GenerateInput) (*GenerationResult, error) {
					if in != input {
						t.Error("直接生成改变了原分享参数")
					}
					return &GenerationResult{Written: 8}, nil
				})
			}
			result, err := w.Generate(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if tc.bypass {
				if len(fake.calls) != 0 || fake.listCalls != 0 || result.ReshareURL != "" || result.Written != 8 {
					t.Fatal("密码匹配仍执行了转存流程")
				}
				if _, err := os.Stat(w.JournalDir); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("直接生成不应创建转存记录")
				}
			} else {
				if !fake.deleted || !sharePasswordPattern.MatchString(fake.configuredPassword) {
					t.Fatal("未设置有效密码或未完成转存")
				}
				if tc.fixed != "" && fake.configuredPassword != tc.fixed {
					t.Fatal("未设置固定密码")
				}
				if !strings.HasSuffix(result.ReshareURL, "password="+fake.configuredPassword) {
					t.Fatal("结果链接密码未更新")
				}
			}
		})
	}
}

func TestFixedSharePasswordPersistsAndCanBeCleared(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "automation.json")
	store, err := NewAutomationStore(file, dir, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	settings := store.Value()
	settings.FixedSharePassword = " ab12 "
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewAutomationStore(file, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Value().FixedSharePassword != "ab12" {
		t.Fatal("固定密码未持久化")
	}
	for _, invalid := range []string{"123", "12345", "中文ab", "ab?1"} {
		settings.FixedSharePassword = invalid
		if err := store.Save(settings); !errors.Is(err, ErrGenerationInput) {
			t.Fatal("未拒绝无效密码", invalid)
		}
	}
	settings.FixedSharePassword = ""
	if err := store.Save(settings); err != nil {
		t.Fatal(err)
	}
	if store.Value().FixedSharePassword != "" {
		t.Fatal("无法清除固定密码")
	}
}
