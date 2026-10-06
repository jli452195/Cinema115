package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/service"
)

func pickCodeSHA1Context(ctx context.Context, claim *sharePlaybackParams) context.Context {
	if claim.PickCode != "" && claim.FileID != "" {
		return service.WithPlay115Source(ctx, service.Play115Source{FileID: claim.FileID, SHA1: claim.SHA1, Size: claim.Size, Name: claim.Name})
	}
	// Only redirect115 has a PickCode without the play115 file ID/size fields.
	if claim.PickCode != "" && claim.FileID == "" {
		return service.WithRedirect115SHA1(ctx, claim.SHA1)
	}
	return service.WithPlaybackSHA1Hint(ctx, claim.SHA1)
}

var pickCodePattern = regexp.MustCompile("^[a-zA-Z0-9]{6,64}$")
var pickCodePathSHA1Pattern = regexp.MustCompile("^[a-fA-F0-9]{40}$")
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
	claim, ok := parsePickCodeSTRMClaim(raw)
	if !ok {
		return "", false
	}
	return claim.PickCode, true
}

// Path metadata is external input. Complete play115 declarations may be used
// directly for upload; parsing alone does not verify cloud file ownership.
func parsePickCodeSTRMClaim(raw string) (*sharePlaybackParams, bool) {
	if len(raw) > 16384 {
		return nil, false
	}
	u, err := url.Parse(strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery {
		return nil, false
	}
	path := u.EscapedPath()
	if strings.Contains(raw, "#") {
		// Some STRM generators leave filename '#' characters unescaped. Only
		// recover them in the final filename slot; never change source identity
		// fields or fetch this external URL. Normal field validation still applies.
		parts := strings.Split(path, "/")
		filenameSlot := strings.HasPrefix(path, "/play115/") && len(parts) == 7
		if strings.HasPrefix(path, "/api/redirect115/") {
			filenameSlot = len(parts) == 6 && pickCodePathSHA1Pattern.MatchString(parts[4]) ||
				len(parts) == 5 && !pickCodePathSHA1Pattern.MatchString(parts[4])
		}
		if !filenameSlot || strings.Contains(u.Fragment, "?") {
			return nil, false
		}
		path += "%23" + url.PathEscape(u.Fragment)
	}
	if strings.HasPrefix(path, "/play115/") {
		return parsePlay115Path(strings.TrimPrefix(path, "/play115/"))
	}
	const prefix = "/api/redirect115/"
	if !strings.HasPrefix(path, prefix) {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) > 3 || !pickCodePattern.MatchString(parts[0]) {
		return nil, false
	}
	// Some generators include a SHA1 before the optional filename.
	if len(parts) == 3 && !pickCodePathSHA1Pattern.MatchString(parts[1]) {
		return nil, false
	}
	claim := &sharePlaybackParams{PickCode: parts[0]}
	if len(parts) >= 2 {
		name, ok := pickCodeFilename(parts[len(parts)-1])
		if !ok {
			return nil, false
		}
		if pickCodePathSHA1Pattern.MatchString(parts[1]) {
			claim.SHA1 = strings.ToUpper(parts[1])
			if len(parts) == 3 {
				claim.Name = name
			}
		} else {
			claim.Name = name
		}
	}
	return claim, true
}

func parsePlay115Path(path string) (*sharePlaybackParams, bool) {
	parts := strings.Split(path, "/")
	if len(parts) != 5 || !pickCodePattern.MatchString(parts[0]) || !strmFileID.MatchString(parts[1]) || !pickCodePathSHA1Pattern.MatchString(parts[2]) || !strmFileID.MatchString(parts[3]) {
		return nil, false
	}
	if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
		return nil, false
	}
	size, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil || size <= 0 {
		return nil, false
	}
	name, ok := pickCodeFilename(parts[4])
	if !ok || strings.TrimSpace(name) == "" {
		return nil, false
	}
	return &sharePlaybackParams{PickCode: parts[0], FileID: parts[1], SHA1: strings.ToUpper(parts[2]), Size: size, Name: name}, true
}

func pickCodeFilename(raw string) (string, bool) {
	name, err := url.PathUnescape(raw)
	if err != nil || !utf8.ValidString(name) || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", false
	}
	return name, true
}
