package httpapi

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var pickCodePattern = regexp.MustCompile("^[a-zA-Z0-9]{6,64}$")
var originalStreamPath = regexp.MustCompile("(?i)^/(?:emby/)?videos/([a-zA-Z0-9_-]+)/original\\.[a-zA-Z0-9]{1,16}$")

func originalStreamItem(r *http.Request) string {
	parts := originalStreamPath.FindStringSubmatch(r.URL.Path)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// Recognize data only: the STRM host is never a network destination.
func parsePickCodeSTRM(raw string) (string, bool) {
	if len(raw) > 16384 {
		return "", false
	}
	u, err := url.Parse(strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
		return "", false
	}
	const prefix = "/api/redirect115/"
	if !strings.HasPrefix(u.EscapedPath(), prefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), prefix), "/")
	if len(parts) > 2 || !pickCodePattern.MatchString(parts[0]) {
		return "", false
	}
	if len(parts) == 2 {
		name, err := url.PathUnescape(parts[1])
		if err != nil || name == "." || name == ".." || strings.ContainsAny(name, "/\\\r\n\x00") {
			return "", false
		}
	}
	return parts[0], true
}
