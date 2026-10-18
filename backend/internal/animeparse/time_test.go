package animeparse

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestPublishedKnownTimeAndPrecision(t *testing.T) {
	observed := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw, want, zone string
		precision       int64
	}{
		{"2020-11-04T13:19:24.888", "2020-11-04T05:19:24.888Z", "Asia/Shanghai", 1},
		{"2026-07-24T17:43:53.432835", "2026-07-24T09:43:53.432Z", "Asia/Shanghai", 1},
		{"2026-07-24T17:43:53.4", "2026-07-24T09:43:53.4Z", "Asia/Shanghai", 100},
		{"2026-07-24T17:43:53.43", "2026-07-24T09:43:53.43Z", "Asia/Shanghai", 10},
		{"2026/10/18 12:00", "2026-10-18T04:00:00Z", "Asia/Shanghai", 60000},
		{"2026-10-18 12:00", "2026-10-18T04:00:00Z", "Asia/Shanghai", 60000},
		{"2026/10/18 12:00:59", "2026-10-18T04:00:59Z", "Asia/Shanghai", 1000},
		{"2026-10-18T12:00:00+09:00", "2026-10-18T03:00:00Z", "explicit", 1000},
		{"2026-10-18T12:00:00-04:00", "2026-10-18T16:00:00Z", "explicit", 1000},
		{"2026-10-18T12:00:00+0800", "2026-10-18T04:00:00Z", "explicit", 1000},
		{"2026-10-18T11:59:59Z", "2026-10-18T11:59:59Z", "explicit", 1000},
		{"Sun, 18 Oct 2026 11:59:59 +0000", "2026-10-18T11:59:59Z", "explicit", 1000},
		{"Sun, 18 Oct 2026 11:59:59 GMT", "2026-10-18T11:59:59Z", "explicit", 1000},
		{"Sun, 18 Oct 2026 11:59:59 UTC", "2026-10-18T11:59:59Z", "explicit", 1000},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := Published(tc.raw, "", observed)
			if err != nil {
				t.Fatal(err)
			}
			want, err := time.Parse(time.RFC3339Nano, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "known" || got.UnixMillis == nil || *got.UnixMillis != want.UnixMilli() || got.PrecisionMillis != tc.precision || got.Zone != tc.zone || got.Version != TimeVersion || got.Raw != tc.raw {
				t.Fatalf("unexpected publication: %+v", got)
			}
		})
	}
}

func TestPublishedUncertainTimeNeverBecomesObservationTime(t *testing.T) {
	observed := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ raw, alt, status string }{
		{"", "", "missing"}, {"  ", "", "missing"},
		{"invalid", "", "unparseable"}, {"2026-02-30T12:00:00", "", "unparseable"},
		{"2026-10-18", "", "unparseable"},
		{"2026-10-18T12:00:00+24:00", "", "unparseable"},
		{"2026-10-18T12:00:00+08:60", "", "unparseable"},
		{"2026-10-18T12:00:00.1234567890Z", "", "unparseable"},
		{"Sun, 18 Oct 2026 11:59:59 CST", "", "unparseable"},
		{"2026-10-18T12:05:00.001Z", "", "future"},
		{"2027-01-01T00:00:00Z", "", "future"},
		{"1960-01-01T00:00:00Z", "", "invalid"},
		{"2026-10-18T11:59:59Z", "2026-10-18T11:59:58Z", "conflict"},
		{"2026-10-18T11:59:59.001Z", "2026-10-18T11:59:59.002Z", "conflict"},
		{"2026-10-18T11:59:59Z", "broken", "unparseable"},
		{"broken", "2026-10-18T11:59:59Z", "unparseable"},
	} {
		t.Run(tc.raw+tc.alt, func(t *testing.T) {
			got, err := Published(tc.raw, tc.alt, observed)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.status || got.UnixMillis != nil || got.Raw != tc.raw || got.AlternateRaw != tc.alt || got.PrecisionMillis != 0 {
				t.Fatalf("unexpected publication: %+v", got)
			}
		})
	}
}

func TestPublishedCorroborationKeepsPrimaryPrecision(t *testing.T) {
	observed := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	raw, alt := "2020-11-04T13:19:24.888", "2020/11/04 13:19"
	got, err := Published(raw, alt, observed)
	if err != nil || got.Status != "known" || got.PrecisionMillis != 1 || got.Raw != raw || got.AlternateRaw != alt {
		t.Fatalf("corroboration changed primary: %+v %v", got, err)
	}
	other, err := Published(raw, "Wed, 04 Nov 2020 05:19:24 +0000", observed)
	if err != nil || other.Status != "known" || other.UnixMillis == nil || *other.UnixMillis != *got.UnixMillis {
		t.Fatalf("offset corroboration failed: %+v %v", other, err)
	}
	conflict, err := Published(raw, "2020/11/04 13:20", observed)
	if err != nil || conflict.Status != "conflict" || conflict.UnixMillis != nil {
		t.Fatalf("minute conflict ignored: %+v %v", conflict, err)
	}
	boundary, err := Published("2026-10-18T12:05:00Z", "", observed)
	if err != nil || boundary.Status != "known" {
		t.Fatalf("exact future allowance rejected: %+v %v", boundary, err)
	}
	blank, err := Published("", raw, observed)
	if err != nil || blank.Status != "known" || blank.Raw != "" || blank.AlternateRaw != raw {
		t.Fatalf("missing primary fallback: %+v %v", blank, err)
	}
}

func TestPublishedHostTimezoneIndependent(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("unrelated", -11*3600)
	t.Cleanup(func() { time.Local = oldLocal })
	got, err := Published("2026/10/18 12:00", "", time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	if err != nil || got.UnixMillis == nil || *got.UnixMillis != time.Date(2026, 10, 18, 4, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("host timezone used: %+v %v", got, err)
	}
}

func TestPublishedInputLimits(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw, alt string
		at       time.Time
	}{
		{"", "", time.Time{}}, {"", "", time.UnixMilli(-1)},
		{strings.Repeat("a", 2049), "", now}, {"", strings.Repeat("a", 2049), now},
		{"bad\x00", "", now}, {"", string([]byte{0xff}), now},
	} {
		if _, err := Published(tc.raw, tc.alt, tc.at); !errors.Is(err, ErrInput) {
			t.Fatalf("invalid publication input accepted: %v", err)
		}
	}
}
