package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestFlatShareWritesOriginalFilesWithoutOverwritingSameNames(t *testing.T) {
	f := &batchTransferFake{}
	s, db, input := setupShareTasks(t, nil)
	g := &STRMGenerator{Client: f, Cookie: s.Cookie, Root: s.Settings.root}
	w := NewShareWorkflow(f, g, s.Cookie, s.Settings, s.Settings.root, db)
	input.MinSizeMB = 0
	input.Password = ""
	input.Mode = "reshare"
	inputs := []GenerateInput{input, input}
	inputs[0].URL = "https://115.com/s/a"
	inputs[1].URL = "https://115.com/s/b"
	out, err := w.GenerateBatch(context.Background(), inputs)
	if err != nil || out[0].Err != nil || out[1].Err != nil {
		t.Fatal("合并生成失败", err, out)
	}
	if f.shares != 1 || len(f.shared) != 2 || f.shared[0].Directory || f.shared[1].Directory || f.shared[0].Name != "同名电影.mkv" {
		t.Fatal("分享根目录仍包含包装目录", f.shared)
	}
	for i, name := range []string{"同名电影.strm", "同名电影 (2).strm"} {
		data, err := os.ReadFile(filepath.Join(s.Settings.root, input.OutputDir, name))
		if err != nil || !strings.Contains(string(data), fmt.Sprintf("id=%d", 11001+i)) || !strings.Contains(string(data), "share_code=own") {
			t.Fatal("同名来源被覆盖或使用了错误文件 ID", name, err)
		}
	}
	// 重试第二个来源仍写入原来的后缀名称，且不会重写第一个来源。
	second := input
	second.URL = out[1].Result.ReshareURL
	second.CID = "0"
	second.Mode = "direct"
	r, err := g.Generate(context.WithValue(context.Background(), generationRootIDsKey{}, out[1].RetryRootIDs), second)
	if err != nil || r.Written != 1 || r.Files[0].Path != "同名电影 (2).strm" {
		t.Fatal("来源重试没有保持文件范围及名称", err)
	}
}

type folderBatchFake struct{ batchTransferFake }

func (f *folderBatchFake) ListForTransfer(_ context.Context, share *p115.ShareInfo, cid string, _ int, _ string) (*p115.SharePage, error) {
	if cid == "0" || cid == "" {
		return &p115.SharePage{OwnerID: "99", Count: 1, Files: []p115.ShareFile{{ID: "10", Name: "剧集", Directory: true}}}, nil
	}
	return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "11", Name: share.Code + ".mkv", Size: 100}}}, nil
}

func (f *folderBatchFake) ReceiveShare(_ context.Context, share *p115.ShareInfo, _ []string, cid, _ string) error {
	f.received = append(f.received, share.Code)
	id := strconv.Itoa(2000 + len(f.received))
	f.names[id] = "剧集"
	f.drive[cid] = append(f.drive[cid], p115.ShareFile{ID: id, Name: "剧集", Directory: true})
	f.drive[id] = []p115.ShareFile{{ID: strconv.Itoa(3000 + len(f.received)), Name: share.Code + ".mkv", Size: 100}}
	return nil
}

func (f *folderBatchFake) SendShare(ctx context.Context, ids []string, cookie string) (*p115.ShareInfo, error) {
	share, err := f.batchTransferFake.SendShare(ctx, ids, cookie)
	// 分享顺序与来源顺序相反，用内容校验区分同名目录，不能只按名称盲配。
	for i, j := 0, len(f.shared)-1; i < j; i, j = i+1, j-1 {
		f.shared[i], f.shared[j] = f.shared[j], f.shared[i]
	}
	return share, err
}

func TestFlatSharePreservesOriginalFoldersAndMatchesTheirContents(t *testing.T) {
	f := &folderBatchFake{}
	s, db, input := setupShareTasks(t, nil)
	g := &STRMGenerator{Client: f, Cookie: s.Cookie, Root: s.Settings.root}
	w := NewShareWorkflow(f, g, s.Cookie, s.Settings, s.Settings.root, db)
	input.MinSizeMB = 0
	input.Password = ""
	input.Mode = "reshare"
	inputs := []GenerateInput{input, input}
	inputs[0].URL = "https://115.com/s/a"
	inputs[1].URL = "https://115.com/s/b"
	out, err := w.GenerateBatch(context.Background(), inputs)
	if err != nil || out[0].Err != nil || out[1].Err != nil {
		t.Fatal("文件夹合并失败", err, out)
	}
	for _, root := range f.shared {
		if root.Name != "剧集" || !root.Directory {
			t.Fatal("没有保留原始目录", root)
		}
	}
	for i, relative := range []string{"剧集 (2)/a.strm", "剧集/b.strm"} {
		data, err := os.ReadFile(filepath.Join(s.Settings.root, input.OutputDir, filepath.FromSlash(relative)))
		if err != nil || !strings.Contains(string(data), fmt.Sprintf("id=%d", 13001+i)) {
			t.Fatal("同名目录混淆了来源或子文件", relative, err)
		}
	}
	if out[0].RetryRootIDs[0] != "12001" || out[1].RetryRootIDs[0] != "12002" {
		t.Fatal("来源范围使用了网盘 ID 而非新分享 ID")
	}
}
