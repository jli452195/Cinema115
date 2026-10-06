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
// one recorded managed-account file (optionally locating it by SHA1), never
// uploads, scans or primary reads.
func TestPlaybackReuseLiveCanary(t *testing.T) {
	runPlaybackReuseLiveCanary(t, false)
}

func TestRedirect115TargetFirstLiveCanary(t *testing.T) {
	runPlaybackReuseLiveCanary(t, true)
}

func TestPlay115TargetFirstLiveCanary(t *testing.T) {
	runPlaybackReuseLiveCanary(t, true, true)
}

func TestSHA1VersusPickCodeLiveCanary(t *testing.T) {
	runPlaybackReuseLiveCanary(t, true, false, true)
}

func TestPlaybackWritePreparationLiveCanary(t *testing.T) {
	runPlaybackReuseLiveCanary(t, false, false, false, true)
}

func runPlaybackReuseLiveCanary(t *testing.T, targetFirst bool, play115 ...bool) {
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
	if targetFirst {
		guard.hash = material.SHA1
	}
	client.HTTP.Transport = guard
	client.ReadRetryDelays = nil
	if len(play115) > 2 && play115[2] {
		guard.allowUploadInfo = true
		runWritePreparationLiveComparison(t, ctx, guard, accountID, cid, cookie)
		return
	}
	if len(play115) > 1 && play115[1] {
		// Compare read-only paths against the same recorded managed-account
		// content. Alternate order to avoid assigning all warm connections to
		// one path. No metadata or link from a prior round satisfies a query.
		for round := 1; round <= 4; round++ {
			modes := []string{"sha1", "pickcode"}
			if round%2 == 0 {
				modes = []string{"pickcode", "sha1"}
			}
			for _, mode := range modes {
				guard.reset()
				started := time.Now()
				expected := copy.File
				if mode == "sha1" {
					candidate, err := client.FindPlaybackCandidateBySHA1(ctx, material.SHA1, cookie)
					if err != nil || candidate == nil {
						t.Fatal("live SHA1 lookup failed (details withheld)")
					}
					expected = *candidate
				}
				guard.fileID = expected.ID
				if _, err := client.VerifiedPlaybackURL(ctx, expected, cookie, p115.PlaybackUserAgent); err != nil {
					t.Fatal("live file link failed (details withheld)")
				}
				counts := guard.snapshot()
				wantSearch := 0
				if mode == "sha1" {
					wantSearch = 1
				}
				if counts["blocked"] != 0 || counts["/files/shasearch"] != wantSearch || counts["/app/chrome/downurl"] != 1 {
					t.Fatal("unexpected query count", counts)
				}
				t.Logf("round=%d mode=%s elapsed=%dms requests=%v sameRecordedFile=%t primary=0 writes=0", round, mode, time.Since(started).Milliseconds(), counts, expected.ID == copy.File.ID)
			}
		}
		return
	}
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
	if !targetFirst {
		if err := cache.putMaterial(material); err != nil {
			t.Fatal("canary fixture material failed")
		}
		copy.AccountID, copy.ParentCID, copy.SourceKey = a.ID, cid, ""
		if _, err := cache.storeReadyCopy(copy); err != nil {
			t.Fatal("canary fixture copy failed")
		}
	}
	selected := &PlaybackCacheSelection{binding: PlaybackBinding{AccountID: a.ID, ParentCID: cid}, accountRevision: a.Revision, uid: uid, cookie: cookie}
	user := remote.users[r.EmbyID]
	modes := []string{"baseline", "reuse", "reuse-again", "cached"}
	if targetFirst {
		modes = []string{"target-first", "target-mapped"}
	}
	for _, mode := range modes {
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
			resolveCtx := WithPlaybackSHA1Hint(grant.ResolutionContext(requestCtx), material.SHA1)
			if targetFirst {
				resolveCtx = WithRedirect115SHA1(resolveCtx, material.SHA1)
			}
			if len(play115) > 0 && play115[0] {
				resolveCtx = WithPlay115Source(resolveCtx, Play115Source{FileID: copy.File.ID, SHA1: material.SHA1, Size: material.Size, Name: material.Name})
			}
			_, report, err = cache.ResolvePickCodeWithReport(resolveCtx, selected, "canary-local-material", "", p115.PlaybackUserAgent, "canary-origin")
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
		wantDirectory, wantLink, wantSearch := 0, 1, 0
		if mode == "target-first" {
			wantSearch = 1
		}
		if mode == "baseline" {
			wantDirectory = 1
		}
		if mode == "cached" {
			wantDirectory, wantLink = 1, 0
		}
		if counts["/files"] != wantDirectory || counts["/app/chrome/downurl"] != wantLink || counts["/files/shasearch"] != wantSearch || counts["blocked"] != 0 {
			t.Fatal("live canary request budget violated", counts)
		}
		t.Logf("%s elapsed=%dms requests=%v primary=0 writes=0", mode, time.Since(started).Milliseconds(), counts)
	}
}

type reuseCanaryTransport struct {
	next                http.RoundTripper
	cookie, cid, fileID string
	hash                string
	allowUploadInfo     bool
	mu                  sync.Mutex
	counts              map[string]int
}

func (g *reuseCanaryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	allowed := r.URL.Scheme == "https" && r.Header.Get("Cookie") == g.cookie && (r.URL.Host == "proapi.115.com" && r.Method == http.MethodPost && r.URL.Path == "/app/chrome/downurl" ||
		r.URL.Host == "webapi.115.com" && r.Method == http.MethodGet && (g.allowUploadInfo && r.URL.Path == "/files/uploadinfo" || r.URL.Path == "/files" && r.URL.Query().Get("cid") == g.cid && r.URL.Query().Get("limit") == "1" || r.URL.Path == "/files/get_info" && r.URL.Query().Get("file_id") == g.fileID || g.hash != "" && r.URL.Path == "/files/shasearch" && r.URL.Query().Get("sha1") == g.hash))
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

// Fresh preparation caches for each comparison, shared HTTP connections and
// alternating order. Only directory and upload-credential reads are allowed;
// no initupload, source reads or changes to live application state occur.
func runWritePreparationLiveComparison(t *testing.T, ctx context.Context, guard *reuseCanaryTransport, accountID, cid, cookie string) {
	t.Helper()
	for round := 1; round <= 3; round++ {
		modes := []string{"serial", "parallel"}
		if round%2 == 0 {
			modes = []string{"parallel", "serial"}
		}
		for _, mode := range modes {
			client := p115.NewShareClient()
			client.HTTP.Transport = guard
			client.ReadRetryDelays = nil
			validate := func(ctx context.Context) error {
				return client.ValidateMediaPlaybackDirectory(ctx, accountID, cid, cookie)
			}
			guard.reset()
			started := time.Now()
			var err error
			if mode == "parallel" {
				_, err = preparePlaybackWrite(ctx, client, accountID, cookie, validate)
			} else {
				err = validate(ctx)
				if err == nil {
					_, err = client.PreparePlaybackAccount(ctx, accountID, cookie)
				}
			}
			counts := guard.snapshot()
			if err != nil {
				t.Fatal("live preflight failed (details withheld)")
			}
			if len(counts) != 2 || counts["/files"] != 1 || counts["/files/uploadinfo"] != 1 {
				t.Fatal("unexpected preflight requests", counts)
			}
			t.Logf("round=%d mode=%s elapsed=%dms requests=%v primary=0 writes=0", round, mode, time.Since(started).Milliseconds(), counts)
			guard.reset()
			started = time.Now()
			if _, err := preparePlaybackWrite(ctx, client, accountID, cookie, validate); err != nil {
				t.Fatal("live cached preflight failed (details withheld)")
			}
			counts = guard.snapshot()
			if len(counts) != 1 || counts["/files"] != 1 {
				t.Fatal("cached upload ticket made extra requests", counts)
			}
			t.Logf("round=%d after=%s cached-ticket elapsed=%dms requests=%v primary=0 writes=0", round, mode, time.Since(started).Milliseconds(), counts)
		}
	}
}
