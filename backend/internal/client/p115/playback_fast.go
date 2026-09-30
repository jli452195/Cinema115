package p115

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
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

const playbackDirectoryTTL = 10 * time.Minute
const playbackRecoveryDirectoryFreshness = 30 * time.Second

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
	return c.validatePlaybackDirectory(ctx, scope, cid, cookie, playbackDirectoryTTL)
}

// Recovery accepts only a recent real directory check. Cache hits do not
// extend freshness; credential, scope and directory changes isolate entries.
func (c *ShareClient) ValidatePlaybackRecoveryDirectory(ctx context.Context, scope, cid, cookie string) (resultErr error) {
	started := time.Now()
	var total RequestTiming
	readCtx := WithRequestTiming(ctx, func(v RequestTiming) {
		reportRequestTiming(ctx, v)
		total.Requests += v.Requests
		total.Wait += v.Wait
		total.HTTP += v.HTTP
		total.Retries += v.Retries
	})
	defer func() {
		stage := "恢复目录核验汇总"
		if resultErr == nil && total.Requests == 0 {
			stage = "恢复目录核验复用（30秒内，无115请求）"
		}
		emitPlaybackTiming(ctx, PlaybackTiming{Stage: stage, Duration: time.Since(started), SearchSummary: true, Requests: total.Requests, Retries: total.Retries, Wait: total.Wait, RequestHTTP: total.HTTP, Failed: resultErr != nil})
	}()
	return c.validatePlaybackDirectory(readCtx, scope, cid, cookie, playbackRecoveryDirectoryFreshness)
}

func (c *ShareClient) validatePlaybackDirectory(ctx context.Context, scope, cid, cookie string, maxAge time.Duration) error {
	if scope == "" || CookieUserID(cookie) == "" || !validDriveID(cid, true) {
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
	if maxAge > 0 && time.Now().Before(expires.Add(maxAge-playbackDirectoryTTL)) {
		return nil
	}
	// Discard a stale success before revalidation, so a failed recovery check
	// cannot leave a longer-lived ordinary-upload cache entry behind.
	c.playbackPreparation.mu.Lock()
	delete(c.playbackPreparation.directories, key)
	c.playbackPreparation.mu.Unlock()
	if err := c.playbackReader(cookie).ValidateCacheParent(ctx, cid, cookie); err != nil {
		return err
	}
	c.playbackPreparation.mu.Lock()
	if c.playbackPreparation.directories == nil || len(c.playbackPreparation.directories) >= 256 {
		c.playbackPreparation.directories = make(map[string]time.Time)
	}
	c.playbackPreparation.directories[key] = time.Now().Add(playbackDirectoryTTL)
	c.playbackPreparation.mu.Unlock()
	return nil
}

func (c *ShareClient) RefreshPlaybackDirectory(ctx context.Context, scope, cid, cookie string) error {
	c.playbackPreparation.mu.Lock()
	delete(c.playbackPreparation.directories, playbackPreparationKey(scope+"\x00"+cid, cookie))
	c.playbackPreparation.mu.Unlock()
	return c.ValidatePlaybackDirectory(ctx, scope, cid, cookie)
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
