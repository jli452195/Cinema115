package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mediaLocalMoveFixture(t *testing.T, body string) (*mediaOrganizeFiles, MediaOrganizeEntry) {
	t.Helper()
	files := &mediaOrganizeFiles{rule: MediaOrganizeRule{Storage: "local", Source: t.TempDir(), Target: t.TempDir()}}
	for _, root := range []*string{&files.rule.Source, &files.rule.Target} {
		var err error
		if *root, err = mediaLocalRoot(*root); err != nil {
			t.Fatal(err)
		}
	}
	mediaTestFile(t, files.rule.Source, "Work.S01E01.strm", body)
	entries, err := files.scan(context.Background())
	if err != nil || len(entries) != 1 || entries[0].LocalIdentity == "" {
		t.Fatal("主视频扫描必须保存原生身份", entries, err)
	}
	entry := entries[0]
	entry.Relative = "organized.strm"
	return files, entry
}

func TestMediaLocalNativeMoveNeverOverwrites(t *testing.T) {
	for _, occupied := range []bool{false, true} {
		name := "完整移动"
		if occupied {
			name = "目标已占用"
		}
		t.Run(name, func(t *testing.T) {
			files, entry := mediaLocalMoveFixture(t, "原文件正文")
			rule := files.rule
			target := filepath.Join(rule.Target, entry.Relative)
			if occupied {
				mediaTestFile(t, rule.Target, entry.Relative, "独立原目标")
			}
			err := files.move(context.Background(), entry)
			if occupied {
				if !errors.Is(err, errMediaTargetExists) {
					t.Fatal("目标占用必须明确拒绝", err)
				}
				if body, err := os.ReadFile(target); err != nil || string(body) != "独立原目标" {
					t.Fatal("原目标不得被覆盖", string(body), err)
				}
				if body, err := os.ReadFile(filepath.Join(rule.Source, entry.File.ID)); err != nil || string(body) != "原文件正文" {
					t.Fatal("冲突必须保留来源", string(body), err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if body, err := os.ReadFile(target); err != nil || string(body) != "原文件正文" {
				t.Fatal("目标必须保持原文件正文", string(body), err)
			}
			if _, err := os.Stat(filepath.Join(rule.Source, entry.File.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("成功移动后来源应消失", err)
			}
		})
	}
}

type mediaLocalMoveReplacingContext struct {
	context.Context
	replace func()
}

func (c *mediaLocalMoveReplacingContext) Err() error {
	if c.replace != nil {
		replace := c.replace
		c.replace = nil
		replace()
	}
	return nil
}

func TestMediaLocalMoveRejectsReplacementAfterPrecheck(t *testing.T) {
	files, entry := mediaLocalMoveFixture(t, "aaaa")
	rule := files.rule
	source := filepath.Join(rule.Source, entry.File.ID)
	before, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &mediaLocalMoveReplacingContext{Context: context.Background(), replace: func() {
		if err := os.Rename(source, source+".original"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte("bbbb"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(source, before.ModTime(), before.ModTime()); err != nil {
			t.Fatal(err)
		}
	}}
	if err := files.move(ctx, entry); err == nil {
		t.Fatal("首次核验后替换的文件不得被直接改名搬走")
	}
	if body, err := os.ReadFile(source); err != nil || string(body) != "bbbb" {
		t.Fatal("同名替代文件必须保持原处", string(body), err)
	}
	if _, err := os.Stat(filepath.Join(rule.Target, entry.Relative)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("不得发布替代文件为目标", err)
	}
	if pending, err := filepath.Glob(filepath.Join(rule.Target, ".cinema115-organize-*.tmp")); err != nil || len(pending) != 0 {
		t.Fatal("失败应清理自身临时硬链接及目录", pending, err)
	}
}

func TestMediaLocalMoveCheckpointBeforeSourceRemovalAndResume(t *testing.T) {
	for _, copied := range []bool{false, true} {
		name := "临时硬链接发布"
		if copied {
			name = "跨文件系统复制恢复"
		}
		t.Run(name, func(t *testing.T) {
			files, entry := mediaLocalMoveFixture(t, "原文件正文")
			rule := files.rule
			entry.Failure = &MediaOrganizeFailure{State: "moving"}
			source, target := filepath.Join(rule.Source, entry.File.ID), filepath.Join(rule.Target, entry.Relative)
			interrupted := errors.New("模拟目标身份持久化后退出")
			checkpoint := func(actual MediaOrganizeEntry, identity string) error {
				if body, err := os.ReadFile(source); err != nil || string(body) != "原文件正文" {
					t.Fatal("保存目标身份之前必须保留原来源", string(body), err)
				}
				actual.Failure.LocalIdentity = identity
				return interrupted
			}
			if copied {
				before, err := os.Stat(source)
				if err != nil {
					t.Fatal(err)
				}
				identity, err := mediaCopyLocalFile(context.Background(), source, target, before)
				if err != nil {
					t.Fatal(err)
				}
				_ = checkpoint(entry, identity)
				if entry.Failure.LocalIdentity == entry.LocalIdentity {
					t.Fatal("复制目标应有独立原生身份")
				}
			} else {
				files.localCheckpoint = checkpoint
				if err := files.move(context.Background(), entry); !errors.Is(err, interrupted) {
					t.Fatal("检查点失败应阻止删源", err)
				}
			}
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &entry); err != nil {
				t.Fatal(err)
			}
			restarted := &mediaOrganizeFiles{rule: rule}
			if err := restarted.move(context.Background(), entry); err != nil {
				t.Fatal("已确认目标和仍存在的原来源应能继续完成移动", err)
			}
			if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("恢复成功后只删除原来源", err)
			}
			if body, err := os.ReadFile(target); err != nil || string(body) != "原文件正文" {
				t.Fatal("恢复不得改写目标正文", string(body), err)
			}
		})
	}
}

func TestMediaLocalMoveRejectsReplacedNativeIdentity(t *testing.T) {
	for _, restarted := range []bool{false, true} {
		name := "当前扫描"
		if restarted {
			name = "持久化后重试"
		}
		t.Run(name, func(t *testing.T) {
			files, entry := mediaLocalMoveFixture(t, "aaaa")
			rule := files.rule
			entry.Status = "ready"
			source := filepath.Join(rule.Source, entry.File.ID)
			before, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(source, source+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("bbbb"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(source, before.ModTime(), before.ModTime()); err != nil {
				t.Fatal(err)
			}
			if restarted {
				raw, err := json.Marshal(entry)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &entry); err != nil {
					t.Fatal(err)
				}
				files = &mediaOrganizeFiles{rule: rule}
			}
			if err := files.move(context.Background(), entry); err == nil {
				t.Fatal("同尺寸同修改时间的新文件不能冒充扫描原件")
			}
			if body, err := os.ReadFile(source); err != nil || string(body) != "bbbb" {
				t.Fatal("替代文件必须保留原处", string(body), err)
			}
			if _, err := os.Stat(filepath.Join(rule.Target, entry.Relative)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("身份变化不能生成目标", err)
			}
			if !restarted {
				run := MediaOrganizeRun{Rule: rule, Entries: []MediaOrganizeEntry{entry}}
				if err := files.recheckLocalCompanions(context.Background(), &run); err != nil || run.Entries[0].Status != "retained" {
					t.Fatal("识别后的稳定性复查也必须发现原生身份替换", run.Entries, err)
				}
			}
		})
	}
}

type mediaLocalCopyInterruptedContext struct {
	context.Context
	reads int
	check func()
}

func (c *mediaLocalCopyInterruptedContext) Err() error {
	c.reads++
	if c.check != nil {
		c.check()
	}
	if c.reads > 1 {
		return context.Canceled
	}
	return nil
}

func TestMediaLocalCopyPublishesOnlyCompleteFileWithoutOverwrite(t *testing.T) {
	for _, scenario := range []string{"complete", "interrupted", "occupied"} {
		t.Run(scenario, func(t *testing.T) {
			source, target := filepath.Join(t.TempDir(), "source.strm"), filepath.Join(t.TempDir(), "target.strm")
			body := strings.Repeat("原文件正文\n", 20000)
			if err := os.WriteFile(source, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(source)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if scenario == "occupied" {
				if err := os.WriteFile(target, []byte("独立原目标"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "interrupted" {
				ctx = &mediaLocalCopyInterruptedContext{Context: ctx, check: func() {
					if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("复制完成之前不能暴露最终文件名", err)
					}
				}}
			}
			_, err = mediaCopyLocalFile(ctx, source, target, before)
			switch scenario {
			case "complete":
				data, readErr := os.ReadFile(target)
				if err != nil || readErr != nil || string(data) != body {
					t.Fatal("发布的目标必须是完整原文件", err, readErr)
				}
				if info, err := os.Stat(target); err != nil || !info.ModTime().Equal(before.ModTime()) {
					t.Fatal("跨挂载复制必须保留源修改时间", info, err)
				}
			case "interrupted":
				if !errors.Is(err, context.Canceled) {
					t.Fatal("复制中断必须向上返回", err)
				}
				if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("中断不能留下半成品目标", err)
				}
			case "occupied":
				data, readErr := os.ReadFile(target)
				if !errors.Is(err, errMediaTargetExists) || readErr != nil || string(data) != "独立原目标" {
					t.Fatal("完整复制后发布仍不能覆盖已有目标", err, readErr)
				}
			}
			if data, err := os.ReadFile(source); err != nil || string(data) != body {
				t.Fatal("复制阶段不得删除或改变来源", err)
			}
			if pending, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".cinema115-organize-*.tmp")); err != nil || len(pending) != 0 {
				t.Fatal("结束的复制应清理自身临时文件", pending, err)
			}
		})
	}
}
