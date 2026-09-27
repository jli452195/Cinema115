package p115

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Secrets remain memory-only and bound to one explicit account configuration.
type PlaybackUploadTicket struct {
	scope                string
	uid, key, cookieHash string
	expires              time.Time
}

func (*PlaybackUploadTicket) String() string { return "[115 upload credentials]" }

type playbackPreparationCache struct {
	mu          sync.Mutex
	accounts    map[string]PlaybackUploadTicket
	directories map[string]time.Time
}

// Only returned when no initupload request has been attempted. Callers may
// discard their pending file record; unknown upload outcomes must stay blocked.
type PlaybackPreflightError struct {
	Stage string
	Err   error
}

func (e *PlaybackPreflightError) Error() string {
	return "秒传预检失败（" + e.Stage + "，尚未提交上传）：" + e.Err.Error()
}
func (e *PlaybackPreflightError) Unwrap() error { return e.Err }

func playbackCredentialHash(cookie string) string {
	h := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(h[:])
}
func playbackPreparationKey(scope, cookie string) string {
	return playbackCredentialHash(scope + "\x00" + cookie)
}
func (c *ShareClient) PreparePlaybackAccount(ctx context.Context, scope, cookie string) (*PlaybackUploadTicket, error) {
	if scope == "" || CookieUserID(cookie) == "" {
		return nil, fmt.Errorf("秒传账号配置无效")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := playbackPreparationKey(scope, cookie)
	c.playbackPreparation.mu.Lock()
	for k, v := range c.playbackPreparation.accounts {
		if time.Now().After(v.expires) {
			delete(c.playbackPreparation.accounts, k)
		}
	}
	v, ok := c.playbackPreparation.accounts[key]
	c.playbackPreparation.mu.Unlock()
	if ok {
		return &v, nil
	}
	raw, err := c.instantRequest(ctx, http.MethodGet, c.WebURL+"/files/uploadinfo", cookie, nil)
	if err != nil {
		return nil, err
	}
	var identity struct {
		UserID  json.Number     `json:"user_id"`
		UserKey string          `json:"userkey"`
		State   json.RawMessage `json:"state"`
	}
	if json.Unmarshal(raw, &identity) != nil || (len(identity.State) > 0 && !upstreamOK(identity.State)) || identity.UserID.String() != CookieUserID(cookie) || identity.UserKey == "" || len(identity.UserKey) > 256 {
		return nil, fmt.Errorf("无法确认目标账号上传凭据")
	}
	v = PlaybackUploadTicket{scope: scope, uid: identity.UserID.String(), key: identity.UserKey, cookieHash: playbackCredentialHash(cookie), expires: time.Now().Add(5 * time.Minute)}
	c.playbackPreparation.mu.Lock()
	if c.playbackPreparation.accounts == nil || len(c.playbackPreparation.accounts) >= 128 {
		c.playbackPreparation.accounts = make(map[string]PlaybackUploadTicket)
	}
	c.playbackPreparation.accounts[key] = v
	c.playbackPreparation.mu.Unlock()
	return &v, nil
}

func (c *ShareClient) ValidatePlaybackDirectory(ctx context.Context, scope, cid, cookie string) error {
	if scope == "" || CookieUserID(cookie) == "" {
		return fmt.Errorf("目录校验账号配置无效")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := playbackPreparationKey(scope+"\x00"+cid, cookie)
	c.playbackPreparation.mu.Lock()
	for k, v := range c.playbackPreparation.directories {
		if time.Now().After(v) {
			delete(c.playbackPreparation.directories, k)
		}
	}
	expires := c.playbackPreparation.directories[key]
	c.playbackPreparation.mu.Unlock()
	if time.Now().Before(expires) {
		return nil
	}
	if err := c.ValidateCacheParent(ctx, cid, cookie); err != nil {
		return err
	}
	c.playbackPreparation.mu.Lock()
	if c.playbackPreparation.directories == nil || len(c.playbackPreparation.directories) >= 256 {
		c.playbackPreparation.directories = make(map[string]time.Time)
	}
	c.playbackPreparation.directories[key] = time.Now().Add(time.Minute)
	c.playbackPreparation.mu.Unlock()
	return nil
}

func (c *ShareClient) UploadPlayback(ctx context.Context, info *DownloadInfo, scope, cid, cookie string, ticket *PlaybackUploadTicket, progress func(InstantProgress)) (*InstantUploadResult, error) {
	if !validDriveID(cid, true) || ticket == nil || ticket.scope != scope {
		return nil, &PlaybackPreflightError{Stage: "配置检查", Err: fmt.Errorf("秒传缓存目录或预检凭据无效")}
	}
	if err := c.ValidatePlaybackDirectory(ctx, scope, cid, cookie); err != nil {
		return nil, &PlaybackPreflightError{Stage: "缓存目录校验，目录=" + cid, Err: err}
	}
	submitted := false
	result, err := c.instantUploadPrepared(ctx, info, cid, cookie, func(p InstantProgress) {
		if p.Stage == "init" {
			submitted = true
		}
		if progress != nil {
			progress(p)
		}
	}, false, ticket)
	if err != nil {
		c.playbackPreparation.mu.Lock()
		delete(c.playbackPreparation.accounts, playbackPreparationKey(scope, cookie))
		delete(c.playbackPreparation.directories, playbackPreparationKey(scope+"\x00"+cid, cookie))
		c.playbackPreparation.mu.Unlock()
		if !submitted {
			return nil, &PlaybackPreflightError{Stage: "上传凭据或参数校验", Err: err}
		}
	}
	return result, err
}

// A receipt and matching SHA1 in downurl metadata avoid another search.
// Incomplete metadata falls back to a SHA1 lookup in the selected account.
func (c *ShareClient) CompletePlaybackUpload(ctx context.Context, info *DownloadInfo, cid string, result *InstantUploadResult, cookie, ua string) (DrivePlaybackFile, string, error) {
	if result == nil || !validDriveID(cid, true) || info == nil {
		return DrivePlaybackFile{}, "", fmt.Errorf("秒传结果无效")
	}
	var verifiedLink string
	if validDriveID(result.FileID, false) && instantPickCode.MatchString(result.PickCode) {
		got, id, err := c.pickCodeInfo(ctx, result.PickCode, cookie, ua)
		if err == nil {
			if id != result.FileID || (instantHash.MatchString(got.SHA1) && !strings.EqualFold(got.SHA1, info.SHA1)) {
				return DrivePlaybackFile{}, "", fmt.Errorf("目标取链元数据与秒传文件不一致")
			}
			verifiedLink = got.URL
			if strings.EqualFold(got.SHA1, info.SHA1) {
				return DrivePlaybackFile{ID: id, CID: cid, Name: got.Name, Size: got.Size, SHA1: got.SHA1, PickCode: result.PickCode}, got.URL, nil
			}
		}
	}
	// Search by SHA1 when the upload receipt lacks complete metadata. A matching
	// file is reusable regardless of its name, size, folder or duplicate copies.
	f, err := c.FindPlaybackFileBySHA1(ctx, info.SHA1, cookie)
	if err != nil {
		return DrivePlaybackFile{}, "", err
	}
	if f == nil {
		return DrivePlaybackFile{}, "", fmt.Errorf("秒传后尚未查到同 SHA1 文件")
	}
	if verifiedLink != "" && f.ID == result.FileID && f.PickCode == result.PickCode {
		return *f, verifiedLink, nil
	}
	link, err := c.DriveDownloadURLWithUA(ctx, f.ID, f.PickCode, cookie, ua)
	return *f, link, err
}
