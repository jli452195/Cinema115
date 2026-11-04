package mediafacts

import (
	"testing"

	"strm115/backend/internal/naming"
	"strm115/backend/internal/releaseparse"
)

func TestExplicitResolutionTagsAreNotLimitedToCommonHeights(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"未确认进行式.2014.S01E08.420P.mkv", "420p"},
		{"Show.S01E08.360p.mkv", "360p"},
		{"Show.S01E08.540P.mkv", "540p"},
		{"Show.S01E08.900i.mkv", "900i"},
		{"Show.S01E08.640x420.mkv", "420p"},
		{"Show.S01E08.960×540i.mkv", "540i"},
		{"Show.S01E08.420P.640x420.mkv", "420p"},
		{"Show.S01E08.420P.1080p.mkv", ""},
		{"Show.S01E08.420P.420i.mkv", ""},
		{"Show.S01E08.420Pixels.mkv", ""},
		{"Show.S01E08.AI420p.mkv", ""},
		{"Show.S01E08.420.mkv", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Search(tc.name).Resolution; got != tc.want {
				t.Fatalf("分辨率声明识别错误: got %q, want %q", got, tc.want)
			}
			if got := Values(tc.name, releaseparse.Spec{}, "")[naming.Resolution]; got != tc.want {
				t.Fatalf("公共命名丢失分辨率或保留冲突声明: got %q, want %q", got, tc.want)
			}
		})
	}
}
