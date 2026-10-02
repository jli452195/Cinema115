package httpapi

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/service"
)

const play115TestPath = "/play115/d129obvw5ewzwx29g/3432417343848548293/FE01D1140784BC8568312793F65C35DD172D3B1F/589916580/"

func TestPlay115LocalSTRMMapping(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "movie.strm"), []byte("http://retired.invalid"+play115TestPath+"movie.mkv\n"), 0600); err != nil {
		t.Fatal(err)
	}
	share := NewShareAPI(func() string { return "" })
	share.OutputRoot = root
	h := &embyProxyHandler{config: service.EmbyProxyConfig{EmbyPathPrefix: "/media"}, share: share}
	claim, err := h.playbackClaim(map[string]json.RawMessage{"Path": json.RawMessage(`"/media/movie.strm"`)})
	if err != nil || claim.FileID != "3432417343848548293" || claim.PickCode != "d129obvw5ewzwx29g" || claim.Size != 589916580 || claim.Name != "movie.mkv" {
		t.Fatalf("mapped STRM failed: %+v, %v", claim, err)
	}
}

func TestPlay115ExtractsAllFields(t *testing.T) {
	const name = "黑化吧！圣女大人 [tmdbid=293124].S01E01.1080p [LoliHouse].mkv"
	for _, filename := range []string{name, url.PathEscape(name)} {
		raw := "http://192.168.0.3:12366" + play115TestPath + filename
		claim, ok := parsePickCodeSTRMClaim(raw)
		if !ok || claim.PickCode != "d129obvw5ewzwx29g" || claim.FileID != "3432417343848548293" || claim.SHA1 != "FE01D1140784BC8568312793F65C35DD172D3B1F" || claim.Size != 589916580 || claim.Name != name || claim.Code != "" || claim.Password != "" {
			t.Fatalf("wrong parsed fields: %+v, ok=%t", claim, ok)
		}
		pc, ok := parsePickCodeSTRM(raw)
		if !ok || pc != claim.PickCode {
			t.Fatal("recognition and claim extraction disagree")
		}
	}
	claim, ok := parsePickCodeSTRMClaim("\ufeffhttps://another.invalid" + strings.ToLower(play115TestPath) + "a+b%2520.mkv\n")
	if !ok || claim.Name != "a+b%20.mkv" || claim.SHA1 != "FE01D1140784BC8568312793F65C35DD172D3B1F" {
		t.Fatalf("normalization changed path data: %+v", claim)
	}
}

func TestPlay115RejectsMalformedFields(t *testing.T) {
	valid := "http://nas" + play115TestPath + "movie.mkv"
	for _, raw := range []string{
		strings.Replace(valid, "d129obvw5ewzwx29g", "abc", 1),
		strings.Replace(valid, "d129obvw5ewzwx29g", "%64129obvw5ewzwx29g", 1),
		strings.Replace(valid, "3432417343848548293", "0", 1),
		strings.Replace(valid, "3432417343848548293", "-1", 1),
		strings.Replace(valid, "3432417343848548293", "18446744073709551616", 1),
		strings.Replace(valid, "FE01D1140784BC8568312793F65C35DD172D3B1F", "invalid", 1),
		strings.Replace(valid, "589916580", "0", 1),
		strings.Replace(valid, "589916580", "-1", 1),
		strings.Replace(valid, "589916580", "+1", 1),
		strings.Replace(valid, "589916580", "1.5", 1),
		strings.Replace(valid, "589916580", "9223372036854775808", 1),
		strings.Replace(valid, "589916580", "%35", 1),
		strings.TrimSuffix(valid, "/movie.mkv"),
		valid + "/extra", valid + "?token=x", valid + "?", valid + "#fragment",
		strings.Replace(valid, "http://nas", "http://user:password@nas", 1),
		strings.Replace(valid, "http://nas", "file://nas", 1),
	} {
		if _, ok := parsePickCodeSTRMClaim(raw); ok {
			t.Errorf("malformed link accepted: %s", raw)
		}
	}
	for _, name := range []string{"", "%20", ".", "%2e%2e", "a%2fb", "a%5cb", "a%00b", "a%0ab", "a%09b", "a%7fb", "%ff", "%zz"} {
		if _, ok := parsePickCodeSTRMClaim("http://nas" + play115TestPath + name); ok {
			t.Errorf("invalid filename accepted: %s", name)
		}
	}
}

func TestRedirect115RetainsOptionalFilename(t *testing.T) {
	const hash = "fe01d1140784bc8568312793f65c35dd172d3b1f"
	for _, suffix := range []string{"", "/" + hash} {
		claim, ok := parsePickCodeSTRMClaim("http://nas/api/redirect115/abc123" + suffix + "/%E7%94%B5%E5%BD%B1.mkv")
		if !ok || claim.Name != "电影.mkv" || claim.Size != 0 || claim.FileID != "" || (suffix != "" && claim.SHA1 != strings.ToUpper(hash)) {
			t.Fatalf("wrong redirect115 fields: %+v", claim)
		}
	}
}
