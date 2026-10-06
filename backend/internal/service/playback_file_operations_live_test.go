package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"strm115/backend/internal/client/p115"
)

// Explicitly authorized live mutations, confined to a newly created directory
// and tiny generated subtitle. Cleanup recycles only the generated root.
func TestPlaybackFileOperationsLive(t *testing.T) {
	root, uid := os.Getenv("CINEMA115_REUSE_CANARY_ROOT"), os.Getenv("CINEMA115_PROBE_UID")
	if os.Getenv("CINEMA115_FILE_OPERATIONS_LIVE") != "1" || root == "" || uid == "" {
		t.Skip("explicit live mutation opt-in required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	env, err := godotenv.Read(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal("secret unavailable")
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(root, "data", "cinema115.db"))+"?mode=ro")
	if err != nil {
		t.Fatal("database unavailable")
	}
	defer db.Close()
	var id string
	var sealed []byte
	if err := db.QueryRowContext(ctx, `SELECT id,cookie_enc FROM p115_accounts WHERE uid=? AND ownership<>'member' AND owner_user_id='' AND is_primary=0 LIMIT 1`, uid).Scan(&id, &sealed); err != nil {
		t.Fatal("managed account unavailable")
	}
	key := sha256.Sum256([]byte("cinema115-sqlite-p115\x00" + env["JWT_SECRET"]))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal("cipher unavailable")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		t.Fatal("credential invalid")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(id))
	if err != nil {
		t.Fatal("credential unavailable")
	}
	cookie := string(plain)
	if p115.CookieUserID(cookie) != uid {
		t.Fatal("credential UID mismatch")
	}
	c := p115.NewShareClient()
	c.ReadRetryDelays = nil
	label := "Cinema115-live-file-test-" + time.Now().UTC().Format("20060102T150405.000000000")
	data := []byte("1\n00:00:00,000 --> 00:00:01,000\n" + label + "\n")
	h := sha1.Sum(data)
	hash := strings.ToUpper(hex.EncodeToString(h[:]))
	classify := func(err error) string {
		if err == nil {
			return "ok"
		}
		var api *p115.DriveAPIError
		if errors.As(err, &api) {
			return api.Error()
		}
		return p115.SharePlaybackFailureReason(err)
	}
	testRoot, err := c.CreateDirectory(ctx, "0", label, cookie)
	if err != nil {
		t.Fatal("create isolated root:", classify(err))
	}
	t.Logf("created test root=%s name=%s size=%d SHA1=%s", testRoot, label, len(data), hash)
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := c.DeleteDirectory(cleanupCtx, testRoot, cookie); err != nil {
			t.Errorf("cleanup root=%s failed: %s", testRoot, classify(err))
		} else {
			t.Logf("cleanup: recycled test root=%s; recycle bin not emptied", testRoot)
		}
	}()
	makeDir := func(name string) string {
		cid, err := c.CreateDirectory(ctx, testRoot, name, cookie)
		if err != nil {
			t.Fatal("create child:", classify(err))
		}
		return cid
	}
	aDir, bDir, cDir := makeDir("A-source"), makeDir("B-moved"), makeDir("C-copy")
	// Every mutating target below is derived from this run's new directory or
	// verified unique payload. Never take mutation IDs from persistent copies.
	upload := func() p115.DrivePlaybackFile {
		if err := c.UploadSubtitle(ctx, aDir, "probe.srt", data, cookie); err != nil {
			t.Fatal("sample upload:", classify(err))
		}
		f, err := c.FindPlaybackFileBySHA1Quick(ctx, hash, cookie)
		if err != nil || f == nil || f.CID != aDir || f.Size != int64(len(data)) {
			t.Fatal("uploaded sample not uniquely verified:", classify(err))
		}
		return *f
	}
	fetch := func(f p115.DrivePlaybackFile) string {
		link, err := c.VerifiedPlaybackURL(ctx, f, cookie, p115.PlaybackUserAgent)
		if err != nil {
			return classify(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
		if err != nil {
			t.Fatal("CDN request invalid")
		}
		req.Header.Set("User-Agent", p115.PlaybackUserAgent)
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal("CDN read failed (details withheld)")
		}
		defer resp.Body.Close()
		got, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil || resp.StatusCode != 200 || string(got) != string(data) {
			t.Fatalf("sample content mismatch HTTP=%d", resp.StatusCode)
		}
		return "ok; CDN bytes match"
	}
	probe := func(stage string, original p115.DrivePlaybackFile) {
		t.Logf("%s original ID=%s CID=%s fresh-link=%s exact=%s", stage, original.ID, original.CID, fetch(original), classify(c.VerifyPlaybackFile(ctx, original, cookie)))
		quick, err := c.FindPlaybackCandidateBySHA1(ctx, hash, cookie)
		t.Logf("%s SHA1 quick found=%t error=%s", stage, quick != nil, classify(err))
		if err == nil && quick != nil {
			t.Logf("%s quick ID=%s CID=%s sameID=%t samePickCode=%t link=%s", stage, quick.ID, quick.CID, quick.ID == original.ID, quick.PickCode == original.PickCode, fetch(*quick))
		}
		full, err := c.FindPlaybackFileBySHA1(ctx, hash, cookie)
		t.Logf("%s SHA1 ordinary found=%t error=%s", stage, full != nil, classify(err))
		if err == nil && full != nil {
			t.Logf("%s ordinary ID=%s CID=%s sameID=%t samePickCode=%t link=%s", stage, full.ID, full.CID, full.ID == original.ID, full.PickCode == original.PickCode, fetch(*full))
		}
	}
	a := upload()
	probe("baseline", a)
	if err := c.DeleteEntries(ctx, []string{a.ID}, cookie); err != nil {
		t.Fatal("recycle A:", classify(err))
	}
	probe("A-recycled", a)
	b := upload()
	if err := c.MoveEntries(ctx, []string{b.ID}, bDir, cookie); err != nil {
		t.Fatal("move B:", classify(err))
	}
	probe("B-moved", b)
	// Copy API is isolated here; no automatic retry of a write.
	form := url.Values{"fid[0]": {b.ID}, "pid": {cDir}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://webapi.115.com/files/copy", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal("copy request invalid")
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", p115.PlaybackUserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		t.Fatal("copy result uncertain; not retried")
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	var outcome struct {
		State bool `json:"state"`
	}
	if readErr != nil || json.Unmarshal(body, &outcome) != nil || !outcome.State {
		t.Fatalf("copy unconfirmed HTTP=%d; not retried", resp.StatusCode)
	}
	page, err := c.DriveFiles(ctx, cDir, 0, cookie)
	if err != nil || len(page.Files) != 1 || page.Files[0].Directory || page.Files[0].Size != int64(len(data)) || page.Files[0].ID == b.ID {
		t.Fatal("copy not independently confirmed")
	}
	copyID := page.Files[0].ID
	t.Logf("C-copy confirmed sourceID=%s copyID=%s targetCID=%s", b.ID, copyID, cDir)
	if err := c.DeleteEntries(ctx, []string{b.ID}, cookie); err != nil {
		t.Fatal("recycle C source:", classify(err))
	}
	probe("C-copy-source-recycled", b)
	t.Log(fmt.Sprintf("completed A/B/C using same SHA1; recycled originals=%s,%s; copy=%s", a.ID, b.ID, copyID))
}
