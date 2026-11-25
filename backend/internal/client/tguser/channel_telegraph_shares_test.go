package tguser

import (
	"slices"
	"testing"
)

func telegraphShareAnchor(href, label string) map[string]any {
	anchor := telegraphTestNode("a", label)
	anchor["attrs"] = map[string]string{"href": href}
	return anchor
}

func TestTelegraphShareGroupsKeepVersionsAndED2KSeparate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		title   string
		page    string
		files   []any
		version string
		quality string
		wantRes string
	}{
		{
			name: "混合清单", title: "凡人修仙传", page: "凡人修仙传 (2020)",
			files:   []any{telegraphTestNode("pre", telegraphTestNode("code", channelED2K))},
			version: "凡人修仙传 1-178", quality: "4K / AVC", wantRes: "2160p",
		},
		{
			name: "纯分享清单", title: "葬送的芙莉莲", page: "葬送的芙莉莲",
			version: "葬送的芙莉莲 第二季 BDRIP 1080p HEVC FLAC", quality: "1080p / HEVC", wantRes: "1080p",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := append(tc.files,
				telegraphTestNode("hr"), telegraphTestNode("p", telegraphTestNode("strong", tc.version)),
				telegraphTestNode("p", "画质规格："+tc.quality), telegraphTestNode("p", telegraphShareAnchor("https://115cdn.com/s/first?password=ex12&", "查看链接")),
				telegraphTestNode("hr"), telegraphTestNode("p", telegraphTestNode("strong", "其他版本")),
				telegraphTestNode("p", telegraphShareAnchor("https://anxia.com/s/second?password=ex34", "查看链接")),
			)
			result, err := parseTelegraph(telegraphFixture(tc.page, content), ChannelSearchTarget{Title: tc.title, Kind: "tv", TMDBID: "42"})
			if err != nil || len(result.ShareGroups) != 2 || len(result.Groups) != len(tc.files) {
				t.Fatalf("资源类型或版本分组不符: %+v, %v", result, err)
			}
			first, second := result.ShareGroups[0], result.ShareGroups[1]
			if first.Title != tc.version || second.Title != "其他版本" || first.ID == "" || first.ID == second.ID || len(first.Links) != 1 || len(second.Links) != 1 {
				t.Fatalf("页面版本标题或链接没有分开: %+v", result.ShareGroups)
			}
			if first.Links[0].Type != "115" || first.Links[0].URL != "https://115cdn.com/s/first?password=ex12" || second.Links[0].URL != "https://115cdn.com/s/second?password=ex34" {
				t.Fatalf("链接或提取码规范化失败: %+v", result.ShareGroups)
			}
			if metadata := first.Links[0].Metadata; metadata == nil || metadata.Resolution != tc.wantRes {
				t.Fatalf("第一组没有继承画质规格: %+v", metadata)
			}
			if metadata := second.Links[0].Metadata; metadata != nil && metadata.Resolution != "" {
				t.Fatalf("第二组串用了第一组的画质规格: %+v", metadata)
			}
		})
	}
}

func TestTelegraphSharePasswordsAreLocalAndPreferExplicitLinks(t *testing.T) {
	content := []any{
		telegraphTestNode("h3", "1080p", telegraphShareAnchor("https://115.com/s/heading", "查看链接")),
		telegraphTestNode("p", telegraphShareAnchor("https://115.com/s/anchor", "https://115.com/s/fake"), telegraphTestNode("br"), "提取码：ab12"),
		telegraphTestNode("p", "https://115.com/s/plain，提取码：cd34"),
		telegraphTestNode("p", "https://115cdn.com/s/plain"),
		telegraphTestNode("p", "https://115.com/s/explicit?password=ef56 密码：gg78"),
		telegraphTestNode("p", "https://anxia.com/s/explicit?password=gh78"),
		telegraphTestNode("p", "https://115cdn.com/s/explicit"),
		telegraphTestNode("p", "https://115.com/s/left，https://115.com/s/right 提取码：uv12"),
		telegraphTestNode("p", telegraphShareAnchor("https://115.com/s/multiCloud", "115"), telegraphShareAnchor("https://pan.example/s/other", "其他网盘"), "提取码：qr56"),
		telegraphTestNode("p", "https://115.com/s/ambiguous 提取码：ij90 密码：kl12"),
		telegraphTestNode("p", "https://115.com/s/long 密码：mn123"),
		telegraphTestNode("p", "https://115.com/s/separate"), telegraphTestNode("p", "提取码：op34"),
		telegraphTestNode("p", "https://115.com.fake/s/ignored https://wrong.example/?link=https://115.com/s/ignored"),
	}
	result, err := parseTelegraph(telegraphFixture("作品", content), ChannelSearchTarget{Title: "作品", Kind: "tv", TMDBID: "42"})
	if err != nil || len(result.ShareGroups) != 1 || len(result.Groups) != 0 {
		t.Fatalf("h3 和纯分享内容解析失败: %+v, %v", result, err)
	}
	var urls []string
	for _, link := range result.ShareGroups[0].Links {
		urls = append(urls, link.URL)
	}
	want := []string{
		"https://115cdn.com/s/heading", "https://115cdn.com/s/anchor?password=ab12", "https://115cdn.com/s/plain?password=cd34",
		"https://115cdn.com/s/explicit?password=ef56", "https://115cdn.com/s/explicit?password=gh78",
		"https://115cdn.com/s/left", "https://115cdn.com/s/right", "https://115cdn.com/s/multiCloud", "https://115cdn.com/s/ambiguous", "https://115cdn.com/s/long", "https://115cdn.com/s/separate",
	}
	if !slices.Equal(urls, want) {
		t.Fatalf("连续链接、去重或密码范围不符: %q", urls)
	}
}

func TestTelegraphNestedParagraphsKeepFileMetadataAndSeparateSharePasswords(t *testing.T) {
	file := "ed2k://|file|Series.mkv|123|0123456789abcdef0123456789abcdef|/"
	content := []any{
		telegraphTestNode("h3", "作品"),
		telegraphTestNode("ul", telegraphTestNode("li",
			telegraphTestNode("p", "规格：1080p"), telegraphTestNode("p", "字幕：简体中文"),
			telegraphTestNode("p", telegraphTestNode("strong", file[:32]), file[32:]),
			telegraphTestNode("p", "https://115.com/s/first 提取码：ab12"),
			telegraphTestNode("p", "https://115.com/s/second 提取码：cd34"),
		)),
	}
	result, err := parseTelegraph(telegraphFixture("作品", content), ChannelSearchTarget{Title: "作品", Kind: "tv", TMDBID: "42"})
	if err != nil || len(result.Groups) != 1 || len(result.Groups[0].Links) != 1 || len(result.ShareGroups) != 1 || len(result.ShareGroups[0].Links) != 2 {
		t.Fatalf("嵌套段落未解析: %+v, %v", result, err)
	}
	metadata := result.Groups[0].Links[0].Metadata
	if metadata == nil || metadata.Resolution != "1080p" || metadata.Subtitles != "简体中文" {
		t.Fatalf("嵌套段落的文件规格丢失: %+v", metadata)
	}
	links := result.ShareGroups[0].Links
	if links[0].URL != "https://115cdn.com/s/first?password=ab12" || links[1].URL != "https://115cdn.com/s/second?password=cd34" {
		t.Fatalf("嵌套段落内的提取码绑定错误: %+v", links)
	}
}
