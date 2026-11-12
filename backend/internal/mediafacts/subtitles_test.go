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
		{"Show.CHS.forced.ass", "local", ".CHS.forced", true, []string{"short"}},
		{"SHOW.chs.FORCED.ass", "other", ".FORCED", true, []string{"long", "duplicate"}},
		{"Show.CHS.ass", "missing", "", false, []string{"long", "duplicate"}},
		{"Show.CHS.ass", "missing", "", true, nil},
		{"Show2.ass", "local", "", true, nil},
		{"ς.ſ.k.中文.CHT.ass", "local", ".CHT", true, []string{"unicode"}},
		{"σ.s.K.中文.ass", "other", "", false, []string{"unicode"}},
	} {
		t.Run(tc.name+"/"+tc.parent, func(t *testing.T) {
			owners, tail := matcher.StemCandidates(tc.parent, tc.name, tc.sameDirectory)
			if !slices.Equal(owners, tc.owners) || tail != tc.tail {
				t.Fatalf("字幕归属或原始后缀错误：%v %q，期望 %v %q", owners, tail, tc.owners, tc.tail)
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
		{"Kono Oto Tomare CHT", "[Kono Oto Tomare][02][CHT].ass", "1", ".zh-Hant", []SubtitleVideo{{"video", "1", "[Kono Oto Tomare][02][1080P].mkv"}}, "video"},
		{"Kono Oto Tomare CHS", "[Kono Oto Tomare][02][CHS].ass", "1", ".zh-Hans", []SubtitleVideo{{"video", "1", "[Kono Oto Tomare][02][1080P].mkv"}}, "video"},
		{"Kono Oto Tomare punctuation", "[Kono Oto Tomare][05][CHT].ass", "1", ".zh-Hant", []SubtitleVideo{{"video", "1", "Kono Oto Tomare! - 05 [1080p].mkv"}}, "video"},
		{"fullwidth punctuation", "[Show][02][CHT].ass", "1", ".zh-Hant", []SubtitleVideo{{"video", "1", "[Show！？][02][1080p].mkv"}}, "video"},
		{"punctuation does not pick an edition", "[Show][02][CHT].ass", "1", "", []SubtitleVideo{{"a", "1", "[Show!][02][1080p].mkv"}, {"b", "1", "[Show][02][1080p].mkv"}}, ""},
		{"flags", "[Show][02][CHT][forced][SDH].ass", "1", ".zh-Hant.forced.sdh", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, "video"},
		{"padded episode", "[Show][2][CHT].ass", "1", ".zh-Hant", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, "video"},
		{"other work", "[Other Show][02][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"other episode", "[Show][03][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"other directory", "[Show][02][CHT].ass", "2", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"unknown edition", "[Show][02][CHT][Commentary].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"different version", "[Show][02v2][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"different season", "Show.S02E02.CHT.ass", "1", "", []SubtitleVideo{{"video", "1", "Show.S01E02.1080P.mkv"}}, ""},
		{"unqualified edition", "[Show][02][CHT].ass", "1", "", []SubtitleVideo{{"1080", "1", "[Show][02][1080P].mkv"}, {"720", "1", "[Show][02][720P].mkv"}}, ""},
		{"qualified resolution", "[Show][02][1080p][CHT].ass", "1", ".zh-Hant", []SubtitleVideo{{"1080", "1", "[Show][02][1080P].mkv"}, {"720", "1", "[Show][02][720P].mkv"}}, "1080"},
		{"420p unique video", "Show.S01E08.CHT.ass", "1", ".zh-Hant", []SubtitleVideo{{"420", "1", "Show.S01E08.420P.mkv"}}, "420"},
		{"420p qualified resolution", "[Show][08][420p][CHT].ass", "1", ".zh-Hant", []SubtitleVideo{{"420", "1", "[Show][08][420P].mkv"}, {"720", "1", "[Show][08][720P].mkv"}}, "420"},
		{"420p conflicting resolution", "[Show][08][420p][CHT].ass", "1", "", []SubtitleVideo{{"720", "1", "[Show][08][720P].mkv"}}, ""},
		{"420p conflicting declarations", "[Show][08][420p][720p][CHT].ass", "1", "", []SubtitleVideo{{"420", "1", "[Show][08][420P].mkv"}}, ""},
		{"conflicting resolution", "[Show][02][720p][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080P].mkv"}}, ""},
		{"conflicting dimensions", "[Show][02][1280x720][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1920x1080].mkv"}}, ""},
		{"conflicting qualities", "[Show][02][720p][1080p][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080p].mkv"}}, ""},
		{"different group", "[Other Group][Show][02][CHT].ass", "1", "", []SubtitleVideo{{"video", "1", "[Group][Show][02][1080p].mkv"}}, ""},
		{"unknown language tag", "[Show][02][UnknownLang].ass", "1", "", []SubtitleVideo{{"video", "1", "[Show][02][1080p].mkv"}}, ""},
		{"qualified codec", "[Show][02][AVC][CHT].ass", "1", ".zh-Hant", []SubtitleVideo{{"avc", "1", "[Show][02][1080P][H.264].mkv"}, {"hevc", "1", "[Show][02][1080P][HEVC].mkv"}}, "avc"},
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
