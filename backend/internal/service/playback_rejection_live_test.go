package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"strm115/backend/internal/client/p115"
)

// Explicit, read-only diagnosis of the user-specified stale file. No database
// writes, uploads, directory operations, recycling, or CDN downloads.
func TestPlaybackRejectionLiveProbe(t *testing.T) {
	root, fileID, uid, hash := os.Getenv("CINEMA115_REUSE_CANARY_ROOT"), os.Getenv("CINEMA115_PROBE_FILE_ID"), os.Getenv("CINEMA115_PROBE_UID"), os.Getenv("CINEMA115_PROBE_SHA1")
	if root == "" || fileID == "" || uid == "" || !validMaterialHash(hash) {
		t.Skip("explicit probe parameters required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	env, err := godotenv.Read(filepath.Join(root, ".env"))
	if err != nil || env["JWT_SECRET"] == "" {
		t.Fatal("secret unavailable")
	}
	db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join(root, "data", "cinema115.db"))+"?mode=ro")
	if err != nil {
		t.Fatal("read-only database unavailable")
	}
	defer db.Close()
	var accountID string
	var sealed []byte
	err = db.QueryRowContext(ctx, `SELECT id,cookie_enc FROM p115_accounts WHERE uid=? AND ownership<>'member' AND owner_user_id='' AND is_primary=0 LIMIT 1`, uid).Scan(&accountID, &sealed)
	if err != nil {
		t.Fatal("managed target unavailable")
	}
	key := sha256.Sum256([]byte("cinema115-sqlite-p115\x00" + env["JWT_SECRET"]))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal("cipher unavailable")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		t.Fatal("invalid credential")
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(accountID))
	if err != nil {
		t.Fatal("credential unavailable")
	}
	cookie := string(plain)
	if p115.CookieUserID(cookie) != uid {
		t.Fatal("UID mismatch")
	}
	client := p115.NewShareClient()
	client.ReadRetryDelays = nil
	guard := &reuseCanaryTransport{next: client.HTTP.Transport, cookie: cookie, fileID: fileID, hash: hash, counts: make(map[string]int)}
	client.HTTP.Transport = guard
	var rawCopy string
	var old PlaybackCacheEntry
	err = db.QueryRowContext(ctx, `SELECT record FROM playback_copies WHERE json_extract(record,'$.uid')=? AND json_extract(record,'$.file.id')=? LIMIT 1`, uid, fileID).Scan(&rawCopy)
	if err == nil {
		if json.Unmarshal([]byte(rawCopy), &old) != nil {
			t.Fatal("invalid recorded copy")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("copy lookup failed")
	}
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
	if old.File.PickCode != "" {
		_, err = client.VerifiedPlaybackURL(ctx, old.File, cookie, p115.PlaybackUserAgent)
		t.Logf("old recorded file download: %s", classify(err))
	} else {
		t.Log("old copy no longer present in local records; skip old PickCode download")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://webapi.115.com/files/get_info?file_id="+fileID, nil)
	if err != nil {
		t.Fatal("request invalid")
	}
	req.Header.Set("Cookie", cookie)
	resp, err := client.HTTP.Do(req)
	if err != nil {
		t.Fatal("exact read failed (details withheld)")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		t.Fatal("exact body unavailable")
	}
	var result struct {
		State json.RawMessage `json:"state"`
		Code  json.RawMessage `json:"code"`
		Errno json.RawMessage `json:"errno"`
		Data  []struct {
			ID   json.Number `json:"fid"`
			Area json.Number `json:"aid"`
			CID  json.Number `json:"cid"`
			PC   string      `json:"pc"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &result) != nil {
		t.Fatalf("exact response not recognized HTTP=%d", resp.StatusCode)
	}
	t.Logf("exact read HTTP=%d state=%s code=%s errno=%s rows=%d", resp.StatusCode, result.State, result.Code, result.Errno, len(result.Data))
	for _, v := range result.Data {
		t.Logf("exact record fileID=%s area=%s cid=%s pickcodePresent=%t", v.ID, v.Area, v.CID, v.PC != "")
	}
	current, err := client.FindPlaybackCandidateBySHA1(ctx, hash, cookie)
	t.Logf("target SHA1 query: %s found=%t", classify(err), current != nil)
	if err == nil && current != nil {
		guard.fileID = current.ID
		_, err = client.VerifiedPlaybackURL(ctx, *current, cookie, p115.PlaybackUserAgent)
		t.Logf("current fileID=%s sameAsOld=%t cid=%s download=%s", current.ID, current.ID == fileID, current.CID, classify(err))
	}
	if guard.snapshot()["blocked"] != 0 {
		t.Fatal("probe exceeded read-only scope")
	}
	t.Logf("requests=%v writes=0", guard.snapshot())
}
