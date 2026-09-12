package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type shareListFunc func(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error)

func (f shareListFunc) List(ctx context.Context, share *p115.ShareInfo, cid string, offset int, cookie string) (*p115.SharePage, error) {
	return f(ctx, share, cid, offset, cookie)
}

func generationOptions(t *testing.T) GenerateInput {
	t.Helper()
	return GenerateInput{URL: "https://115.com/s/demo?password=old", Password: "a+b&c", BaseURL: "http://192.168.0.24:3002", OutputDir: "strm", MinSizeMB: 1, Recursive: true}
}

func TestGenerateRecursivePaginatedDirectory(t *testing.T) {
	input := generationOptions(t)
	calls := 0
	client := shareListFunc(func(ctx context.Context, share *p115.ShareInfo, cid string, offset int, cookie string) (*p115.SharePage, error) {
		calls++
		if share.Password != "a+b&c" || cookie != "test-cookie" {
			t.Fatal("未传递独立提取码或当前会话")
		}
		if cid == "0" && offset == 0 {
			return &p115.SharePage{Count: 5, Files: []p115.ShareFile{{ID: "dir", Name: "Series", Directory: true}, {ID: "1", Name: "sample.mkv", Size: 2 << 20}, {ID: "2", Name: "sample.MP4", Size: 2 << 20}}}, nil
		}
		if cid == "0" && offset == 3 {
			return &p115.SharePage{Count: 5, Files: []p115.ShareFile{{ID: "3", Name: "字幕.srt", Size: 2 << 20}, {ID: "4", Name: "small.mp4", Size: 100}}}, nil
		}
		if cid == "dir" && offset == 0 {
			return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "5", Name: "电影.mkv", Size: 2 << 20}, {ID: "6", Name: `../../CON.mp4`, Size: 2 << 20}}}, nil
		}
		return nil, fmt.Errorf("意外分页 %s/%d", cid, offset)
	})
	g := &STRMGenerator{Root: t.TempDir(), Client: client, Cookie: func() string { return "test-cookie" }}
	result, err := g.Generate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(result.Files) != 4 || result.Skipped != 2 {
		t.Fatalf("分页或过滤错误：calls=%d files=%d skipped=%d", calls, len(result.Files), result.Skipped)
	}
	if result.Directory != filepath.Join(g.Root, input.OutputDir) || result.Written != 4 {
		t.Fatal("未直接写入所选目录")
	}
	paths := make(map[string]bool)
	for _, entry := range result.Files {
		if !filepath.IsLocal(filepath.FromSlash(entry.Path)) || strings.Contains(entry.Path, "../") {
			t.Fatalf("不安全的输出路径：%s", entry.Path)
		}
		if paths[strings.ToLower(entry.Path)] {
			t.Fatal("同名媒体覆盖")
		}
		paths[strings.ToLower(entry.Path)] = true
		body, err := os.ReadFile(filepath.Join(g.Root, input.OutputDir, filepath.FromSlash(entry.Path)))
		if err != nil || string(body) != entry.Content {
			t.Fatal("服务器保存内容不正确")
		}
		u, err := url.Parse(strings.TrimSpace(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		if u.Host != "192.168.0.24:3002" || u.Path != "/api/" || len(u.Query()) != 3 || u.Query().Get("receive_code") != "a+b&c" {
			t.Fatalf("生成了错误的播放地址：%s", u.Redacted())
		}
	}
	for _, name := range []string{"sample.strm", "sample (2).strm", "Series/电影.strm"} {
		if !paths[strings.ToLower(name)] {
			t.Fatalf("缺少文件 %s", name)
		}
	}
	second, err := g.Generate(context.Background(), input)
	if err != nil || second.Directory != result.Directory || second.Written != 4 {
		t.Fatal("重复生成应重新写入所有 STRM")
	}
	input.OutputDir = "strm2"
	third, err := g.Generate(context.Background(), input)
	if err != nil || third.Directory != filepath.Join(g.Root, input.OutputDir) || third.Written != 4 {
		t.Fatal("切换目录后应直接写入新目录")
	}
}

func TestGenerationFailureDoesNotLeavePartialFiles(t *testing.T) {
	upstreamErr := errors.New("上游不可用")
	for _, mode := range []string{"empty", "filtered", "upstream", "duplicate", "incomplete", "cycle", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			root := t.TempDir()
			g := &STRMGenerator{Root: root, Cookie: func() string { return "cookie" }, Client: shareListFunc(func(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error) {
				switch mode {
				case "empty":
					return &p115.SharePage{}, nil
				case "filtered":
					return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "1", Name: "notes.txt"}}}, nil
				case "upstream":
					return nil, upstreamErr
				case "duplicate":
					return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "1", Name: "a.mkv", Size: 2 << 20}, {ID: "1", Name: "a.mkv", Size: 2 << 20}}}, nil
				case "incomplete":
					return &p115.SharePage{Count: 10}, nil
				case "cycle":
					return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "0", Name: "parent", Directory: true}}}, nil
				default:
					cancel()
					return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "1", Name: "a.mkv", Size: 2 << 20}}}, nil
				}
			})}
			input := generationOptions(t)
			result, err := g.Generate(ctx, input)
			if err == nil || result != nil {
				t.Fatal("失败的扫描不能返回生成成功")
			}
			if mode == "upstream" && !errors.Is(err, upstreamErr) {
				t.Fatal("丢失上游错误链")
			}
			if mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("未响应取消")
			}
			if (mode == "empty" || mode == "filtered") && !errors.Is(err, ErrNoMedia) {
				t.Fatal("空结果未给出过滤提示")
			}
			files, err := os.ReadDir(root)
			if err != nil || len(files) != 0 {
				t.Fatal("失败后留下了部分输出")
			}
		})
	}
}

func TestGenerationRejectsInvalidConfigBeforeNetwork(t *testing.T) {
	for _, mutate := range []func(*GenerateInput){
		func(i *GenerateInput) { i.OutputDir = "../escape" },
		func(i *GenerateInput) { i.OutputDir = "/strm/../escape" },
		func(i *GenerateInput) { i.OutputDir = "/" },
		func(i *GenerateInput) { i.BaseURL = "javascript:alert(1)" },
		func(i *GenerateInput) { i.BaseURL = "http://user:password@example.com" },
		func(i *GenerateInput) { i.BaseURL = "http://example.com?token=secret" },
		func(i *GenerateInput) { i.URL = "https://example.com/s/test" },
		func(i *GenerateInput) { i.MinSizeMB = -1 },
		func(i *GenerateInput) { i.VideoExtensions = "mp4,../mkv" },
		func(i *GenerateInput) { i.CID = "../" },
	} {
		input := generationOptions(t)
		mutate(&input)
		g := &STRMGenerator{Root: t.TempDir(), Client: shareListFunc(func(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error) {
			t.Fatal("无效配置不应请求上游")
			return nil, nil
		})}
		if _, err := g.Generate(context.Background(), input); !errors.Is(err, ErrGenerationInput) {
			t.Fatalf("配置未被拒绝：%v", err)
		}
	}
}

func TestGenerationCanExcludeSubdirectories(t *testing.T) {
	input := generationOptions(t)
	input.Recursive = false
	input.CID = "123"
	g := &STRMGenerator{Root: t.TempDir(), Cookie: func() string { return "cookie" }, Client: shareListFunc(func(_ context.Context, _ *p115.ShareInfo, cid string, _ int, _ string) (*p115.SharePage, error) {
		if cid != "123" {
			t.Fatal("不应进入子目录或错误的起始目录")
		}
		return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "dir", Name: "sub", Directory: true}, {ID: "file", Name: "a.mp4", Size: 1 << 20}}}, nil
	})}
	result, err := g.Generate(context.Background(), input)
	if err != nil || len(result.Files) != 1 {
		t.Fatalf("目录开关或大小边界错误：%v", err)
	}
}

func TestRelativeOutputDirectoryResolution(t *testing.T) {
	root := t.TempDir()
	for _, raw := range []string{"strm", "strm2", "strm/电影"} {
		resolved, err := ResolveOutputDirectory(root, raw)
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(root, filepath.FromSlash(raw))
		if resolved != want {
			t.Fatalf("实际目录不正确：%s", resolved)
		}
	}
	for _, raw := range []string{"", "/strm", "../escape", "strm/../other", "strm/./other", "strm//other", "//server/share", "/", `D:/strm`, `strm\other`, "strm:other", "CON"} {
		if _, err := ResolveOutputDirectory(root, raw); err == nil {
			t.Fatalf("接受了无效目录：%s", raw)
		}
	}
}

func TestGenerationRefreshesFilesAndRegeneratesAfterOrganization(t *testing.T) {
	input := generationOptions(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, input.OutputDir), 0755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, input.OutputDir, "a.strm")
	if err := os.WriteFile(existing, []byte("existing content"), 0600); err != nil {
		t.Fatal(err)
	}
	g := &STRMGenerator{Root: root, Cookie: func() string { return "cookie" }, Client: shareListFunc(func(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error) {
		return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "1", Name: "a.mp4", Size: 2 << 20}}}, nil
	})}
	result, err := g.Generate(context.Background(), input)
	if err != nil || result.Written != 1 {
		t.Fatalf("已有文件未重新生成：%v", err)
	}
	content, err := os.ReadFile(existing)
	if err != nil || string(content) != result.Files[0].Content {
		t.Fatal("未写入最新分享内容")
	}
	organized := filepath.Join(root, "整理后的电影.strm")
	if err := os.Rename(existing, organized); err != nil {
		t.Fatal(err)
	}
	input.URL = "https://115.com/s/new-share"
	input.Password = "new-code"
	regenerated, err := g.Generate(context.Background(), input)
	if err != nil || regenerated.Written != 1 {
		t.Fatal("整理后无法再次生成", err)
	}
	current, err := os.ReadFile(existing)
	if err != nil || !strings.Contains(string(current), "share_code=new-share") || !strings.Contains(string(current), "receive_code=new-code") {
		t.Fatal("新生成文件未使用新分享参数")
	}
	preserved, err := os.ReadFile(organized)
	if err != nil || string(preserved) != string(content) {
		t.Fatal("影响了其他工具整理后的文件")
	}
	// 同名目录不能作为已生成的 STRM 计入成功结果。
	input.OutputDir = "strm2"
	if err := os.MkdirAll(filepath.Join(root, input.OutputDir, "a.strm"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Generate(context.Background(), input); err == nil {
		t.Fatal("同名目录应返回写入冲突")
	}
}

func TestCancelledSTRMWritePreservesPreviousContent(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "电影.strm")
	if err := os.WriteFile(destination, []byte("previous complete content"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeSTRM(ctx, destination, "replacement"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "previous complete content" {
		t.Fatal("取消时破坏旧文件")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("临时文件未清理")
	}
}
