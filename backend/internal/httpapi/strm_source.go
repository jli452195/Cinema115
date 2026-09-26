package httpapi

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var strmShareCode = regexp.MustCompile(`^[a-zA-Z0-9]{1,128}$`)
var strmReceiveCode = regexp.MustCompile(`^[a-zA-Z0-9]{0,32}$`)
var strmFileID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

// parseSTRMURL only extracts a supported share identity. The URL is never fetched.
// Origin matching is optional; required parameter validation is not.
func (a *ShareAPI) parseSTRMURL(raw string, strict bool, origin string) (*sharePlaybackParams, error) {
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
	if strict {
		base, err := url.Parse(origin)
		if err != nil || base.Host == "" || !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
			return nil, fmt.Errorf("%w：严格模式下来源协议、主机或端口不匹配", errNotShareSTRM)
		}
	}
	if u.Path != "/api/" {
		return nil, errNotShareSTRM
	}
	q, parseErr := url.ParseQuery(u.RawQuery)
	if parseErr != nil {
		return nil, fmt.Errorf("STRM 分享参数编码无效")
	}
	claim, err := a.parseQuery(q)
	if err != nil {
		return nil, err
	}
	if !strmShareCode.MatchString(claim.Code) || !strmReceiveCode.MatchString(claim.Password) || !strmFileID.MatchString(claim.FileID) {
		return nil, fmt.Errorf("STRM 分享码、提取码或文件 ID 格式无效")
	}
	if _, err := strconv.ParseUint(claim.FileID, 10, 64); err != nil {
		return nil, fmt.Errorf("STRM 文件 ID 超出范围")
	}
	return claim, nil
}
