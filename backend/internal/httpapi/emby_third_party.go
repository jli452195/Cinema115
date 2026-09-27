package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

var thirdPartyPickcode = regexp.MustCompile(`^[a-zA-Z0-9]{1,64}$`)
var originalStreamPath = regexp.MustCompile(`(?i)^/(?:emby/)?videos/([a-zA-Z0-9_-]+)/original\.[a-zA-Z0-9]{1,16}$`)

func originalStreamItem(r *http.Request) string {
	parts := originalStreamPath.FindStringSubmatch(r.URL.Path)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// Only this explicitly configured service and its pickcode endpoint may receive requests.
// This recognition is deliberately separate from share STRMs and never interprets a pickcode as a share ID.
func (h *embyProxyHandler) thirdPartyURL(raw string) (*url.URL, bool) {
	if h.config.ThirdPartyURL == "" || len(raw) > 16384 {
		return nil, false
	}
	base, err := url.Parse(h.config.ThirdPartyURL)
	if err != nil {
		return nil, false
	}
	u, err := url.Parse(strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")))
	if err != nil || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery || !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
		return nil, false
	}
	const prefix = "/api/redirect115/"
	// Escapes are allowed only in the optional filename, never in the routing prefix or pickcode.
	if !strings.HasPrefix(u.EscapedPath(), prefix) {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), prefix), "/")
	if len(parts) < 1 || len(parts) > 2 || !thirdPartyPickcode.MatchString(parts[0]) {
		return nil, false
	}
	if len(parts) == 2 {
		name, err := url.PathUnescape(parts[1])
		if err != nil || name == "." || name == ".." || strings.ContainsAny(name, "/\\\r\n\x00") {
			return nil, false
		}
	}
	return u, true
}

func (h *embyProxyHandler) thirdPartySource(source map[string]json.RawMessage) (*url.URL, bool) {
	if h.config.ThirdPartyURL == "" {
		return nil, false
	}
	raw, err := h.sourceURL(source)
	if err != nil {
		return nil, false
	}
	return h.thirdPartyURL(raw)
}

func (h *embyProxyHandler) thirdPartyLink(ctx context.Context, target *url.URL, ua string) (string, error) {
	if strings.TrimSpace(ua) == "" {
		return "", fmt.Errorf("第三方取链需要播放器 User-Agent，请检查客户端请求头")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	current := target
	// No automatic redirects, cookie jar, or inherited player headers. Never fetch the CDN body.
	client := &http.Client{Transport: h.http.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for hop := 0; hop < 3; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return "", fmt.Errorf("无法构造第三方取链请求")
		}
		req.Header.Set("User-Agent", ua)
		req.Header.Set("Accept", "*/*")
		// Limit accidental response bodies if the configured service streams instead of redirecting.
		req.Header.Set("Range", "bytes=0-0")
		response, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("第三方取链请求失败：%s", proxyFailureReason(err))
		}
		location := response.Header.Get("Location")
		response.Body.Close()
		h.logs.Add(ctx, "INFO", fmt.Sprintf("第三方取链响应：服务=%s，HTTP %d，跳转=%d/3", target.Host, response.StatusCode, hop+1))
		switch response.StatusCode {
		case 301, 302, 303, 307, 308:
		default:
			return "", fmt.Errorf("第三方接口返回 HTTP %d，预期为包含 CDN Location 的跳转响应", response.StatusCode)
		}
		if location == "" || len(location) > 16384 {
			return "", fmt.Errorf("第三方接口缺少有效跳转地址")
		}
		next, err := current.Parse(location)
		if err != nil {
			return "", fmt.Errorf("第三方接口返回无效跳转地址")
		}
		if p115.AllowedMediaURL(next.String()) {
			return next.String(), nil
		}
		allowed, ok := h.thirdPartyURL(next.String())
		if !ok {
			return "", fmt.Errorf("第三方接口跳转到非 115 CDN 或未授权地址，已停止取链")
		}
		current = allowed
	}
	return "", fmt.Errorf("第三方取链跳转超过 3 次，可能存在循环")
}

func (h *embyProxyHandler) redirectThirdParty(ctx context.Context, w http.ResponseWriter, r *http.Request, target *url.URL, item, userID, userName, sourceID string) error {
	ua := r.UserAgent()
	if strings.TrimSpace(ua) == "" {
		return fmt.Errorf("第三方取链需要播放器 User-Agent，请检查客户端请求头")
	}
	key := service.DirectLinkKey("third-party", h.config.URL, userID, item, sourceID, h.config.ThirdPartyURL, target.String(), ua)
	headers := http.Header{"User-Agent": {ua}, "Accept": {"*/*"}, "Range": {"bytes=0-0"}}
	view := service.DirectLinkCacheEntry{User: service.PlaybackLogName(userName) + "（" + userID + "）", MediaSource: proxyLogID(sourceID), TraceID: service.ProxyTraceID(ctx), RequestHeaders: proxyDiagnosticHeaders(r.Header, r.Host), LookupHeaders: proxyDiagnosticHeaders(headers, target.Host)}
	value, hit, expires, err := h.directLinks.Get(ctx, key, time.Duration(h.config.DirectLinkCacheMinutes)*time.Minute, view, func(fetchCtx context.Context) (service.DirectLinkValue, error) {
		h.logs.Add(fetchCtx, "INFO", "第三方接口取链：服务="+target.Host+"，使用播放器 UA；115 账号由第三方管理，不使用本项目个人绑定或秒传缓存")
		link, err := h.thirdPartyLink(fetchCtx, target, ua)
		return service.DirectLinkValue{URL: link, Report: service.PlaybackResolution{Mode: "第三方接口取链", AccountLabel: "由第三方管理（" + target.Host + "）"}}, err
	})
	if err != nil {
		return err
	}
	cache := "本次取链"
	if hit {
		cache = "命中"
		h.logs.Add(ctx, "INFO", fmt.Sprintf("第三方直链缓存命中，剩余 %d 秒（有效期不顺延）", max(0, int(time.Until(expires).Seconds()))))
	}
	h.logs.Add(ctx, "INFO", "播放取链结果：用户="+service.PlaybackLogName(userName)+"（ID="+userID+"）｜条目="+proxyLogID(item)+"｜媒体源="+proxyLogID(sourceID)+"｜播放方式=第三方接口取链｜取链服务="+target.Host+"｜115 账号=由第三方管理｜直链缓存="+cache+"｜返回 HTTP 302（不代表客户端已开播）")
	w.Header().Set("Location", value.URL)
	w.WriteHeader(http.StatusFound)
	return nil
}
