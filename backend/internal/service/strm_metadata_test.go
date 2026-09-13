package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"strm115/backend/internal/client/p115"
)

type metadataClientFake struct {
	content []byte
	err     error
	reads   int
}

func (f *metadataClientFake) List(_ context.Context, _ *p115.ShareInfo, cid string, _ int, _ string) (*p115.SharePage, error) {
	if cid == "0" {
		return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "10", Name: "剧集", Directory: true}}}, nil
	}
	return &p115.SharePage{Count: 3, Files: []p115.ShareFile{{ID: "1", Name: "第1集.mkv", Size: 2 << 20}, {ID: "2", Name: "第1集.zh.srt", Size: int64(len(f.content))}, {ID: "3", Name: "说明.txt", Size: 10}}}, nil
}

func (f *metadataClientFake) ReadMetadata(_ context.Context, share *p115.ShareInfo, id, cookie string) ([]byte, error) {
	f.reads++
	if share.Password != "new1" || id != "2" || cookie != "test-cookie" {
		return nil, errors.New("元数据未使用实际分享或登录参数")
	}
	return f.content, f.err
}

func TestMetadataKeepsBytesPathsAndSTRMCounts(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "留空禁用", true: "字幕下载"}[enabled], func(t *testing.T) {
			// 非 UTF-8 字节也必须完整保存，不应被网页编码或换行转换破坏。
			fake := &metadataClientFake{content: []byte{0xff, 0xfe, 0x31, 0, 0x0d, 0, 0x0a, 0}}
			root := t.TempDir()
			input := generationOptions(t)
			input.Password = "new1"
			input.MinSizeMB = 1
			if enabled {
				input.MetadataExtensions = ".SRT，ass,ssa"
			}
			g := &STRMGenerator{Client: fake, Root: root, Cookie: func() string { return "test-cookie" }}
			result, err := g.Generate(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if result.Written != 1 || len(result.Files) != 1 {
				t.Fatal("元数据被计入 STRM 数量")
			}
			destination := filepath.Join(root, input.OutputDir, "剧集", "第1集.zh.srt")
			if enabled {
				data, err := os.ReadFile(destination)
				if err != nil || string(data) != string(fake.content) || result.MetadataWritten != 1 || fake.reads != 1 {
					t.Fatal("字幕原始内容或目录不正确", err)
				}
				if _, err := os.Stat(filepath.Join(root, input.OutputDir, "剧集", "第1集.zh.strm")); !os.IsNotExist(err) {
					t.Fatal("字幕错误生成了 STRM")
				}
			} else if _, err := os.Stat(destination); !os.IsNotExist(err) || fake.reads != 0 {
				t.Fatal("禁用后仍下载元数据")
			}
		})
	}
}

func TestMetadataFailurePreservesExistingFile(t *testing.T) {
	root := t.TempDir()
	input := generationOptions(t)
	input.Password = "new1"
	input.MetadataExtensions = "srt"
	destination := filepath.Join(root, input.OutputDir, "剧集", "第1集.zh.srt")
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("原有字幕"), 0600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("元数据下载失败")
	fake := &metadataClientFake{content: []byte("新字幕"), err: cause}
	result, err := (&STRMGenerator{Client: fake, Root: root, Cookie: func() string { return "test-cookie" }}).Generate(context.Background(), input)
	if !errors.Is(err, cause) || result == nil || result.MetadataWritten != 0 || result.MetadataFailed != 1 || result.Failed != 0 || result.Written != 1 {
		t.Fatal("未传播下载失败")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "原有字幕" {
		t.Fatal("失败覆盖了原有字幕")
	}
}

func TestMetadataCancellationIsNotFileFailure(t *testing.T) {
	input := generationOptions(t)
	input.Password = "new1"
	input.MetadataExtensions = "srt"
	fake := &metadataClientFake{content: []byte("字幕"), err: context.Canceled}
	result, err := (&STRMGenerator{Root: t.TempDir(), Client: fake, Cookie: func() string { return "test-cookie" }}).Generate(context.Background(), input)
	if !errors.Is(err, context.Canceled) || result == nil || result.MetadataFailed != 0 || result.MetadataWritten != 0 || result.Written != 1 {
		t.Fatal("取消不应计为元数据文件失败", err)
	}
}

func TestMetadataSettingsValidationAndPersistence(t *testing.T) {
	root := t.TempDir()
	store, err := NewAutomationStore(filepath.Join(root, "config.json"), root, "http://nas:8115")
	if err != nil {
		t.Fatal(err)
	}
	if store.Value().Generation.MetadataExtensions != "srt,ass,ssa" {
		t.Fatal("默认元数据后缀错误")
	}
	for _, suffix := range []string{"../srt", "strm", "mkv"} {
		value := store.Value()
		value.Generation.MetadataExtensions = suffix
		if err := store.Save(value); !errors.Is(err, ErrGenerationInput) {
			t.Fatal("非法或冲突后缀未拦截", suffix)
		}
	}
	value := store.Value()
	value.Generation.MetadataExtensions = ""
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewAutomationStore(filepath.Join(root, "config.json"), root, "http://nas:8115")
	if err != nil || reopened.Value().Generation.MetadataExtensions != "" {
		t.Fatal("禁用设置未保留", err)
	}
}
