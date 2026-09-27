package p115

import (
	"strings"
	"testing"
)

func TestPickCodeInfoValidatesIdentityAndDirectPlayback(t *testing.T) {
	raw := `{"12":{"file_id":"12","pick_code":"abc123","file_name":"movie.mkv","file_size":"100","sha1":"` + strings.Repeat("a", 40) + `","url":{"url":"https://cdn.115.com/video?f=1"}}}`
	info, id, err := parsePickCodeInfo([]byte(raw), "abc123")
	if err != nil || id != "12" || info.Name != "movie.mkv" || info.Size != 100 || info.SHA1 != strings.Repeat("A", 40) {
		t.Fatal("metadata mismatch", err)
	}
	for _, bad := range []string{
		strings.Replace(raw, `"file_id":"12"`, `"file_id":"13"`, 1),
		strings.Replace(raw, `"pick_code":"abc123"`, `"pick_code":"other123"`, 1),
		strings.Replace(raw, `"file_size":"100"`, `"file_size":"invalid"`, 1),
		strings.Replace(raw, `"file_size":"100"`, `"file_size":"0"`, 1),
		strings.Replace(raw, `"file_name":"movie.mkv"`, `"file_name":""`, 1),
		strings.Replace(raw, "f=1", "f=3", 1),
		strings.Replace(raw, "cdn.115.com", "127.0.0.1", 1),
		`{"12":{},"13":{}}`, `{"12":{}}`,
	} {
		if _, _, err := parsePickCodeInfo([]byte(bad), "abc123"); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
}
