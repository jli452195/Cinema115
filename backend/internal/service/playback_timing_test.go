package service

import (
	"context"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type timingCacheClient struct {
	*cacheDriveFake
}

func (f *timingCacheClient) InstantUploadToEmptyDirectory(ctx context.Context, info *p115.DownloadInfo, cid, cookie string, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	for _, stage := range []string{"target", "init", "range", "range_done", "init"} {
		progress(p115.InstantProgress{Stage: stage, Bytes: 140209})
	}
	return f.cacheDriveFake.InstantUploadToEmptyDirectory(ctx, info, cid, cookie, progress)
}

func TestPlaybackTimingStaysInOneRequestAndCoversFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, f, selected := setupPlaybackCache(t)
		s.client = &timingCacheClient{f}
		if fail {
			f.uploadErr = p115.ErrInstantUncertain
		}
		ctx, finish := s.logs.BeginRequest(context.Background(), true)
		_, _, err := s.ResolvePickCodeWithReport(ctx, selected, "abc123", "source-private", "player-UA")
		finish()
		if (err != nil) != fail {
			t.Fatal("wrong result", err)
		}
		entries := s.logs.Snapshot()
		if len(entries) != 1 || entries[0].Details == nil {
			t.Fatal("timing produced extra log rows")
		}
		steps := entries[0].Details.Steps
		if strings.Count(steps, "秒传链路耗时：") != 1 {
			t.Fatal("missing or repeated timing summary")
		}
		for _, stage := range []string{"等待缓存操作=", "源文件解析=", "秒传预检=", "秒传提交=", "二次校验片段读取=", "合计="} {
			if !strings.Contains(steps, stage) {
				t.Fatal("missing stage", stage)
			}
		}
		if strings.Contains(steps, "目标 SHA1 校验与取链=") == fail {
			t.Fatal("wrong final stage")
		}
		for _, secret := range []string{"source-private", "SEID=", "https://"} {
			if strings.Contains(steps, secret) {
				t.Fatal("timing leaked credentials")
			}
		}
		if fail && (f.links != 0 || s.Status().Entries[0].Status != "uncertain") {
			t.Fatal("failed write was used for playback")
		}
	}
}
