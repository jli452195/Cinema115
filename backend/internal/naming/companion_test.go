package naming_test

import (
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/naming"
)

func TestCompanionPreservesFullStemAndByteBudget(t *testing.T) {
	video := naming.Result{Directory: "Title/Season 01", Stem: "Title.Name.S01E13-E14.Group.v3"}
	got, err := naming.Companion(video, ".zh-TW.default", "original.ASS")
	if err != nil || got.Name != "Title.Name.S01E13-E14.Group.v3.zh-TW.default.ass" || got.RelativePath != "Title/Season 01/"+got.Name {
		t.Fatal(got, err)
	}
	for _, video := range []naming.Result{{Directory: "../escape", Stem: "Title"}, {Directory: "/absolute", Stem: "Title"}, {Stem: "../escape"}} {
		if _, err := naming.Companion(video, "", "sub.srt"); !errors.Is(err, naming.ErrInput) {
			t.Fatal("unsafe path accepted", video, err)
		}
	}
	for _, input := range []struct{ stem, tail, name string }{
		{"", "", "sub.srt"}, {video.Stem, "/../escape", "sub.srt"}, {video.Stem, "", "dir/sub.srt"}, {video.Stem, "", "no-extension"}, {strings.Repeat("名", 80), "", "sub.srt"},
	} {
		if _, err := naming.Companion(naming.Result{Stem: input.stem}, input.tail, input.name); !errors.Is(err, naming.ErrInput) {
			t.Fatal("invalid companion accepted", input, err)
		}
	}
}
