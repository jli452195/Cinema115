package mediafacts

import (
	"slices"
	"testing"
)

func TestSubtitleStemCandidates(t *testing.T) {
	matcher := NewSubtitleMatcher([]SubtitleVideo{
		{"short", "local", "Show.mkv"},
		{"long", "other", "Show.CHS.mkv"},
		{"duplicate", "other", "show.chs.mp4"},
		{"unicode", "local", "Σ.S.K.中文.mkv"},
	})
	for _, tc := range []struct {
		name, parent, tail string
		sameDirectory      bool
		owners             []string
	}{
		{"Show.CHS.forced.ass", "local", ".zh-CN.forced", true, []string{"short"}},
		{"SHOW.chs.FORCED.ass", "other", ".forced", true, []string{"long", "duplicate"}},
		{"Show.CHS.ass", "missing", "", false, []string{"long", "duplicate"}},
		{"Show.CHS.ass", "missing", "", true, nil},
		{"Show2.ass", "local", "", true, nil},
		{"ς.ſ.k.中文.CHT.ass", "local", ".zh-TW", true, []string{"unicode"}},
		{"σ.s.K.中文.ass", "other", "", false, []string{"unicode"}},
		{"Show.K组.ZH.HK.CC.ass", "local", ".zh-HK.K组.sdh", true, []string{"short"}},
		{"Show.KTXP.简日双语.特效.ass", "local", ".zh-CN.KTXP.bilingual-ja.effects", true, []string{"short"}},
		{"Show.HI.ass", "local", ".HI", true, []string{"short"}},
	} {
		t.Run(tc.name+"/"+tc.parent, func(t *testing.T) {
			owners, tail := matcher.StemCandidates(tc.parent, tc.name, tc.sameDirectory)
			if !slices.Equal(owners, tc.owners) || tail != tc.tail {
				t.Fatalf("字幕归属或原始后缀错误：%v %q，期望 %v %q", owners, tail, tc.owners, tc.tail)
			}
		})
	}
}

func TestSubtitleAniOneMatchesKnownLanguagesInSameDirectory(t *testing.T) {
	video := SubtitleVideo{"video", "local", "《Love All Play》#24 (日語原聲)【Ani-One】.mp4"}
	for _, tc := range []struct {
		name, parent, tail string
		sameDirectory      bool
		videos             []SubtitleVideo
		owners             []string
	}{
		{"《Love All Play》#24 (簡中字幕)【Ani-One】.ass", "local", ".zh-CN", true, []SubtitleVideo{video}, []string{"video"}},
		{"《Love All Play》#24 (繁中字幕)【Ani-One】.ass", "local", ".zh-TW", true, []SubtitleVideo{video}, []string{"video"}},
		{"《Love All Play》#24 (日文自動產生字幕)【Ani-One】.ass", "local", ".ja", true, []SubtitleVideo{video}, []string{"video"}},
		{"《Love All Play》#23 (簡中字幕)【Ani-One】.ass", "local", "", true, []SubtitleVideo{video}, nil},
		{"《Other Show》#24 (簡中字幕)【Ani-One】.ass", "local", "", true, []SubtitleVideo{video}, nil},
		{"《Love All Play》#24 (簡中字幕)【Ani-One】.ass", "other", "", true, []SubtitleVideo{video}, nil},
		{"《Love All Play》#24 (簡中字幕)【Ani-One】.ass", "other", "", false, []SubtitleVideo{video}, nil},
		{"《Love All Play》#24 (英文字幕)【Ani-One】.ass", "local", "", true, []SubtitleVideo{video}, nil},
		{"《Love All Play》#24 (簡中字幕)【Other Group】.ass", "local", "", true, []SubtitleVideo{video}, nil},
		{"《Love All Play》#24 (簡中字幕)【Ani-One】[Commentary].ass", "local", "", true, []SubtitleVideo{video}, nil},
		{"《Love All Play》#24 (簡中字幕)【Ani-One】.ass", "local", ".zh-CN", true, []SubtitleVideo{video, {"duplicate", "local", "《Love All Play》#24 (日語原聲)【Ani-One】.mkv"}}, []string{"video", "duplicate"}},
		{"《Love All Play》#24 (簡中字幕)【Ani-One】.ass", "local", "", true, []SubtitleVideo{video, {"exact", "local", "《Love All Play》#24 (簡中字幕)【Ani-One】.mp4"}}, []string{"exact"}},
	} {
		t.Run(tc.name+"/"+tc.parent, func(t *testing.T) {
			matcher := NewSubtitleMatcher(tc.videos)
			owners, tail := matcher.StemCandidates(tc.parent, tc.name, tc.sameDirectory)
			if !slices.Equal(owners, tc.owners) || tail != tc.tail {
				t.Fatalf("字幕归属或语言后缀错误：%v %q，期望 %v %q", owners, tail, tc.owners, tc.tail)
			}
		})
	}
}

func TestSubtitleFallbackMatchesOnlyUniqueWorkEpisodeAndSpecifications(t *testing.T) {
	for _, tc := range []struct {
		name, sub, parent, tail string
		videos                  []SubtitleVideo
		want                    string
	}{
		{"Kono Oto Tomare CHT", "[Kono Oto Tomare][02][CHT].ass", "1", ".zh-TW", []SubtitleVideo{{"video", "1", "[Kono Oto Tomare][02][1080P].mkv"}}, "video"},
		{"Kono Oto Tomare CHS", "[Kono Oto Tomare][02][CHS].ass", "1", ".zh-CN", []SubtitleVideo{{"video", "1", "[Kono Oto Tomare][02][1080P].mkv"}}, "video"},
		{"Kono Oto Tomare punctuation", "[Kono Oto Tomare][05][CHT].ass", "1", ".zh-TW", []SubtitleVideo{{"video", "1", "Kono Oto Tomare! - 05 [1080p].mkv"}}, "video"},
		{"fullwidth punctuation", "[Show][02][CHT].ass", "1", ".zh-TW", []SubtitleVideo{{"video", "1", "[Show！？][02][1080p].mkv"}}, "video"},
		{"punctuation does not pick an edition", "[Show][02][CHT].ass", "1", "", []SubtitleVideo{{"a", "1", "[Show!][02][1080p].mkv"}, {"b", "1", "[Show][02][1080p].mkv"}}, ""},
		{"flags", "[Show][02][CHT][forced][SDH].ass", "1", ".zh-TW.forced.sdh", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, "video"},
		{"mixed metadata", "[Show][02][1080p CHT][Commentary].ass", "1", ".zh-TW.commentary", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, "video"},
		{"空格集号简日字幕", "[Nekomoe kissaten] Kanojo mo Kanojo 01 [WebRip 1080p HEVC-10bit AAC].JPSC.ass", "1", ".zh-CN.bilingual-ja", []SubtitleVideo{{"video", "1", "[Nekomoe kissaten] Kanojo mo Kanojo 001 [WebRip 1080p HEVC-10bit AAC].mkv"}}, "video"},
		{"空格集号繁日字幕", "[Nekomoe kissaten] Kanojo mo Kanojo 01 [WebRip 1080p HEVC-10bit AAC].jptc.ass", "1", ".zh-TW.bilingual-ja", []SubtitleVideo{{"video", "1", "[Nekomoe kissaten] Kanojo mo Kanojo 001 [WebRip 1080p HEVC-10bit AAC].mkv"}}, "video"},
		{"双语字幕不跨发行组匹配", "[Nekomoe kissaten] Kanojo mo Kanojo 01 [WebRip 1080p HEVC-10bit AAC].JPSC.ass", "1", "", []SubtitleVideo{{"video", "1", "[KTXP] Kanojo mo Kanojo 01 [WebRip 1080p HEVC-10bit AAC].mkv"}}, ""},
		{"未知尾标记保留", "[Nekomoe kissaten] Kanojo mo Kanojo 01 [WebRip 1080p HEVC-10bit AAC].CUSTOM.JPTC.ass", "1", "", []SubtitleVideo{{"video", "1", "[Nekomoe kissaten] Kanojo mo Kanojo 001 [WebRip 1080p HEVC-10bit AAC].mkv"}}, ""},
		{"padded episode", "[Show][2][CHT].ass", "1", ".zh-TW", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, "video"},
		{"other work", "[Other Show][02][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"other episode", "[Show][03][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"other directory", "[Show][02][CHT].ass", "2", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"unknown edition", "[Show][02][CHT][AltCut].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"different version", "[Show][02v2][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"different season", "Show.S02E02.CHT.ass", "1", "", []SubtitleVideo{{"video", "1", "Show.S01E02.1080P.mkv"}}, ""},
		{"unqualified edition", "[Show][02][CHT].ass", "1", "", []SubtitleVideo{{"1080", "1", "[Show][02][1080P].mkv"}, {"720", "1", "[Show][02][720P].mkv"}}, ""},
		{"qualified resolution", "[Show][02][1080p][CHT].ass", "1", ".zh-TW", []SubtitleVideo{{"1080", "1", "[Show][02][1080P].mkv"}, {"720", "1", "[Show][02][720P].mkv"}}, "1080"},
		{"canonical video languages", "Show.S01E02.1080p.CHT.ass", "1", ".zh-TW", []SubtitleVideo{{"video", "1", "Show.S01E02.1080p.[zh-CN+zh-TW].softsub.mkv"}}, "video"},
		{"canonical tags keep edition", "Show.S01E02.1080p.CHT.ass", "1", "", []SubtitleVideo{{"video", "1", "Show.S01E02.1080p.[zh-CN+zh-TW].softsub.AltCut.mkv"}}, ""},
		{"420p unique video", "Show.S01E08.CHT.ass", "1", ".zh-TW", []SubtitleVideo{{"420", "1", "Show.S01E08.420P.mkv"}}, "420"},
		{"420p qualified resolution", "[Show][08][420p][CHT].ass", "1", ".zh-TW", []SubtitleVideo{{"420", "1", "[Show][08][420P].mkv"}, {"720", "1", "[Show][08][720P].mkv"}}, "420"},
		{"420p conflicting resolution", "[Show][08][420p][CHT].ass", "1", "", []SubtitleVideo{{"720", "1", "[Show][08][720P].mkv"}}, ""},
		{"420p conflicting declarations", "[Show][08][420p][720p][CHT].ass", "1", "", []SubtitleVideo{{"420", "1", "[Show][08][420P].mkv"}}, ""},
		{"conflicting resolution", "[Show][02][720p][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"conflicting dimensions", "[Show][02][1280x720][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1920x1080].mkv"}}, ""},
		{"conflicting qualities", "[Show][02][720p][1080p][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080p].mkv"}}, ""},
		{"different group", "[Other Group][Show][02][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Group][Show][02][1080p].mkv"}}, ""},
		{"unknown language tag", "[Show][02][UnknownLang].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080p].mkv"}}, ""},
		{"qualified codec", "[Show][02][AVC][CHT].ass", "1", ".zh-TW", []SubtitleVideo{{"avc", "1", "[Show][02][1080P][H.264].mkv"}, {"hevc", "1", "[Show][02][1080P][HEVC].mkv"}}, "avc"},
		{"unknown video specification", "[Show][02][1080p][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02].mkv"}}, ""},
		{"duplicate basename extensions", "[Show][02][CHT].ass", "1", "", []SubtitleVideo{{"mkv", "1", "[Show][02][1080P].mkv"}, {"mp4", "1", "[Show][02][1080P].mp4"}}, ""},
		{"bare episode", "[02][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[02][1080P].mkv"}}, ""},
		{"composite", "[Show][02-03][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02-03][1080P].mkv"}}, ""},
		{"special", "[Show][SP02][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][SP02][1080P].mkv"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matcher := NewSubtitleMatcher(tc.videos)
			for i := 0; i < 2; i++ {
				ids, tail := matcher.Candidates(tc.parent, tc.sub)
				id := ""
				if len(ids) == 1 {
					id = ids[0]
				} else {
					tail = ""
				}
				if id != tc.want || tail != tc.tail {
					t.Fatalf("got %q %q, want %q %q", id, tail, tc.want, tc.tail)
				}
			}
		})
	}
}
