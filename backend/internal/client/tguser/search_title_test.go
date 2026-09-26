package tguser

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLimitBotSearchTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{name: "short", title: "庆余年", want: "庆余年"},
		{name: "exactly 18", title: "一二三四五六七八九十一二三四五六七八", want: "一二三四五六七八九十一二三四五六七八"},
		{name: "long Chinese", title: "一二三四五六七八九十一二三四五六七八九十", want: "一二三四五六七八九十一二三四五六七八"},
		{name: "mixed Unicode", title: "ABC电影🎬一二三四五六七八九十一二三四五", want: "ABC电影🎬一二三四五六七八九十一二"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := limitBotSearchTitle(tt.title); got != tt.want {
				t.Fatalf("limitBotSearchTitle(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestSearchUsesLimitedTitleForCache(t *testing.T) {
	c := testClient(t)
	c.config.Bot = testBotUsername
	query := "一二三四五六七八九十一二三四五六七八"
	c.cacheResult("search:"+query, SearchResult{Text: "cached result", Empty: true}, time.Now())

	result, err := c.Search(context.Background(), query+strings.Repeat("九", 100), false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cached || result.Text != "cached result" {
		t.Fatalf("long title did not use the limited Bot query cache: %+v", result)
	}
}
