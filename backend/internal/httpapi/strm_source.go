package httpapi

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"strm115/backend/internal/strm"
)

var strmShareCode = regexp.MustCompile(`^[a-zA-Z0-9]{1,128}$`)
var strmReceiveCode = regexp.MustCompile(`^[a-zA-Z0-9]{1,32}$`)
var strmFileID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var strmSharePath = regexp.MustCompile(`^/s/([a-zA-Z0-9]{1,128})_([a-zA-Z0-9]{1,32})_([1-9][0-9]{0,19})(?:\.[a-zA-Z0-9]{1,16})?$`)

// parseSTRMURL only extracts a supported share identity. The URL is never fetched.
// HTTP origins do not affect recognition; only the path and share parameters do.
func (a *ShareAPI) parseSTRMURL(raw string) (*sharePlaybackParams, error) {
	if len(raw) > 16384 {
		return nil, fmt.Errorf("STRM 地址过长")
	}
	u, err := url.Parse(strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")))
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawPath != "" || strings.ContainsAny(u.Host, "\\\r\n\t ") {
		return nil, errNotShareSTRM
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errNotShareSTRM
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, errNotShareSTRM
	}
	var claim *sharePlaybackParams
	switch {
	case u.Path == "/api/":
		value, parseErr := strm.ParseShareQuery(u.RawQuery)
		if parseErr != nil {
			return nil, parseErr
		}
		claim = &sharePlaybackParams{Code: value.Code, Password: value.Password, FileID: value.FileID, Name: value.FileID}
	case strings.HasPrefix(u.Path, strm.SharePrefix):
		if u.RawQuery != "" || u.ForceQuery {
			return nil, fmt.Errorf("STRM 分享地址不允许查询参数")
		}
		value, parseErr := strm.ParseSharePath(u.Path)
		if parseErr != nil {
			return nil, parseErr
		}
		claim = &sharePlaybackParams{Code: value.Code, SHA1: value.SHA1, Password: value.Password, FileID: value.FileID, Name: value.FileID}
	case strings.HasPrefix(u.Path, "/s/"):
		parts := strmSharePath.FindStringSubmatch(u.Path)
		if parts == nil {
			return nil, fmt.Errorf("STRM 分享路径格式无效，须为 /s/分享码_提取码_文件ID，可附扩展名")
		}
		// Everything after '?' is an optional filename hint, not share parameters.
		claim = &sharePlaybackParams{Code: parts[1], Password: parts[2], FileID: parts[3], Name: parts[3]}
	default:
		return nil, errNotShareSTRM
	}
	if !strmShareCode.MatchString(claim.Code) || !strmReceiveCode.MatchString(claim.Password) || !strmFileID.MatchString(claim.FileID) {
		return nil, fmt.Errorf("STRM 分享码、提取码或文件 ID 格式无效")
	}
	if _, err := strconv.ParseUint(claim.FileID, 10, 64); err != nil {
		return nil, fmt.Errorf("STRM 文件 ID 超出范围")
	}
	return claim, nil
}
