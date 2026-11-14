package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"strm115/backend/internal/recognition"
)

func TestRecognitionSettingsRevisionReloadAndRepair(t *testing.T) {
	prior := recognition.Current()
	t.Cleanup(func() { _ = recognition.Activate(prior) })
	ctx := context.Background()
	directory := t.TempDir()
	s, err := OpenRecognitionSettings(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.Read(ctx)
	if err != nil || initial.Revision != initial.ActiveRevision || initial.Groups != "" || initial.Platforms != "" || initial.LoadError != "" {
		t.Fatal(initial, err)
	}
	saved, err := s.Save(ctx, "# 保留注释\r\nLoliHouse\r\n", initial.Platforms)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(directory, "release-groups.txt")); err != nil || string(body) != saved.Groups {
		t.Fatal("没有保留TXT内容", err)
	}
	if _, err := s.Save(ctx, saved.Groups, saved.Platforms); err != nil {
		t.Fatal("丢失响应后的相同保存必须幂等", err)
	}
	if overwritten, err := s.Save(ctx, "Other", saved.Platforms); err != nil || overwritten.Groups != "Other" {
		t.Fatal("保存草稿应覆盖当前内容", err)
	}
	if _, err := s.Save(ctx, saved.Groups, saved.Platforms); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "[", saved.Platforms); !errors.Is(err, recognition.ErrConfig) {
		t.Fatal("无效规则必须拒绝", err)
	}
	if recognition.Current().Revision != saved.Revision {
		t.Fatal("失败保存修改了激活版本")
	}
	if err := os.WriteFile(filepath.Join(directory, "release-groups.txt"), []byte("["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reload(ctx); !errors.Is(err, recognition.ErrConfig) {
		t.Fatal("无效重载未拒绝", err)
	}
	broken, err := s.Read(ctx)
	if err != nil || broken.Diagnostic == nil || broken.ActiveRevision != saved.Revision || broken.Revision == saved.Revision {
		t.Fatal(broken, err)
	}
	if recognition.Current().Revision != saved.Revision {
		t.Fatal("失败重载丢失原快照")
	}
	if _, err := s.Save(ctx, saved.Groups, saved.Platforms); err != nil {
		t.Fatal("无法修复坏文件", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Save(cancelled, "Another", saved.Platforms); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if recognition.Current().Revision != saved.Revision {
		t.Fatal("取消保存改变了版本")
	}
}

func TestRecognitionSettingsMissingFileDoesNotResetOtherFile(t *testing.T) {
	prior := recognition.Current()
	t.Cleanup(func() { _ = recognition.Activate(prior) })
	directory := t.TempDir()
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(directory, "release-groups.txt"), []byte("Custom"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenRecognitionSettings(ctx, directory)
	if err != nil {
		t.Fatal("单个字幕组词表应独立生效", err)
	}
	doc, err := s.Read(ctx)
	if err != nil || doc.Groups != "Custom" || doc.Platforms != "" || doc.LoadError != "" {
		t.Fatal(doc, err)
	}
	result, err := recognition.Current().Recognize("[Custom] Show.NF.WEB-DL.mkv")
	if err != nil || len(result.ReleaseGroups) != 1 || result.ReleaseGroups[0] != "Custom" || len(result.Platforms) != 0 {
		t.Fatal("缺失的平台词表不能识别平台", result, err)
	}
	if _, err := s.Save(ctx, doc.Groups, "NF"); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(directory, "release-groups.txt")); err != nil || string(body) != "Custom" {
		t.Fatal("修复覆盖了已有组词表", err)
	}
}

func TestRecognitionSettingsMissingFilesStayEmptyUntilSave(t *testing.T) {
	prior := recognition.Current()
	t.Cleanup(func() { _ = recognition.Activate(prior) })
	ctx := context.Background()
	directory := filepath.Join(t.TempDir(), "not-created")
	s, err := OpenRecognitionSettings(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range []func(context.Context) (RecognitionDocument, error){s.Read, s.Reload} {
		doc, err := read(ctx)
		if err != nil || doc.Groups != "" || doc.Platforms != "" || doc.LoadError != "" {
			t.Fatal("缺失词表应视为空", doc, err)
		}
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("读取和启动不能创建数据目录", err)
	}
	fixedEmpty := recognition.Current()
	result, err := fixedEmpty.Recognize("[LoliHouse] Show.NF.WEB-DL.mkv")
	if err != nil || len(result.ReleaseGroups) != 0 || len(result.Platforms) != 0 {
		t.Fatal("未配置时不能识别", result, err)
	}
	if _, err := s.Save(ctx, "LoliHouse", "NF"); err != nil {
		t.Fatal("首次保存应创建目录和词表", err)
	}
	result, err = fixedEmpty.Recognize("[LoliHouse] Show.NF.WEB-DL.mkv")
	if err != nil || len(result.ReleaseGroups) != 0 || len(result.Platforms) != 0 {
		t.Fatal("已捕获空快照不能跟随新词表", result, err)
	}
	if err := os.Remove(filepath.Join(directory, "release-groups.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	result, err = recognition.Current().Recognize("[LoliHouse] Show.NF.WEB-DL.mkv")
	if err != nil || len(result.ReleaseGroups) != 0 || len(result.Platforms) != 1 || result.Platforms[0] != "NF" {
		t.Fatal("单独的平台词表应独立生效", result, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "release-groups.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("重载不能补写缺失词表", err)
	}
}

func TestRecognitionSettingsRecoversInterruptedTwoFileCommit(t *testing.T) {
	prior := recognition.Current()
	t.Cleanup(func() { _ = recognition.Activate(prior) })
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial", true: "completed"}[completed], func(t *testing.T) {
			directory := t.TempDir()
			ctx := context.Background()
			oldGroups, oldPlatforms := "# 旧版\r\nLoliHouse\r\n", "NF"
			newGroups, newPlatforms := "喵萌奶茶屋", "IQ ~= iQIYI|爱奇艺"
			write := func(name string, body []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, name), body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write("release-groups.txt", []byte(newGroups))
			if completed {
				write("platforms.txt", []byte(newPlatforms))
			} else {
				write("platforms.txt", []byte(oldPlatforms))
			}
			journal := recognitionJournal{Version: 1, Groups: []byte(oldGroups), Platforms: []byte(oldPlatforms), GroupsExisted: true, PlatformsExisted: true, NextRevision: recognition.Revision(newGroups, newPlatforms), PreviousRevision: recognition.Revision(oldGroups, oldPlatforms)}
			body, err := json.Marshal(journal)
			if err != nil {
				t.Fatal(err)
			}
			write(recognitionJournalName, body)
			s, err := OpenRecognitionSettings(ctx, directory)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := s.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			wantGroups, wantPlatforms := oldGroups, oldPlatforms
			if completed {
				wantGroups, wantPlatforms = newGroups, newPlatforms
			}
			if doc.Groups != wantGroups || doc.Platforms != wantPlatforms || doc.ActiveRevision != recognition.Revision(wantGroups, wantPlatforms) {
				t.Fatal("中断恢复激活了混合配置", doc)
			}
			if _, err := os.Stat(filepath.Join(directory, recognitionJournalName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("恢复后事务日志未清理", err)
			}
		})
	}
}

func TestRecognitionSettingsDamagedJournalDoesNotOverwriteFiles(t *testing.T) {
	prior := recognition.Current()
	t.Cleanup(func() { _ = recognition.Activate(prior) })
	directory := t.TempDir()
	for name, source := range map[string]string{"release-groups.txt": "LoliHouse", "platforms.txt": "NF", recognitionJournalName: `{"version":1,"nextRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := OpenRecognitionSettings(context.Background(), directory); !errors.Is(err, recognition.ErrConfig) {
		t.Fatal("损坏的事务日志应保留现场", err)
	}
	if body, err := os.ReadFile(filepath.Join(directory, "release-groups.txt")); err != nil || string(body) != "LoliHouse" {
		t.Fatal("损坏日志覆盖了现有文件", err)
	}
	if _, err := os.Stat(filepath.Join(directory, recognitionJournalName)); err != nil {
		t.Fatal("损坏日志应保留用于修复", err)
	}
}
