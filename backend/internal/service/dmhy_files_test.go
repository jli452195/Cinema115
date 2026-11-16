package service

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"html"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/dmhy"
	"strm115/backend/internal/utils/torrentmeta"
)

func dmhyServiceDetail(id, hash, torrentURL string) []byte {
	return []byte(`<html><body><div class="topic-title"><li class="share"><a href="/js/share.html?url=http://share.dmhy.org/topics/view/` + id + `_.html">share</a></li></div><div id="resource-tabs"><a href="` + html.EscapeString("magnet:?xt=urn:btih:"+hash) + `">Magnet</a><a href="` + html.EscapeString(torrentURL) + `">種子</a></div></body></html>`)
}

func dmhyFilesFixture(t *testing.T) (*DmhyService, *dmhyServiceTransport, DownloadResourceInput, []byte) {
	t.Helper()
	body, _ := mikanOrganizeTorrent(t)
	meta, err := torrentmeta.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	in := DownloadResourceInput{Source: "dmhy", ProviderID: "123", Magnet: "magnet:?xt=urn:btih:" + meta.InfoHash}
	f := &dmhyServiceTransport{fetch: func(_ context.Context, endpoint string) ([]byte, error) {
		id := strings.TrimSuffix(strings.TrimPrefix(endpoint, "/topics/view/"), "_.html")
		return dmhyServiceDetail(id, meta.InfoHash, "https://dl.dmhy.org/2026/11/16/"+meta.InfoHash+".torrent"), nil
	}}
	return newDmhyTestService(f), f, in, body
}

func TestDmhyFilesAreLazyVerifyTorrentAndIsolatePostCache(t *testing.T) {
	s, detail, in, body := dmhyFilesFixture(t)
	hash, _ := btresource.MagnetHash(in.Magnet)
	reads := 0
	s.TorrentReader = animeClaimTorrentFunc(func(_ context.Context, provider, post, endpoint string) ([]byte, error) {
		reads++
		if provider != "dmhy" || post != hash || endpoint != "https://dl.dmhy.org/2026/11/16/"+hash+".torrent" {
			t.Error("读取了错误的原站种子", provider, post, endpoint)
		}
		return body, nil
	})
	if detail.calls() != 0 || reads != 0 {
		t.Fatal("初始化触发读取")
	}
	files, err := s.Files(context.Background(), in)
	if err != nil || len(files.Files) != 2 || len(files.Subtitles) != 1 || reads != 1 || detail.calls() != 1 {
		t.Fatal("真实种子文件清单未正确读取", files, err, reads, detail.calls())
	}
	if files.Files[0].ID != "show/[Group] Show [01].mkv" || files.Files[0].Size != 1200 || files.Subtitles[0].VideoID != files.Files[0].ID {
		t.Fatal("真实路径、大小或字幕关联丢失", files)
	}
	data, _ := hex.DecodeString(hash)
	in.Magnet = "magnet:?xt=urn:btih:" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(data)
	if _, err := s.Files(context.Background(), in); err != nil || reads != 1 || detail.calls() != 1 {
		t.Fatal("base32/hex 身份未复用已验证缓存", err, reads, detail.calls())
	}
	in.ProviderID = "124"
	if _, err := s.Files(context.Background(), in); err != nil || reads != 1 || detail.calls() != 2 {
		t.Fatal("不同原帖错误共享详情或重复读取同一真实种子", err, reads, detail.calls())
	}
	in.Magnet = "magnet:?xt=urn:btih:" + strings.Repeat("b", 40)
	if _, err := s.Files(context.Background(), in); !errors.Is(err, ErrOrganizeInput) || reads != 1 || detail.calls() != 2 {
		t.Fatal("缓存命中跳过调用方磁力核验", err, reads, detail.calls())
	}
}

func TestDmhyResolveMagnetSharesDetailCacheWithoutReadingTorrent(t *testing.T) {
	s, detail, in, body := dmhyFilesFixture(t)
	reads := 0
	s.TorrentReader = animeClaimTorrentFunc(func(_ context.Context, _, _, _ string) ([]byte, error) {
		reads++
		return body, nil
	})
	for _, id := range []string{"", "0", "../123", "https://evil.test/post"} {
		if _, err := s.ResolveMagnet(context.Background(), id); !errors.Is(err, dmhy.ErrInput) || detail.calls() != 0 {
			t.Fatal("无效编号读取了原帖", id, err, detail.calls())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ResolveMagnet(ctx, in.ProviderID); !errors.Is(err, context.Canceled) || detail.calls() != 0 {
		t.Fatal("取消请求仍读取原帖", err, detail.calls())
	}
	first, err := s.ResolveMagnet(context.Background(), in.ProviderID)
	if err != nil || first.ID != in.ProviderID || first.Magnet != in.Magnet || first.Cached || first.FetchedAt.IsZero() || first.ExpiresAt.Sub(first.FetchedAt) != 30*time.Minute || detail.calls() != 1 || reads != 0 {
		t.Fatal("补读磁力读取了种子或返回错误", first, err, detail.calls(), reads)
	}
	again, err := s.ResolveMagnet(context.Background(), in.ProviderID)
	if err != nil || !again.Cached || !again.FetchedAt.Equal(first.FetchedAt) || detail.calls() != 1 || reads != 0 {
		t.Fatal("补读磁力未复用详情缓存", again, err, detail.calls(), reads)
	}
	if files, err := s.Files(context.Background(), in); err != nil || len(files.Files) != 2 || detail.calls() != 1 || reads != 1 {
		t.Fatal("整理未共用已核验原帖", files, err, detail.calls(), reads)
	}
}

func TestDmhyResolveMagnetWithoutTorrentDoesNotInventFiles(t *testing.T) {
	s, detail, in, _ := dmhyFilesFixture(t)
	hash, _ := btresource.MagnetHash(in.Magnet)
	detail.fetch = func(_ context.Context, _ string) ([]byte, error) {
		return dmhyServiceDetail(in.ProviderID, hash, ""), nil
	}
	s.TorrentReader = animeClaimTorrentFunc(func(_ context.Context, _, _, _ string) ([]byte, error) {
		t.Fatal("原帖没有种子地址时不应读取种子")
		return nil, nil
	})
	if result, err := s.ResolveMagnet(context.Background(), in.ProviderID); err != nil || result.Magnet != in.Magnet || detail.calls() != 1 {
		t.Fatal("合法磁力错误依赖种子地址", result, err)
	}
	if _, err := s.Files(context.Background(), in); !errors.Is(err, ErrOrganizeInput) || !strings.Contains(err.Error(), "普通云下载") || detail.calls() != 1 {
		t.Fatal("缺少真实种子时不应使用网页清单或标题整理", err, detail.calls())
	}
}

func TestDmhyResolveMagnetConcurrentCallsAndCancelledRead(t *testing.T) {
	s, detail, in, _ := dmhyFilesFixture(t)
	hash, _ := btresource.MagnetHash(in.Magnet)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	detail.fetch = func(ctx context.Context, _ string) ([]byte, error) {
		once.Do(func() { close(started) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return dmhyServiceDetail(in.ProviderID, hash, ""), nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := s.ResolveMagnet(ctx, in.ProviderID); finished <- err }()
	<-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("读取原帖时取消未传递", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if result, err := s.ResolveMagnet(context.Background(), in.ProviderID); err != nil || result.Magnet != in.Magnet {
				t.Error("取消后的同帖读取失败", result, err)
			}
		}()
	}
	close(release)
	wg.Wait()
	if detail.calls() != 2 {
		t.Fatal("同帖补读未合并或取消污染缓存", detail.calls())
	}
}

func TestDmhyFilesRejectUntrustedSourcesBeforeReads(t *testing.T) {
	s, detail, source, _ := dmhyFilesFixture(t)
	for _, change := range []func(*DownloadResourceInput){
		func(in *DownloadResourceInput) { in.Source = "garden" },
		func(in *DownloadResourceInput) { in.Provider = "dmhy" },
		func(in *DownloadResourceInput) { in.TorrentURL = "https://dl.dmhy.org/example.torrent" },
		func(in *DownloadResourceInput) { in.ProviderID = "https://evil.test" },
		func(in *DownloadResourceInput) { in.Magnet = "magnet:?xt=broken" },
	} {
		in := source
		change(&in)
		if _, err := s.Files(context.Background(), in); !errors.Is(err, ErrOrganizeInput) || detail.calls() != 0 {
			t.Fatal("不可信来源访问了原站", in, err, detail.calls())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Files(ctx, source); !errors.Is(err, context.Canceled) || detail.calls() != 0 {
		t.Fatal("取消请求仍访问原站", err)
	}
}

func TestDmhyFilesRejectWrongDetailAndPoisonedTorrent(t *testing.T) {
	for _, mode := range []string{"post ID", "detail hash", "torrent URL", "torrent bytes", "network"} {
		t.Run(mode, func(t *testing.T) {
			s, detail, in, body := dmhyFilesFixture(t)
			hash, _ := btresource.MagnetHash(in.Magnet)
			wrongBody := []byte(strings.Replace(string(body), "[Group] Show [01].mkv", "[Group] Show [09].mkv", 1))
			wrongMeta, err := torrentmeta.Parse(wrongBody)
			if err != nil || wrongMeta.InfoHash == hash {
				t.Fatal("错误身份测试种子无效", err)
			}
			reads := 0
			s.TorrentReader = animeClaimTorrentFunc(func(_ context.Context, _, _, _ string) ([]byte, error) {
				reads++
				if mode == "torrent bytes" {
					return wrongBody, nil
				}
				return body, nil
			})
			detail.fetch = func(_ context.Context, _ string) ([]byte, error) {
				switch mode {
				case "post ID":
					return dmhyServiceDetail("999", hash, "https://dl.dmhy.org/2026/11/16/"+hash+".torrent"), nil
				case "detail hash":
					other := strings.Repeat("b", 40)
					return dmhyServiceDetail(in.ProviderID, other, "https://dl.dmhy.org/2026/11/16/"+other+".torrent"), nil
				case "torrent URL":
					return dmhyServiceDetail(in.ProviderID, hash, "https://evil.test/"+hash+".torrent"), nil
				case "network":
					return nil, dmhy.ErrNetwork
				default:
					return dmhyServiceDetail(in.ProviderID, hash, "https://dl.dmhy.org/2026/11/16/"+hash+".torrent"), nil
				}
			}
			if _, err := s.Files(context.Background(), in); err == nil {
				t.Fatal("错误身份或上游内容被接受")
			}
			if mode != "torrent bytes" && reads != 0 {
				t.Fatal("详情失败后仍读取种子", reads)
			}
			if mode == "torrent bytes" {
				s.TorrentReader = animeClaimTorrentFunc(func(_ context.Context, _, _, _ string) ([]byte, error) { reads++; return body, nil })
				if _, err := s.Files(context.Background(), in); err != nil || reads != 2 {
					t.Fatal("错误种子被缓存，阻止正确重试", err, reads)
				}
			}
		})
	}
}
