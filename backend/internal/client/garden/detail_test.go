package garden

import (
	"encoding/json"
	"testing"
)

func TestDetailPathsAndFileListValidation(t *testing.T) {
	for _, p := range []string{"/detail/dmhy/688777", "/detail/moe/abc123"} {
		if !validDetailPath(p) {
			t.Fatal(p)
		}
	}
	for _, p := range []string{"/detail/infohash/abc", "/detail/dmhy/../x", "/detail/dmhy/x/extra", "/detail/evil/x"} {
		if validDetailPath(p) {
			t.Fatal(p)
		}
	}
	for _, name := range []string{"../a.mkv", "/a.mkv", "a/../b.mkv", "a\\b.mkv", "a//b.mkv", "a/./b.mkv", "C:/a.mkv", "a\x00.mkv", ".", ".."} {
		d := ResourceDetail{Status: "OK", Resource: Resource{Provider: "dmhy", ProviderID: "1"}}
		d.Detail.Files = []DetailFile{{Name: name}}
		body, _ := json.Marshal(d)
		if _, err := ParseResourceDetail(body, "dmhy", "1"); err == nil {
			t.Fatal("unsafe path accepted", name)
		}
	}
}
