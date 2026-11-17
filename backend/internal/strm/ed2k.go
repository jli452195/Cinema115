package strm

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const ED2KPrefix = "/api/ed2k-strm/"

var ed2kHash = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)

// ED2K 只描述稳定的资源与内容身份，不携带账号或可变的云端文件位置。
type ED2K struct {
	Hash string
	SHA1 string
	Size int64
	Name string
}

// ParseED2KPath 只解码末尾文件名一次，解析过程不查询文件或触发离线。
func ParseED2KPath(path string) (ED2K, error) {
	if len(path) > 16384 || !strings.HasPrefix(path, ED2KPrefix) || strings.ContainsAny(path, "?#") {
		return ED2K{}, fmt.Errorf("ED2K STRM 路径格式无效")
	}
	parts := strings.Split(strings.TrimPrefix(path, ED2KPrefix), "/")
	if len(parts) != 4 || !ed2kHash.MatchString(parts[0]) || !driveSHA1.MatchString(parts[1]) || !fileID.MatchString(parts[2]) {
		return ED2K{}, fmt.Errorf("ED2K STRM 须包含 32 位 ED2K 哈希、40 位 SHA1、字节大小和文件名")
	}
	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || size <= 0 {
		return ED2K{}, fmt.Errorf("ED2K STRM 文件大小无效")
	}
	name, err := url.PathUnescape(parts[3])
	if err != nil || !utf8.ValidString(name) || strings.TrimSpace(name) == "" || name == "." || name == ".." || len([]rune(name)) > 255 || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return ED2K{}, fmt.Errorf("ED2K STRM 文件名无效")
	}
	return ED2K{Hash: strings.ToLower(parts[0]), SHA1: strings.ToUpper(parts[1]), Size: size, Name: name}, nil
}

func ED2KPath(value ED2K) (string, error) {
	path := ED2KPrefix + strings.ToLower(value.Hash) + "/" + strings.ToUpper(value.SHA1) + "/" + strconv.FormatInt(value.Size, 10) + "/" + url.PathEscape(value.Name)
	if _, err := ParseED2KPath(path); err != nil {
		return "", err
	}
	return path, nil
}

// ED2KURL 生成不依赖缓存文件 ID、PickCode 和资源数据库编号的播放地址。
func ED2KURL(baseURL string, value ED2K) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") {
		return "", fmt.Errorf("ED2K STRM 服务地址应为 HTTP/HTTPS 域名或 IP 地址，不含路径、账号、查询参数或锚点")
	}
	path, err := ED2KPath(value)
	if err != nil {
		return "", err
	}
	return base + path, nil
}
