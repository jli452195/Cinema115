package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestPoolFaultMatrix(t *testing.T) {
	cases := []struct {
		err                 error
		code, scope, health string
	}{
		{p115.ErrShareLoginRequired, "invalid_credentials", "credential", "invalid_credentials"},
		{p115.ErrPlaybackDirectory, "invalid_directory", "credential", "invalid_directory"},
		{&p115.HTTPError{Status: 429}, "http_429", "global", "throttled"},
		{p115.ErrShareThrottled, "uid_throttled", "uid", "throttled"},
		{&p115.HTTPError{Status: 503}, "upstream_5xx", "credential", "cooldown"},
		{context.DeadlineExceeded, "network", "credential", "cooldown"},
		{context.Canceled, "canceled", "request", ""},
		{p115.ErrRateQueueFull, "queue_full", "request", ""},
		{ErrAccountChanged, "configuration_changed", "request", ""},
		{p115.ErrDriveFileMissing, "file_missing", "file", ""},
		{p115.ErrDriveLinkRestricted, "file_restricted", "file", ""},
		{p115.ErrShareNotReadable, "file_restricted", "file", ""},
		{&p115.DriveAPIError{Code: 999999, Cause: p115.ErrDriveRejected}, "file_rejected", "file", ""},
		{errors.Join(p115.ErrInstantUncertain, p115.ErrShareLoginRequired), "write_uncertain", "file", ""},
		{errors.New("unknown upstream business error"), "unknown", "request", ""},
	}
	for _, tc := range cases {
		for _, origin := range []PoolFaultOrigin{PoolFaultTarget, PoolFaultProbe, PoolFaultSource, PoolFaultDonor, PoolFaultEmby} {
			t.Run(string(origin)+"/"+tc.code, func(t *testing.T) {
				d := ClassifyPoolFault(origin, fmt.Errorf("wrapped: %w", tc.err))
				health := tc.health
				if origin != PoolFaultProbe && origin != PoolFaultTarget {
					health = ""
				}
				if d.Code != tc.code || d.Scope != tc.scope || d.Health != health {
					t.Fatal(d)
				}
			})
		}
		if tc.health == "" {
			p, _ := poolTest(t)
			a := poolTestAccount("a", "7")
			if err := p.report(context.Background(), a, nil); err != nil {
				t.Fatal(err)
			}
			before := p.doc.Health[a.ID]
			calls := 0
			_, err := p.Acquire(context.Background(), "request", "user", []P115Account{a, poolTestAccount("b", "8")}, func(context.Context, P115Account) error { calls++; return tc.err })
			if err == nil || calls != 1 || p.doc.Health[a.ID] != before || len(p.doc.Leases) != 0 {
				t.Fatalf("%s: unsafe retry, health change or leaked reservation", tc.code)
			}
		}
	}
}
