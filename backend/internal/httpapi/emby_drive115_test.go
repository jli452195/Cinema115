package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"strm115/backend/internal/strm"
)

func TestDrive115MediaSourceAndStrictURL(t *testing.T) {
	value := strm.Drive{PickCode: "abc123", FileID: "123", SHA1: strings.Repeat("a", 40), Size: 42, Name: "电影 #1+%?.mkv"}
	raw, err := strm.DriveURL("http://retired.invalid", value)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := json.Marshal(raw)
	h := &embyProxyHandler{share: NewShareAPI(func() string { return "" })}
	claim, err := h.playbackClaim(map[string]json.RawMessage{"Path": path})
	if err != nil || claim.FileID != value.FileID || claim.PickCode != value.PickCode || claim.SHA1 != strings.ToUpper(value.SHA1) || claim.Size != value.Size || claim.Name != value.Name || claim.Code != "" || claim.Password != "" {
		t.Fatalf("native declaration changed: %+v %v", claim, err)
	}
	for _, bad := range []string{raw + "?token=x", raw + "?", raw + "#fragment", strings.Replace(raw, "/123/", "/%31%32%33/", 1), strings.Replace(raw, "retired.invalid", "u:p@retired.invalid", 1), strings.Replace(raw, "%23", "#", 1)} {
		if _, ok := parsePickCodeSTRMClaim(bad); ok {
			t.Errorf("unsafe native declaration accepted: %q", bad)
		}
	}
}
