package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"strm115/backend/internal/client/p115"
)

// Opt-in canary. Live database is opened read-only; account and playback state
// are written only to temporary test fixtures. Transport permits only reads of
// one recorded managed-account copy, never uploads, scans or primary reads.
func TestPlaybackReuseLiveCanary(t *testing.T) {
	root := os.Getenv("CINEMA115_REUSE_CANARY_ROOT")
	if root == "" {
		t.Skip("set CINEMA115_REUSE_CANARY_ROOT for the read-only live canary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	env, err := godotenv.Read(filepath.Join(root, ".env"))
	if err != nil || env["JWT_SECRET"] == "" {
		t.Fatal("canary secret unavailable")
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(root, "data", "cinema115.db"))+"?mode=ro")
	if err != nil {
		t.Fatal("canary read-only database unavailable")
	}
	defer db.Close()
	var accountID, uid, cid, rawCopy, rawMaterial string
	var sealed []byte
	err = db.QueryRowContext(ctx, `SELECT a.id,a.uid,a.playback_parent_cid,a.cookie_enc,c.record,m.record
		FROM playback_copies c JOIN p115_accounts a ON a.id=c.account_id
		JOIN playback_materials m ON m.id=json_extract(c.record,'$.materialId')
		WHERE a.ownership<>'member' AND a.owner_user_id='' AND a.is_primary=0
		AND json_extract(c.record,'$.status')='ready' AND json_extract(c.record,'$.uid')=a.uid
		ORDER BY c.rowid DESC LIMIT 1`).Scan(&accountID, &uid, &cid, &sealed, &rawCopy, &rawMaterial)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatal("no eligible recorded managed copy for canary")
		}
		t.Fatal("read-only canary selection failed", err)
	}
	key := sha256.Sum256([]byte("cinema115-sqlite-p115\x00" + env["JWT_SECRET"]))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal("canary key invalid")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		t.Fatal("canary credential invalid")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(accountID))
	if err != nil {
		t.Fatal("canary credential unavailable")
	}
	cookie := string(plain)
	var copy PlaybackCacheEntry
	var material PlaybackMaterial
	if json.Unmarshal([]byte(rawCopy), &copy) != nil || json.Unmarshal([]byte(rawMaterial), &material) != nil || material.VerifiedAt.IsZero() || copy.UID != uid || p115.CookieUserID(cookie) != uid || !cacheMatches(copy.File, &p115.DownloadInfo{SHA1: material.SHA1, Size: material.Size}) {
		t.Fatal("canary identity not verified")
	}
	client := p115.NewShareClient()
	guard := &reuseCanaryTransport{next: client.HTTP.Transport, cookie: cookie, cid: cid, fileID: copy.File.ID, counts: make(map[string]int)}
	client.HTTP.Transport = guard
	client.ReadRetryDelays = nil
	s, remote, id := memberFixture(t)
	memberScan(t, s, id)
	r := s.record(id)
	a, err := s.playbackAccount(r)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the fixture's account/user association while substituting only
	// this canary's fixed credentials and directory in isolated memory.
	s.accounts.mu.Lock()
	for i := range s.accounts.value.Accounts {
		if s.accounts.value.Accounts[i].ID == a.ID {
			s.accounts.value.Accounts[i].Cookie = cookie
			s.accounts.value.Accounts[i].UID = uid
			s.accounts.value.Accounts[i].PlaybackParentCID = cid
		}
	}
	s.accounts.mu.Unlock()
	s.mu.Lock()
	r.DirectoryID = cid
	s.records[id] = r
	s.mu.Unlock()
	a, err = s.playbackAccount(r)
	if err != nil {
		t.Fatal("canary fixture binding invalid")
	}
	s.drive = mediaDirectoryDrive{memberDriveClient: s.drive, check: client.ValidateMediaPlaybackDirectory}
	cache, _, _ := setupFastCache(t)
	cache.client = client
	material.Origins = nil
	if err := cache.putMaterial(material); err != nil {
		t.Fatal("canary fixture material failed")
	}
	copy.AccountID, copy.ParentCID, copy.SourceKey = a.ID, cid, ""
	if _, err := cache.storeReadyCopy(copy); err != nil {
		t.Fatal("canary fixture copy failed")
	}
	selected := &PlaybackCacheSelection{binding: PlaybackBinding{AccountID: a.ID, ParentCID: cid}, accountRevision: a.Revision, uid: uid, cookie: cookie}
	user := remote.users[r.EmbyID]
	for _, mode := range []string{"baseline", "reuse", "reuse-again", "cached"} {
		requestCtx := ctx
		if mode != "baseline" {
			requestCtx = WithPlaybackFileReuse(ctx)
		}
		started := time.Now()
		guard.reset()
		grant, err := s.BeginMediaAuthorization(requestCtx, r.EmbyURL, r.EmbyID, false, &user)
		if err != nil {
			t.Fatal("live canary authorization failed (details withheld)")
		}
		if mode == "cached" {
			err = grant.ValidateTargetDirectory(requestCtx)
		} else {
			var report PlaybackResolution
			_, report, err = cache.ResolvePickCodeWithReport(WithPlaybackSHA1Hint(grant.ResolutionContext(requestCtx), material.SHA1), selected, "canary-local-material", "", p115.PlaybackUserAgent, "canary-origin")
			if report.UsedSource {
				t.Fatal("canary unexpectedly used primary")
			}
		}
		if err != nil {
			_ = grant.Close()
			t.Fatal("live canary resolution failed (details withheld)")
		}
		if err := grant.Commit(requestCtx); err != nil {
			_ = grant.Close()
			t.Fatal("live canary commit failed")
		}
		if err := grant.Close(); err != nil {
			t.Fatal("canary close failed")
		}
		counts := guard.snapshot()
		wantDirectory, wantLink := 0, 1
		if mode == "baseline" {
			wantDirectory = 1
		}
		if mode == "cached" {
			wantDirectory, wantLink = 1, 0
		}
		if counts["/files"] != wantDirectory || counts["/app/chrome/downurl"] != wantLink || counts["blocked"] != 0 {
			t.Fatal("live canary request budget violated", counts)
		}
		t.Logf("%s elapsed=%dms requests=%v primary=0 writes=0", mode, time.Since(started).Milliseconds(), counts)
	}
}

type reuseCanaryTransport struct {
	next                http.RoundTripper
	cookie, cid, fileID string
	mu                  sync.Mutex
	counts              map[string]int
}

func (g *reuseCanaryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	allowed := r.URL.Scheme == "https" && r.Header.Get("Cookie") == g.cookie && (r.URL.Host == "proapi.115.com" && r.Method == http.MethodPost && r.URL.Path == "/app/chrome/downurl" ||
		r.URL.Host == "webapi.115.com" && r.Method == http.MethodGet && (r.URL.Path == "/files" && r.URL.Query().Get("cid") == g.cid && r.URL.Query().Get("limit") == "1" || r.URL.Path == "/files/get_info" && r.URL.Query().Get("file_id") == g.fileID))
	g.mu.Lock()
	if allowed {
		g.counts[r.URL.Path]++
	} else {
		g.counts["blocked"]++
	}
	g.mu.Unlock()
	if !allowed {
		return nil, errors.New("canary rejected non-allowlisted request")
	}
	return g.next.RoundTrip(r)
}

func (g *reuseCanaryTransport) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counts = make(map[string]int)
}
func (g *reuseCanaryTransport) snapshot() map[string]int {
	g.mu.Lock()
	defer g.mu.Unlock()
	result := make(map[string]int, len(g.counts))
	for k, v := range g.counts {
		result[k] = v
	}
	return result
}
