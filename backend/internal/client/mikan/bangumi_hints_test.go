package mikan

import (
	"fmt"
	"strings"
	"testing"
)

func TestBangumiSubjectHintsAreControlled(t *testing.T) {
	for _, raw := range []string{"https://bgm.tv/subject/278826", "http://bangumi.tv/subject/278826", "https://chii.in/subject/278826"} {
		if got := bangumiSubjectHint(raw); got != "278826" {
			t.Fatalf("missing hint: %s => %s", raw, got)
		}
	}
	for _, raw := range []string{"//bgm.tv/subject/278826", "https://bgm.tv.evil/subject/278826", "https://bgm.tv@evil/subject/278826", "https://bgm.tv:443/subject/278826", "https://bgm.tv/subject/0278826", "https://bgm.tv/subject/278826?token=secret", "https://bgm.tv/subject/278826#x", "https://bgm.tv/subject/%32%37%38%38%32%36", "file://bgm.tv/subject/278826"} {
		if got := bangumiSubjectHint(raw); got != "" {
			t.Fatalf("uncontrolled hint accepted: %s", raw)
		}
	}
}

func TestSourceEntryKeepsSubjectHintsWithoutMerging(t *testing.T) {
	links := `<a href="http://bgm.tv/subject/278826">Bangumi</a><a href="https://bgm.tv/subject/278826">duplicate</a><a href="https://bangumi.tv/subject/305041">another chapter</a><a href="https://evil.test/subject/111">ignored</a>`
	body := `<html><input id="header-search" name="searchstr"><p class="bangumi-title">相同名称</p>` + links + `<a class="subgroup-name" data-anchor="#45">实际发布组</a><table class="episode-table"><tbody></tbody></table></html>`
	in := sourceRequest("entry", 0)
	in.Source.GroupID = ""
	page, err := parseSourceHTML([]byte(body), in)
	if err != nil || page.Entry == nil || len(page.Entry.BangumiIDs) != 2 || page.Entry.BangumiIDs[0] != "278826" || page.Entry.BangumiIDs[1] != "305041" || len(page.Releases) != 0 {
		t.Fatalf("unexpected entry hints: %+v %v", page.Entry, err)
	}
	var many strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&many, `<a href="https://bgm.tv/subject/%d">hint</a>`, i)
	}
	body = strings.Replace(body, links, many.String(), 1)
	page, err = parseSourceHTML([]byte(body), in)
	if err != nil || len(page.Entry.BangumiIDs) != 32 {
		t.Fatalf("unbounded hints: %+v %v", page.Entry, err)
	}
}
