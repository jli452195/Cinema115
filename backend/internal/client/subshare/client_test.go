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
	"strings"
	"sync/atomic"
	"testing"
)

const catalogue = `<DocumentElement><subs><time>2007-10-04T00:00:00+08:00</time><name_chs>团子大家族</name_chs><name_cht>糰子大家族</name_cht><name_jp>クラナド</name_jp><name_en>CLANNAD</name_en><type>animation</type><source>BD</source><sub_name>测试组</sub_name><path>animation\2007\(2007.10.4)CLANNAD</path></subs></DocumentElement>`

func TestCatalogueCacheFilesAndPinnedContents(t *testing.T) {
	var indexCalls, dirCalls atomic.Int32
	data := []byte("[Script Info]\nTitle: 简体字幕")
	h := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...))
	sha := hex.EncodeToString(h[:])
	revision := strings.Repeat("a", 40)
	var wrong bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "db.xml"):
			indexCalls.Add(1)
			fmt.Fprint(w, catalogue)
		case strings.HasSuffix(r.URL.Path, "/git/ref/heads/master"):
			fmt.Fprintf(w, `{"object":{"sha":%q}}`, revision)
		case strings.Contains(r.URL.Path, "/contents/"):
			dirCalls.Add(1)
			if r.URL.Query().Get("ref") != revision {
				t.Error("directory was not pinned")
			}
			dir := strings.SplitN(r.URL.Path, "/contents/", 2)[1]
			_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": dir + "/Show [01].chs.ass", "name": "Show [01].chs.ass", "sha": sha, "size": len(data)}, {"type": "dir", "path": dir + "/繁体", "name": "繁体", "sha": strings.Repeat("b", 40)}})
		case strings.Contains(r.URL.Path, "/git/trees/"):
			fmt.Fprintf(w, `{"truncated":false,"tree":[{"path":"Show [01].cht.ass","type":"blob","mode":"100644","sha":%q,"size":%d}]}`, sha, len(data))
		case strings.HasPrefix(r.URL.Path, "/raw/"+revision+"/"):
			if wrong {
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
	ctx := context.Background()
	versions, err := c.Search(ctx, []string{"CLANNAD"})
	if err != nil || len(versions) != 1 {
		t.Fatal(versions, err)
	}
	for _, q := range []string{"糰子 大家族", "クラナド"} {
		got, e := c.Search(ctx, []string{q})
		if e != nil || len(got) != 1 {
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
	wrong = true
	if _, err = c.Read(ctx, files[0]); err == nil {
		t.Fatal("changed contents accepted")
	}
	restarted := New(c.CacheDir)
	restarted.Raw = server.URL + "/missing"
	if got, e := restarted.Search(ctx, []string{"团子"}); e != nil || len(got) != 1 || indexCalls.Load() != 1 {
		t.Fatal("disk cache was not reused", got, e)
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
