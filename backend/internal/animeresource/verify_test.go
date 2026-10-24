package animeresource

import (
	"context"
	"crypto/sha1"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"strm115/backend/internal/client/animefeed"
)

func testTorrent() ([]byte, string) {
	info := "d6:lengthi1e4:name1:x12:piece lengthi1e6:pieces20:" + strings.Repeat("p", 20) + "e"
	h := sha1.Sum([]byte(info))
	return []byte("d4:info" + info + "e"), hex.EncodeToString(h[:])
}

func TestTorrentHashExactInfoAndV1Semantics(t *testing.T) {
	body, want := testTorrent()
	for _, raw := range [][]byte{body, []byte("d8:announce17:https://tracker/x4:info" + string(body[7:len(body)-1]) + "e")} {
		got, err := TorrentHash(raw)
		if err != nil || got != want {
			t.Fatal("did not hash exact info slice", got, want, err)
		}
	}
	multi := "d4:infod5:filesld6:lengthi1e4:pathl1:aee" + "d6:lengthi1e4:pathl1:beee4:name1:x12:piece lengthi2e6:pieces20:" + strings.Repeat("p", 20) + "ee"
	if _, err := TorrentHash([]byte(multi)); err != nil {
		t.Fatal("valid multi-file BT v1 rejected", err)
	}
}

func TestTorrentHashRejectsMalformedAndUnsupported(t *testing.T) {
	body, _ := testTorrent()
	for _, raw := range []string{
		"", "<html>challenge</html>", string(body) + "garbage", string(body[:len(body)-1]),
		"d4:infoi1ee", "d4:infodee", strings.Replace(string(body), "i1e", "i01e", 1),
		strings.Replace(string(body), "6:lengthi1e", "6:lengthi-0e", 1),
		strings.Replace(string(body), "6:lengthi1e", "6:lengthi9223372036854775808e", 1),
		strings.Replace(string(body), "6:lengthi1e", "6:lengthi1e6:lengthi1e", 1),
		strings.Replace(string(body), "6:lengthi1e4:name1:x", "4:name1:x6:lengthi1e", 1),
		strings.Replace(string(body), "piece lengthi1e", "piece lengthi0e", 1),
		strings.Replace(string(body), "6:pieces20:", "6:pieces21:", 1),
		strings.Replace(string(body), "6:lengthi1e", "6:lengthi100e", 1),
		strings.Replace(string(body), "4:name1:x", "4:name0:", 1),
		strings.Repeat("l", 40) + strings.Repeat("e", 40), strings.Repeat("x", MaxTorrentBytes+1),
		"d4:infod6:lengthi1e12:meta versioni2e4:name1:x12:piece lengthi1e6:pieces20:" + strings.Repeat("p", 20) + "ee",
	} {
		if _, err := TorrentHash([]byte(raw)); err == nil {
			t.Fatal("accepted invalid torrent")
		}
	}
}

func TestTorrentHashPreservesErrorKinds(t *testing.T) {
	body, _ := testTorrent()
	v2 := strings.Replace(string(body), "4:name", "12:meta versioni2e4:name", 1)
	if _, err := TorrentHash([]byte(v2)); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unsupported protocol error lost", err)
	}
	if _, err := TorrentHash(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid metadata error lost", err)
	}
}

func TestMagnetHashCanonicalAndUnambiguous(t *testing.T) {
	_, hash := testTorrent()
	bytes, _ := hex.DecodeString(hash)
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes)
	for _, value := range []string{hash, strings.ToUpper(hash), b32, strings.ToLower(b32)} {
		got, err := MagnetHash("magnet:?dn=ignored&xt=urn:btih:" + value + "&tr=https%3A%2F%2Ftracker.test")
		if err != nil || got != hash {
			t.Fatal("base32/hex identity differed", err)
		}
	}
	for _, raw := range []string{"", "ed2k://file", "https://x/" + hash + ".torrent", "magnet:?xt=urn:btmh:1220" + hash, "magnet:?xt=urn:btih:" + hash + "&xt=urn:btih:" + hash, "magnet:?xt=urn:btih:" + hash + "#fragment", "magnet:?xt=urn:btih:abc", "magnet:?xt=urn:btih:" + hash + "&bad=%zz"} {
		if _, err := MagnetHash(raw); err == nil {
			t.Fatal("accepted ambiguous/invalid magnet", raw)
		}
	}
}

type torrentFunc func(context.Context, string, string, string) ([]byte, error)

func (f torrentFunc) ReadTorrent(ctx context.Context, p, key, raw string) ([]byte, error) {
	return f(ctx, p, key, raw)
}

func TestVerifyActualTorrentNotURLFilenameOrGUID(t *testing.T) {
	body, hash := testTorrent()
	post := strings.Repeat("a", 40)
	row := animefeed.SourceRelease{PostKey: post, Title: "episode", OriginalGUID: strings.Repeat("b", 40), TorrentURL: "https://mikanani.me/Download/20261019/" + post + ".torrent"}
	reads := 0
	reader := torrentFunc(func(context.Context, string, string, string) ([]byte, error) {
		reads++
		return body, nil
	})
	v, err := Verify(context.Background(), "mikan", row, reader)
	if err != nil || !v.Valid() || v.Hash() != hash || v.Hash() == row.PostKey || v.Evidence().Method != "torrent_info" || v.Evidence().MetadataSHA256 == "" {
		t.Fatal("filename/GUID used as hash", err)
	}
	row.MagnetHint = "magnet:?xt=urn:btih:" + strings.Repeat("c", 40)
	if _, err := Verify(context.Background(), "mikan", row, reader); !errors.Is(err, ErrConflict) {
		t.Fatal("conflicting evidence accepted", err)
	}
	row.PostKey, row.TorrentURL, row.MagnetHint = "rel_anibt", "https://anibt.net/api/torrent/rel_anibt.torrent", ""
	if v, err := Verify(context.Background(), "anibt", row, reader); err != nil || v.Hash() != hash {
		t.Fatal(err)
	}
	row.PostKey, row.TorrentURL, row.MagnetHint = "rel_dmhy_123", "", "magnet:?xt=urn:btih:"+hash
	if v, err := Verify(context.Background(), "garden", row, nil); err != nil || v.Hash() != hash || v.Evidence().Method != "source_magnet" {
		t.Fatal(err)
	}
	if reads != 3 {
		t.Fatal("verification must read each Mikan/AniBT torrent only once", reads)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTorrentHTTPBoundsOriginRedirectAndCredentials(t *testing.T) {
	key := strings.Repeat("a", 40)
	endpoint := "https://mikanani.me/Download/20261019/" + key + ".torrent"
	for _, raw := range []string{"http://mikanani.me/Download/20261019/" + key + ".torrent", strings.Replace(endpoint, "mikanani.me", "127.0.0.1", 1), endpoint + "?token=secret", endpoint + "#x", strings.Replace(endpoint, "mikanani.me", "mikanani.me:443", 1), strings.Replace(endpoint, "/Download/", "/%44ownload/", 1)} {
		if _, err := TorrentURL("mikan", key, raw); err == nil {
			t.Fatal("uncontrolled torrent URL accepted", raw)
		}
	}
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"success", 200, "torrent"}, {"redirect", 302, "redirect"}, {"challenge", 403, "secret upstream body"}, {"too large", 200, strings.Repeat("x", MaxTorrentBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.String() != endpoint || req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" {
					t.Fatal("unexpected target or leaked credentials")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": []string{"https://127.0.0.1/private"}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: req}, nil
			})}
			body, err := (&HTTPReader{HTTP: client}).ReadTorrent(context.Background(), "mikan", key, endpoint)
			if calls != 1 {
				t.Fatal("followed redirect", calls)
			}
			if tc.name == "success" {
				if err != nil || string(body) != "torrent" {
					t.Fatal(err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe response accepted", err)
			}
		})
	}
}

func TestVerifyCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Verify(ctx, "garden", animefeed.SourceRelease{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	reader := &HTTPReader{HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}}
	if _, err := reader.ReadTorrent(ctx, "anibt", "rel_a", "https://anibt.net/api/torrent/rel_a.torrent"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRestoreVerifiedEvidenceIsStrict(t *testing.T) {
	row := animefeed.SourceRelease{PostKey: "rel_dmhy_1", Title: "episode", MagnetHint: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)}
	v, err := Verify(context.Background(), "garden", row, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Restore(v.Hash(), VerificationVersion, v.VerifiedAt(), v.Evidence()); err != nil || got.Hash() != v.Hash() {
		t.Fatal(err)
	}
	for _, change := range []func(*Evidence){
		func(e *Evidence) { e.ContentFingerprint = "filename" }, func(e *Evidence) { e.Method = "filename" }, func(e *Evidence) { e.Provider = "unknown" }, func(e *Evidence) { e.MetadataSHA256 = "unexpected" },
	} {
		e := v.Evidence()
		change(&e)
		if _, err := Restore(v.Hash(), VerificationVersion, v.VerifiedAt(), e); err == nil {
			t.Fatal("invalid saved proof accepted")
		}
	}
	if _, err := Restore(strings.ToUpper(v.Hash()), VerificationVersion, v.VerifiedAt(), v.Evidence()); err == nil {
		t.Fatal("noncanonical hash restored")
	}
	if _, err := Restore(v.Hash(), VerificationVersion+1, v.VerifiedAt(), v.Evidence()); err == nil {
		t.Fatal("unknown verifier accepted")
	}
}

func FuzzTorrentHash(f *testing.F) {
	body, _ := testTorrent()
	f.Add(body)
	f.Add([]byte("d4:infodee"))
	f.Fuzz(func(t *testing.T, body []byte) {
		hash, err := TorrentHash(body)
		if err == nil && (len(hash) != 40 || strings.ToLower(hash) != hash) {
			t.Fatal(fmt.Sprint("invalid hash ", hash))
		}
	})
}
