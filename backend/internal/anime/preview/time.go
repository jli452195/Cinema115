package preview

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // Keep the source timezone independent of the host OS.
	"unicode"
	"unicode/utf8"
)

const TimeVersion int64 = 1

// Publication retains both source fields. UnixMillis is available only for a
// known, consistent time, never synthesized from first discovery. Precision
// allows later baseline comparisons to respect a source's actual precision.
type Publication struct {
	Raw             string `json:"raw"`
	AlternateRaw    string `json:"alternateRaw"`
	Status          string `json:"status"` // known, missing, unparseable, conflict, invalid, future
	UnixMillis      *int64 `json:"unixMillis"`
	PrecisionMillis int64  `json:"precisionMillis"`
	Zone            string `json:"zone"`
	Version         int64  `json:"version"`
}

var (
	errPublicationInput   = errors.New("invalid anime publication input")
	shanghai, shanghaiErr = time.LoadLocation("Asia/Shanghai")
	dateFraction          = regexp.MustCompile(`:[0-9]{2}[.,]([0-9]+)`)
	dateOffset            = regexp.MustCompile(`[+-]([0-9]{2}):?([0-9]{2})$`)
)

type parsedTime struct {
	time      time.Time
	precision int64
	zone      string
}

// Published interprets the primary date and an optional alternate date from
// the same observation. Coarser dates may corroborate, but never replace, the
// primary date. HTML and RSS reads remain separate observations; callers must
// not overwrite an RSS date with a subsequent HTML minute timestamp.
func Published(raw, alternate string, observedAt time.Time) (Publication, error) {
	out := Publication{Raw: raw, AlternateRaw: alternate, Status: "missing", Version: TimeVersion}
	if !validPublicationText(raw) || !validPublicationText(alternate) || observedAt.IsZero() || observedAt.UnixMilli() <= 0 || observedAt.Year() > 9999 {
		return out, errPublicationInput
	}
	if shanghaiErr != nil {
		return out, fmt.Errorf("load anime publisher timezone: %w", shanghaiErr)
	}
	primary, secondary := strings.TrimSpace(raw), strings.TrimSpace(alternate)
	if primary == "" {
		primary, secondary = secondary, ""
	}
	if primary == "" {
		return out, nil
	}
	p, ok := parseTime(primary)
	if !ok {
		out.Status = "unparseable"
		return out, nil
	}
	if secondary != "" {
		other, valid := parseTime(secondary)
		if !valid {
			out.Status = "unparseable"
			return out, nil
		}
		// Compare half-open intervals at the represented precision. A page
		// minute can corroborate an RSS second/fraction in that same minute.
		a, b := p.time.UnixMilli(), other.time.UnixMilli()
		if a >= b+other.precision || b >= a+p.precision {
			out.Status = "conflict"
			return out, nil
		}
	}
	if p.time.Year() < 1970 || p.time.Year() > 9999 || p.time.UnixMilli() <= 0 {
		out.Status = "invalid"
		return out, nil
	}
	if p.time.After(observedAt.Add(5 * time.Minute)) {
		out.Status = "future"
		return out, nil
	}
	stamp := p.time.UnixMilli()
	out.Status, out.UnixMillis, out.PrecisionMillis, out.Zone = "known", &stamp, p.precision, p.zone
	return out, nil
}

func parseTime(raw string) (parsedTime, bool) {
	if fraction := dateFraction.FindStringSubmatch(raw); fraction != nil && len(fraction[1]) > 9 {
		return parsedTime{}, false
	}
	if offset := dateOffset.FindStringSubmatch(raw); offset != nil && (offset[1] >= "24" || offset[2] >= "60") {
		return parsedTime{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05Z07:00", time.RFC1123Z} {
		if t, err := time.Parse(layout, raw); err == nil {
			return parsedTime{time: t, precision: timePrecision(raw, false), zone: "explicit"}, true
		}
	}
	for _, zone := range []string{"GMT", "UTC"} {
		if strings.HasSuffix(raw, " "+zone) {
			if t, err := time.Parse("Mon, 02 Jan 2006 15:04:05 "+zone, raw); err == nil {
				return parsedTime{time: t, precision: 1000, zone: "explicit"}, true
			}
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006/01/02 15:04:05", "2006/01/02 15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, raw, shanghai); err == nil {
			return parsedTime{time: t, precision: timePrecision(raw, strings.HasSuffix(layout, "15:04")), zone: "Asia/Shanghai"}, true
		}
	}
	return parsedTime{}, false
}

func timePrecision(raw string, minute bool) int64 {
	if minute {
		return 60000
	}
	if m := dateFraction.FindStringSubmatch(raw); m != nil {
		switch len(m[1]) {
		case 1:
			return 100
		case 2:
			return 10
		default:
			return 1
		}
	}
	return 1000
}

func validPublicationText(s string) bool {
	if len(s) > 2048*utf8.UTFMax || !utf8.ValidString(s) || utf8.RuneCountInString(s) > 2048 {
		return false
	}
	return !strings.ContainsFunc(s, unicode.IsControl)
}
