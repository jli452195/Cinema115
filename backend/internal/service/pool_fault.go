package service

import (
	"context"
	"errors"
	"net"

	"strm115/backend/internal/client/p115"
)

// Fault origin is assigned by the caller, never inferred from upstream text.
type PoolFaultOrigin string

const (
	PoolFaultProbe  PoolFaultOrigin = "probe"
	PoolFaultTarget PoolFaultOrigin = "target"
	PoolFaultSource PoolFaultOrigin = "source"
	PoolFaultDonor  PoolFaultOrigin = "donor"
	PoolFaultEmby   PoolFaultOrigin = "emby"
)

type PoolFaultDecision struct {
	Code   string          `json:"code"`
	Origin PoolFaultOrigin `json:"origin"`
	Scope  string          `json:"scope"`
	Health string          `json:"health,omitempty"`
}

func ClassifyPoolFault(origin PoolFaultOrigin, err error) PoolFaultDecision {
	d := PoolFaultDecision{Code: "unknown", Origin: origin, Scope: "request"}
	var httpErr *p115.HTTPError
	var network net.Error
	switch {
	case err == nil:
		d.Code = "ok"
	case errors.Is(err, p115.ErrInstantUncertain):
		d.Code, d.Scope = "write_uncertain", "file"
	case errors.Is(err, context.Canceled):
		d.Code = "canceled"
	case errors.Is(err, p115.ErrRateQueueFull):
		d.Code = "queue_full"
	case errors.Is(err, ErrAccountChanged):
		d.Code = "configuration_changed"
	case errors.Is(err, p115.ErrShareLoginRequired):
		d.Code, d.Scope, d.Health = "invalid_credentials", "credential", "invalid_credentials"
	case errors.Is(err, p115.ErrPlaybackDirectory):
		d.Code, d.Scope, d.Health = "invalid_directory", "credential", "invalid_directory"
	case errors.As(err, &httpErr) && httpErr.Status == 429:
		d.Code, d.Scope, d.Health = "http_429", "global", "throttled"
	case errors.Is(err, p115.ErrShareThrottled):
		d.Code, d.Scope, d.Health = "uid_throttled", "uid", "throttled"
	case errors.Is(err, p115.ErrDriveFileMissing):
		d.Code, d.Scope = "file_missing", "file"
	case errors.Is(err, p115.ErrDriveLinkRestricted), errors.Is(err, p115.ErrShareNotReadable), errors.Is(err, p115.ErrShareUnderReview), errors.Is(err, p115.ErrSharePassword):
		d.Code, d.Scope = "file_restricted", "file"
	case errors.Is(err, p115.ErrDriveRejected), errors.Is(err, p115.ErrDriveInvalidResponse), errors.Is(err, p115.ErrShareDownloadRejected):
		d.Code, d.Scope = "file_rejected", "file"
	case errors.As(err, &httpErr) && httpErr.Status >= 500 && httpErr.Status <= 599:
		d.Code, d.Scope, d.Health = "upstream_5xx", "credential", "cooldown"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &network):
		d.Code, d.Scope, d.Health = "network", "credential", "cooldown"
	}
	if origin != PoolFaultProbe && origin != PoolFaultTarget {
		d.Health = ""
	}
	return d
}
