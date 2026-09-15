package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixedSharePasswordOnlyConfiguresNewShare(t *testing.T) {
	for _, tc := range []struct {
		name, fixed, urlPassword, explicit string
	}{
		{"他人相同密码仍转存", "ab12", "ab12", ""},
		{"手动相同密码仍转存", "ab12", "other", " ab12 "},
		{"手动密码覆盖链接密码", "ab12", "ab12", "zz99"},
		{"不同密码转存", "ab12", "zz99", ""},
		{"没有固定密码随机", "", "", ""},
		{"保留新密码大小写", "Ab12", "ab12", ""},
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
			result, err := w.Generate(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if !fake.deleted || !sharePasswordPattern.MatchString(fake.configuredPassword) {
				t.Fatal("未设置有效密码或未完成转存")
			}
			if tc.fixed != "" && fake.configuredPassword != tc.fixed {
				t.Fatal("未设置固定密码")
			}
			if !strings.HasSuffix(result.ReshareURL, "password="+fake.configuredPassword) {
				t.Fatal("结果链接密码未更新")
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
