package mikan

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func catalogBlock(raw int, id string) string {
	return fmt.Sprintf(`<div class="sk-bangumi" data-dayofweek="%d"><ul><li><span data-src="/images/Bangumi/202610/poster.jpg?width=400&amp;format=webp"></span><div class="an-info"><div class="date-text">2026/10/16 更新</div><a class="an-text" title="完整&amp;番名" href="/Home/Bangumi/%s">短名</a></div></li></ul></div>`, raw, id)
}

func TestCatalogCalendarKeepsSiteSemantics(t *testing.T) {
	input := catalogBlock(0, "4063") + catalogBlock(1, "4058") + catalogBlock(7, "5000") + catalogBlock(8, "5001")
	out, err := ParseCatalog(strings.NewReader(input), "2026-10")
	if err != nil || len(out.Days) != 4 || out.Season != "2026-10" {
		t.Fatal(out, err)
	}
	for i, expected := range []int{7, 1, 8, 9} {
		day := out.Days[i]
		if day.Day != expected || len(day.Items) != 1 {
			t.Fatal(day)
		}
		item := day.Items[0]
		if item.Title != "完整&番名" || item.UpdatedDate != "2026-10-16" || item.CoverURL != "/api/mikan/image/202610/poster.jpg" {
			t.Fatal(item)
		}
	}
}

func TestCatalogRejectsChangedOrForeignPage(t *testing.T) {
	for _, input := range []string{"<html>captcha</html>", catalogBlock(10, "1"), catalogBlock(1, "1") + catalogBlock(1, "2"), strings.ReplaceAll(catalogBlock(1, "1"), "/Home/Bangumi/1", "https://evil.test/Home/Bangumi/1")} {
		if _, err := ParseCatalog(strings.NewReader(input), "2026-10"); !errors.Is(err, ErrParse) {
			t.Fatalf("expected invalid page: %v", err)
		}
	}
	for _, season := range []string{"", "2026-02", "1999-10", "2026-10?url=x", "2101-01"} {
		if ValidCatalogSeason(season) {
			t.Fatal(season)
		}
	}
	out, err := ParseCatalog(strings.NewReader(`<div class="sk-bangumi" data-dayofweek="1"><ul></ul></div>`), "2026-10")
	if err != nil || len(out.Days[0].Items) != 0 {
		t.Fatal(out, err)
	}
}

func TestCatalogReadsOnlyChosenSeasonOrSearch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/Home/BangumiCoverFlowByDayOfWeek" {
			if r.URL.Query().Get("year") != "2026" || r.URL.Query().Get("seasonStr") != "秋" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, catalogBlock(1, "4063"))
			return
		}
		if r.URL.Path != "/Home/Search" || r.URL.Query().Get("searchstr") != "test" {
			t.Error(r.URL)
		}
		fmt.Fprint(w, `<li><span data-src="/images/Bangumi/202610/poster.jpg"></span><a class="an-text" title="番剧" href="/Home/Bangumi/4058">番剧</a></li>`+testPage(""))
	}))
	defer server.Close()
	client := New()
	client.BaseURL, client.HTTP = server.URL, server.Client()
	if _, err := client.Catalog(context.Background(), "bad", ""); !errors.Is(err, ErrInput) || calls != 0 {
		t.Fatal(err, calls)
	}
	if _, err := client.Catalog(context.Background(), "2026-10", ""); err != nil {
		t.Fatal(err)
	}
	out, err := client.Catalog(context.Background(), "", "test")
	if err != nil || len(out.Items) != 1 || out.Items[0].ID != "4058" || out.Items[0].CoverURL != "/api/mikan/image/202610/poster.jpg" || calls != 2 {
		t.Fatal(out, err, calls)
	}
}
