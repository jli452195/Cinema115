package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/service"
	"strm115/backend/internal/strm"
)

func TestDrive115LocalMappingAndStrictURL(t *testing.T) {
	value := strm.Drive{PickCode: "abc123", FileID: "123", SHA1: strings.Repeat("a", 40), Size: 42, Name: "电影 #1+%?.mkv"}
	raw, err := strm.DriveURL("http://retired.invalid", value)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.strm"), []byte(raw+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	share := NewShareAPI(func() string { return "" })
	share.OutputRoot = root
	h := &embyProxyHandler{config: service.EmbyProxyConfig{EmbyPathPrefix: "/media"}, share: share}
	claim, err := h.playbackClaim(map[string]json.RawMessage{"Path": json.RawMessage(`"/media/movie.strm"`)})
	if err != nil || claim.FileID != value.FileID || claim.PickCode != value.PickCode || claim.SHA1 != strings.ToUpper(value.SHA1) || claim.Size != value.Size || claim.Name != value.Name || claim.Code != "" || claim.Password != "" {
		t.Fatalf("native declaration changed: %+v %v", claim, err)
	}
	for _, bad := range []string{raw + "?token=x", raw + "?", raw + "#fragment", strings.Replace(raw, "/123/", "/%31%32%33/", 1), strings.Replace(raw, "retired.invalid", "u:p@retired.invalid", 1), strings.Replace(raw, "%23", "#", 1)} {
		if _, ok := parsePickCodeSTRMClaim(bad); ok {
			t.Errorf("unsafe native declaration accepted: %q", bad)
		}
	}
}
