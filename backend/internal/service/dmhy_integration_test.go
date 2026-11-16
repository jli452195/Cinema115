package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/utils/torrentmeta"
)

func TestDmhyDownloadKeepsIndependentVerifiedManifest(t *testing.T) {
	s, drive, adder, _, _, _ := gardenFixture(t)
	reads := 0
	s.Resources = downloadReaderFunc(func(_ context.Context, in DownloadResourceInput) (DownloadFiles, error) {
		reads++
		if in.Source != "dmhy" || in.Provider != "" || in.ProviderID != "688777" {
			t.Fatalf("动漫花园资源被改为其他来源: %+v", in)
		}
		return organizeResourceFiles(gardenTestHash, []torrentmeta.File{{Path: "YuYu.Hakusho.S01E01.mkv", Size: 100}})
	})
	in := DownloadResourceInput{Source: "dmhy", ProviderID: "688777", Title: "YuYu Hakusho [01][1080p]", Magnet: "magnet:?xt=urn:btih:" + gardenTestHash}
	out, err := s.Files(context.Background(), in)
	if err != nil || reads != 1 || len(out.Files) != 1 || out.Files[0].TitleOnly || out.ManifestID == "" {
		t.Fatalf("未复用原站真实清单: %+v reads=%d err=%v", out, reads, err)
	}
	if len(drive.listCalls) != 0 || drive.creates != 0 || adder.url != "" {
		t.Fatal("准备种子清单不应访问 115")
	}
	if _, err := (&DownloadResources{}).Files(context.Background(), in); !errors.Is(err, ErrOrganizeInput) {
		t.Fatalf("动漫花园不可用时不应切换来源: %v", err)
	}
}
