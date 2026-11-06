package service

import (
	"path"
	"reflect"
	"slices"
	"testing"

	"strm115/backend/internal/client/p115"
)

func mediaSubtitleTestFile(id, parent, name, mediaType string) OrganizeFile {
	return OrganizeFile{ShareFile: p115.ShareFile{ID: id, Name: name}, ParentID: parent, SourcePath: path.Join(parent, name), MediaType: mediaType}
}

func TestMediaOrganizeSubtitlesAcrossDirectories(t *testing.T) {
	for _, tc := range []struct {
		name, subtitle, parent, owner, tail string
		videos                              []OrganizeFile
	}{
		{"arbitrary nested directory", "xxx.xxx.ass", "custom/nested", "video", "", nil},
		{"language suffix in sibling directory", "xxx.xxx.chs.ass", "other", "video", ".chs", nil},
		{"case insensitive", "XXX.XXX.CHS.ASS", "other", "video", ".CHS", nil},
		{"unrelated filename", "other.xxx.chs.ass", "other", "", "", nil},
		{"prefix without separator", "xxx.xxx2.ass", "other", "", "", nil},
		{"full stem with language-like ending", "xxx.xxx.chs.ass", "other", "longer", "", []OrganizeFile{mediaSubtitleTestFile("longer", "versions", "xxx.xxx.chs.mkv", "video")}},
		{"ambiguous cross directory videos", "xxx.xxx.chs.ass", "other", "", ".chs", []OrganizeFile{mediaSubtitleTestFile("duplicate", "versions", "xxx.xxx.mp4", "video")}},
		{"same directory wins", "xxx.xxx.chs.ass", "videos", "video", ".chs", []OrganizeFile{mediaSubtitleTestFile("longer", "versions", "xxx.xxx.chs.mkv", "video")}},
		{"same directory ambiguity is retained", "xxx.xxx.chs.ass", "videos", "", ".chs", []OrganizeFile{mediaSubtitleTestFile("duplicate", "videos", "xxx.xxx.mp4", "video"), mediaSubtitleTestFile("longer", "versions", "xxx.xxx.chs.mkv", "video")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := []OrganizeFile{mediaSubtitleTestFile("video", "videos", "xxx.xxx.mkv", "video")}
			files = append(files, tc.videos...)
			files = append(files, mediaSubtitleTestFile("sub", tc.parent, tc.subtitle, "subtitle"))
			before := slices.Clone(files)
			got := mediaOrganizeSubtitles(files)
			if len(got) != 1 || got[0].VideoID != tc.owner || got[0].Tail != tc.tail {
				t.Fatalf("字幕归属或后缀错误：%+v，期望 %q %q", got, tc.owner, tc.tail)
			}
			if !reflect.DeepEqual(files, before) || !reflect.DeepEqual(got[0].File, before[len(before)-1]) {
				t.Fatal("匹配不能改写真实父目录或来源路径")
			}
			if tc.parent != "videos" {
				ordinary := organizeSubtitles(files)
				if ordinary[0].VideoID != "" {
					t.Fatal("其它整理入口不能扩大为跨目录匹配", ordinary)
				}
			}
		})
	}
}

func TestMediaOrganizeSubtitlesKeepsWeakMatchingInSameDirectory(t *testing.T) {
	video := mediaSubtitleTestFile("video", "videos", "[Show][02][1080P].mkv", "video")
	for _, parent := range []string{"videos", "imported/subtitles"} {
		files := []OrganizeFile{video, mediaSubtitleTestFile("sub", parent, "[Show][02][CHT].ass", "subtitle")}
		got := mediaOrganizeSubtitles(files)
		if parent == "videos" {
			if got[0].VideoID != "video" || got[0].Tail != ".zh-Hant" {
				t.Fatal("同目录现有匹配规则应继续生效", got)
			}
		} else if got[0].VideoID != "" {
			t.Fatal("跨目录不能按片名、集号或规格归一化猜测字幕归属", got)
		}
	}
	files := []OrganizeFile{
		video,
		mediaSubtitleTestFile("cross", "imported", "[Show][02][CHT].mkv", "video"),
		mediaSubtitleTestFile("sub", "videos", "[Show][02][CHT].ass", "subtitle"),
	}
	if got := mediaOrganizeSubtitles(files); got[0].VideoID != video.ID || got[0].Tail != ".zh-Hant" {
		t.Fatal("已有同目录归属应优先于跨目录同名视频", got)
	}
}
