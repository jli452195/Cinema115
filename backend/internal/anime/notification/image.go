package notification

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"strm115/backend/internal/client/imagecache"
)

var ErrImage = errors.New("通知图片配置错误")
var gardenLocalCover = regexp.MustCompile(`^/api/garden/image/([1-9][0-9]{0,7})/poster\.jpeg$`)
var publicHostLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var alternateIPv4Tail = regexp.MustCompile(`^(?:[0-9]+|0x[0-9a-f]+)$`)

var reservedImageNetworks = func() []netip.Prefix {
	values := []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"}
	out := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err == nil {
			out = append(out, prefix)
		}
	}
	return out
}()

// 仅校验并交给 Telegram 获取，不在后端请求用户指定的图片地址。
func ValidateImageURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || parsed == nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf("%w：请填写公开可访问且不含登录凭据的 HTTP 或 HTTPS 图片地址", ErrImage)
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("%w：图片地址端口无效", ErrImage)
		}
	}
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.Zone() != "" {
			return fmt.Errorf("%w：图片地址不能使用本机或内网 IP", ErrImage)
		}
		for _, network := range reservedImageNetworks {
			if network.Contains(address) {
				return fmt.Errorf("%w：图片地址不能使用保留 IP", ErrImage)
			}
		}
		return nil
	}
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || len(host) > 253 {
		return fmt.Errorf("%w：请使用公开的图片域名", ErrImage)
	}
	// 浏览器会把缩写、八进制或十六进制主机规范化为 IPv4，不能按普通域名放行。
	if alternateIPv4Tail.MatchString(host[strings.LastIndexByte(host, '.')+1:]) {
		return fmt.Errorf("%w：请使用标准的公开 IP 或图片域名", ErrImage)
	}
	for _, suffix := range []string{"localhost", ".localhost", ".local", ".internal", ".lan", ".home", ".test", ".invalid"} {
		if host == suffix || strings.HasSuffix(host, suffix) {
			return fmt.Errorf("%w：图片地址不能使用本机或内网域名", ErrImage)
		}
	}
	for _, label := range strings.Split(host, ".") {
		if !publicHostLabel.MatchString(label) {
			return fmt.Errorf("%w：图片地址域名格式不正确", ErrImage)
		}
	}
	return nil
}

func NormalizeImage(mode, imageURL string) (string, string, error) {
	mode, imageURL = strings.TrimSpace(mode), strings.TrimSpace(imageURL)
	if mode == "" {
		mode = "cover"
	}
	if mode != "none" && mode != "cover" && mode != "custom" {
		return "", "", fmt.Errorf("%w：请选择不附图、动漫封面或自定义图片", ErrImage)
	}
	if mode != "custom" {
		return mode, "", nil
	}
	if mode == "custom" && imageURL == "" {
		return "", "", fmt.Errorf("%w：自定义图片需要填写公开图片地址", ErrImage)
	}
	if imageURL != "" {
		if err := ValidateImageURL(imageURL); err != nil {
			return "", "", err
		}
	}
	return mode, imageURL, nil
}

// 将已验证的缓存协议还原成固定官方图片源，避免向 Telegram 提交本机地址。
func PublicCoverURL(raw string) string {
	if canonical := imagecache.BangumiCoverURL(raw); canonical != "" {
		raw = canonical
	} else if canonical := imagecache.MikanCoverURL(raw); canonical != "" {
		raw = canonical
	} else if canonical := imagecache.AnibtCoverURL(raw); canonical != "" {
		raw = canonical
	}
	if tail, ok := strings.CutPrefix(raw, "/api/bangumi/image/"); ok {
		parts := strings.Split(tail, "/")
		if len(parts) == 2 {
			remote := "https://lain.bgm.tv/pic/cover/" + strings.ReplaceAll(parts[0], "-", "/") + "/" + parts[1]
			if imagecache.BangumiCoverURL(remote) == raw {
				return remote
			}
		}
	}
	if tail, ok := strings.CutPrefix(raw, "/api/mikan/image/"); ok {
		remote := "https://mikanani.me/images/Bangumi/" + tail
		if imagecache.MikanCoverURL(remote) == raw {
			return remote
		}
	}
	if tail, ok := strings.CutPrefix(raw, "/api/anibt/image/"); ok {
		parts := strings.Split(tail, "/")
		if len(parts) == 2 {
			remote := "https://r2.anibt.net/anime_covers/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".webp") + "/v4/w360.webp"
			if imagecache.AnibtCoverURL(remote) == raw {
				return remote
			}
		}
	}
	if parts := gardenLocalCover.FindStringSubmatch(raw); parts != nil {
		return "https://bgm.animes.garden/bangumi/subject/" + parts[1] + "/poster.jpeg"
	}
	return ""
}

func PhotoURL(mode, imageURL, coverURL string) string {
	switch mode {
	case "", "cover":
		return PublicCoverURL(coverURL)
	case "custom":
		if ValidateImageURL(imageURL) == nil {
			return imageURL
		}
	}
	return ""
}
