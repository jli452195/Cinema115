package subshare

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const catalogue = `<DocumentElement><subs><time>2007-10-04T00:00:00+08:00</time><name_chs>团子大家族</name_chs><name_cht>糰子大家族</name_cht><name_jp>クラナド</name_jp><name_en>CLANNAD</name_en><type>animation</type><source>BD</source><sub_name>测试组</sub_name><path>animation\2007\(2007.10.4)CLANNAD</path></subs></DocumentElement>`

func TestCatalogueCacheFilesAndPinnedContents(t *testing.T) {
	var indexCalls, revisionCalls, dirCalls, treeCalls, rawCalls atomic.Int32
	data := []byte("[Script Info]\nTitle: 简体字幕")
	h := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...))
	sha := hex.EncodeToString(h[:])
	revision := strings.Repeat("a", 40)
	token := "github_pat_" + strings.Repeat("t", 50)
	var wrong atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantAuth := ""
		if strings.HasPrefix(r.URL.Path, "/api/") {
			wantAuth = "Bearer " + token
		}
		if r.Header.Get("Authorization") != wantAuth {
			t.Error("GitHub API 与原始文件请求未使用各自的认证方式")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "db.xml"):
			indexCalls.Add(1)
			second := strings.ReplaceAll(catalogue, "CLANNAD", "AIR")
			fmt.Fprint(w, strings.Replace(catalogue, "</DocumentElement>", strings.TrimPrefix(second, "<DocumentElement>"), 1))
		case strings.HasSuffix(r.URL.Path, "/git/ref/heads/master"):
			revisionCalls.Add(1)
			fmt.Fprintf(w, `{"object":{"sha":%q}}`, revision)
		case strings.Contains(r.URL.Path, "/contents/"):
			dirCalls.Add(1)
			if r.URL.Query().Get("ref") != revision {
				t.Error("directory was not pinned")
			}
			dir := strings.SplitN(r.URL.Path, "/contents/", 2)[1]
			_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": dir + "/Show [01].chs.ass", "name": "Show [01].chs.ass", "sha": sha, "size": len(data)}, {"type": "dir", "path": dir + "/繁体", "name": "繁体", "sha": strings.Repeat("b", 40)}})
		case strings.Contains(r.URL.Path, "/git/trees/"):
			treeCalls.Add(1)
			fmt.Fprintf(w, `{"truncated":false,"tree":[{"path":"Show [01].cht.ass","type":"blob","mode":"100644","sha":%q,"size":%d}]}`, sha, len(data))
		case strings.HasPrefix(r.URL.Path, "/raw/"+revision+"/"):
			rawCalls.Add(1)
			if wrong.Load() {
				fmt.Fprint(w, strings.Repeat("x", len(data)))
			} else {
				_, _ = w.Write(data)
			}
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(t.TempDir())
	c.API = server.URL + "/api"
	c.Raw = server.URL + "/raw/master"
	c.SetToken(token)
	ctx := context.Background()
	versions, err := c.Search(ctx, []string{"CLANNAD"})
	if err != nil || len(versions) != 1 {
		t.Fatal(versions, err)
	}
	for _, q := range []string{"糰子 大家族", "クラナド"} {
		got, e := c.Search(ctx, []string{q})
		if e != nil || len(got) != 2 {
			t.Fatal(q, got, e)
		}
	}
	files, err := c.Files(ctx, versions[0].ID)
	if err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
	if _, err = c.Files(ctx, versions[0].ID); err != nil {
		t.Fatal(err)
	}
	if indexCalls.Load() != 1 || dirCalls.Load() != 1 {
		t.Fatal("cache missed", indexCalls.Load(), dirCalls.Load())
	}
	content, err := c.Read(ctx, files[0])
	if err != nil || string(content) != string(data) {
		t.Fatal(string(content), err)
	}
	wrong.Store(true)
	if _, err = c.Read(ctx, files[0]); err == nil {
		t.Fatal("changed contents accepted")
	}
	// 即使缓存已存放很久，重启和内存清单淘汰后也不再查询上游。
	old := time.Now().Add(-365 * 24 * time.Hour)
	for _, name := range []string{"index.xml", "revision", versions[0].ID + ".json"} {
		if err := os.Chtimes(filepath.Join(c.CacheDir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	restarted := New(c.CacheDir)
	restarted.API, restarted.Raw = c.API, c.Raw
	restarted.SetToken(token)
	if got, e := restarted.Search(ctx, []string{"团子"}); e != nil || len(got) != 2 || indexCalls.Load() != 1 {
		t.Fatal("disk cache was not reused", got, e)
	}
	for i := 0; i < 2; i++ {
		got, e := restarted.Files(ctx, versions[0].ID)
		if e != nil || len(got) != 2 || got[0] != files[0] || revisionCalls.Load() != 1 || dirCalls.Load() != 1 || treeCalls.Load() != 1 {
			t.Fatal("file cache was not reused", got, e)
		}
		got[0].Path = "modified"
		restarted.mu.Lock()
		clear(restarted.lists)
		restarted.mu.Unlock()
	}
	wrong.Store(false)
	if content, err := restarted.Read(ctx, files[0]); err != nil || string(content) != string(data) || rawCalls.Load() != 3 {
		t.Fatal("subtitle contents must still be downloaded", err)
	}
	// 未缓存的版本仍可首次查询，并复用已固定的仓库提交号。
	other, err := restarted.Search(ctx, []string{"AIR"})
	if err != nil || len(other) != 1 {
		t.Fatal(other, err)
	}
	if got, e := restarted.Files(ctx, other[0].ID); e != nil || len(got) != 2 || dirCalls.Load() != 2 || revisionCalls.Load() != 1 {
		t.Fatal("uncached version was not fetched", got, e)
	}
}

func TestIncompleteListingIsNotCached(t *testing.T) {
	var dirCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "db.xml"):
			fmt.Fprint(w, catalogue)
		case strings.HasSuffix(r.URL.Path, "/git/ref/heads/master"):
			fmt.Fprintf(w, `{"object":{"sha":%q}}`, strings.Repeat("a", 40))
		case strings.Contains(r.URL.Path, "/contents/"):
			dirCalls.Add(1)
			dir := strings.SplitN(r.URL.Path, "/contents/", 2)[1]
			_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "dir", "path": dir + "/字幕", "sha": strings.Repeat("b", 40)}})
		case strings.Contains(r.URL.Path, "/git/trees/"):
			fmt.Fprintf(w, `{"truncated":%t,"tree":[{"path":"01.ass","type":"blob","mode":"100644","sha":%q,"size":10}]}`, dirCalls.Load() == 1, strings.Repeat("c", 40))
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := New(t.TempDir())
	c.API, c.Raw = server.URL, server.URL
	ctx := context.Background()
	versions, err := c.Search(ctx, []string{"CLANNAD"})
	if err != nil || len(versions) != 1 {
		t.Fatal(versions, err)
	}
	id := versions[0].ID
	if _, err = c.Files(ctx, id); err == nil {
		t.Fatal("truncated listing accepted")
	}
	if _, err = os.Stat(filepath.Join(c.CacheDir, id+".json")); !os.IsNotExist(err) {
		t.Fatal("incomplete listing persisted", err)
	}
	if files, err := c.Files(ctx, id); err != nil || len(files) != 1 || dirCalls.Load() != 2 {
		t.Fatal("failed listing was not retried", files, err)
	}
	// 损坏或越界的磁盘清单不能直接使用，应重新获取完整清单。
	for _, body := range []string{`{`, `[]`, `[{"revision":"bad"}]`, fmt.Sprintf(`[{"revision":%q,"sha":%q,"path":"subs_list/animation/../outside/01.ass","name":"01.ass","size":10}]`, strings.Repeat("a", 40), strings.Repeat("c", 40))} {
		if err := os.WriteFile(filepath.Join(c.CacheDir, id+".json"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		restarted := New(c.CacheDir)
		restarted.API, restarted.Raw = c.API, c.Raw
		before := dirCalls.Load()
		if files, err := restarted.Files(ctx, id); err != nil || len(files) != 1 || dirCalls.Load() != before+1 {
			t.Fatal("invalid disk listing accepted", files, err)
		}
	}
}

func TestIndexRejectsTraversalAndNonAnimation(t *testing.T) {
	for _, body := range []string{strings.Replace(catalogue, "animation\\2007", "animation\\..\\2007", 1), strings.Replace(catalogue, "<type>animation</type>", "<type>movie</type>", 1), `<html>error</html>`} {
		if _, err := parseIndex([]byte(body)); err == nil {
			t.Fatal("invalid index accepted")
		}
	}
	c := New("")
	if _, err := c.Search(context.Background(), []string{"---"}); err == nil {
		t.Fatal("empty normalized query accepted")
	}
}

// Explicit read-only upstream smoke check; regular CI is entirely local.
func TestLiveSubshareCatalogue(t *testing.T) {
	if os.Getenv("CINEMA_TEST_SUBSHARE") != "1" {
		t.Skip("opt-in upstream check")
	}
	c := New(t.TempDir())
	v, err := c.Search(context.Background(), []string{"CLANNAD"})
	if err != nil || len(v) == 0 {
		t.Fatal(err)
	}
	files, err := c.Files(context.Background(), v[0].ID)
	if err != nil || len(files) == 0 {
		t.Fatal(err)
	}
	if _, err = c.Read(context.Background(), files[0]); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d matching versions; first version %d files; immutable subtitle downloaded and verified", len(v), len(files))
}
