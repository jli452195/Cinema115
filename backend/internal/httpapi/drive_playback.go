package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
	"strm115/backend/internal/strm"
)

type drivePlaybackClient interface {
	VerifiedPlaybackURL(context.Context, p115.DrivePlaybackFile, string, string) (string, error)
}

// Directory STRM URLs work independently of the Emby proxy. The source is
// always the explicit primary account; playback pools and share settings do not apply.
func RegisterDrivePlayback(r *gin.Engine, accounts *service.P115Accounts, client drivePlaybackClient, mediaHTTP *http.Client, logs *service.GenerationLogs) {
	var cache service.DirectLinkCache
	play := func(c *gin.Context) {
		entry := strmPlaybackLog{started: time.Now(), phase: "地址解析"}
		defer entry.write(c, logs, "目录播放")
		c.Header("Cache-Control", "no-store")
		fail := func(status int, message string) {
			entry.failure = message
			if c.Request.Method == http.MethodHead {
				c.Status(status)
				return
			}
			c.JSON(status, gin.H{"error": message})
		}
		u := c.Request.URL
		if u.RawQuery != "" || u.ForceQuery {
			fail(http.StatusBadRequest, "目录 STRM 地址不允许查询参数")
			return
		}
		file, err := strm.ParseDrivePath(u.EscapedPath())
		if err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		entry.phase = "主账号检查"
		account, err := accounts.Primary("")
		if err != nil {
			fail(http.StatusServiceUnavailable, err.Error())
			return
		}
		key := service.DirectLinkKey(account.ID, strconv.FormatUint(account.Revision, 10), account.Cookie, file.PickCode, file.FileID, file.SHA1, strconv.FormatInt(file.Size, 10))
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()
		entry.phase = "直链核验"
		value, hit, _, err := cache.Get(ctx, key, 5*time.Minute, service.DirectLinkCacheEntry{}, func(ctx context.Context) (service.DirectLinkValue, error) {
			link, err := client.VerifiedPlaybackURL(ctx, p115.DrivePlaybackFile{ID: file.FileID, PickCode: file.PickCode, SHA1: file.SHA1, Size: file.Size, Name: file.Name}, account.Cookie, p115.PlaybackUserAgent)
			if err != nil {
				return service.DirectLinkValue{}, err
			}
			if !p115.AllowedDirectPlaybackURL(link) {
				return service.DirectLinkValue{}, p115.ErrDriveLinkRestricted
			}
			return service.DirectLinkValue{URL: link}, nil
		})
		cancel()
		entry.cacheHit = &hit
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			} else if errors.Is(err, p115.ErrDriveFileMissing) {
				status = http.StatusNotFound
			}
			fail(status, "115 文件取链失败："+p115.SharePlaybackFailureReason(err))
			return
		}
		entry.phase = "原文件传输"
		if err := streamMedia(c, mediaHTTP, value.URL, file.Name, func() { entry.write(c, logs, "目录播放") }); err != nil {
			if c.Request.Context().Err() != nil {
				entry.phase = "客户端结束读取"
				return
			}
			entry.failure = "读取 115 原文件失败"
			if !c.Writer.Written() {
				cache.Remove(key)
				fail(http.StatusBadGateway, "读取 115 原文件失败，请重试")
			}
		}
	}
	r.GET(strm.DrivePrefix+"*path", play)
	r.HEAD(strm.DrivePrefix+"*path", play)
}
