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

const DrivePrefix = "/api/drive115/"

var drivePickCode = regexp.MustCompile(`^[a-zA-Z0-9]{6,64}$`)
var driveSHA1 = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

// Drive is a complete source-file declaration, not proof of cloud ownership.
// Direct playback verifies it in the primary account. Emby proxy playback also
// authorizes the user and verifies the selected playback target.
type Drive struct {
	PickCode string
	FileID   string
	SHA1     string
	Size     int64
	Name     string
}

// ParseDrivePath accepts an escaped URL path. Only the last segment is decoded,
// once; encoded separators or identity fields are never accepted.
func ParseDrivePath(path string) (Drive, error) {
	if len(path) > 16384 || !strings.HasPrefix(path, DrivePrefix) || strings.ContainsAny(path, "?#") {
		return Drive{}, fmt.Errorf("目录 STRM 路径格式无效")
	}
	parts := strings.Split(strings.TrimPrefix(path, DrivePrefix), "/")
	if len(parts) != 5 || !drivePickCode.MatchString(parts[0]) || !fileID.MatchString(parts[1]) || !driveSHA1.MatchString(parts[2]) || !fileID.MatchString(parts[3]) {
		return Drive{}, fmt.Errorf("目录 STRM 须包含 PickCode、文件 ID、40 位 SHA1、字节大小及文件名")
	}
	if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
		return Drive{}, fmt.Errorf("目录 STRM 文件 ID 超出范围: %w", err)
	}
	size, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || size <= 0 {
		return Drive{}, fmt.Errorf("目录 STRM 文件大小无效")
	}
	name, err := url.PathUnescape(parts[4])
	if err != nil || !utf8.ValidString(name) || strings.TrimSpace(name) == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return Drive{}, fmt.Errorf("目录 STRM 文件名无效")
	}
	return Drive{PickCode: parts[0], FileID: parts[1], SHA1: strings.ToUpper(parts[2]), Size: size, Name: name}, nil
}

func DrivePath(value Drive) (string, error) {
	path := DrivePrefix + value.PickCode + "/" + value.FileID + "/" + strings.ToUpper(value.SHA1) + "/" + strconv.FormatInt(value.Size, 10) + "/" + url.PathEscape(value.Name)
	if _, err := ParseDrivePath(path); err != nil {
		return "", err
	}
	return path, nil
}

// DriveURL builds a source URL usable by a player or the authorized Emby proxy.
// The URL never selects a playback UID or contains account credentials.
func DriveURL(baseURL string, value Drive) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") {
		return "", fmt.Errorf("目录 STRM 服务地址应为 HTTP/HTTPS 域名或 IP 地址，不含路径、账号、查询参数或锚点")
	}
	path, err := DrivePath(value)
	if err != nil {
		return "", err
	}
	return base + path, nil
}
