package model

import (
	"math"
	"strings"
)

const AnimeOfflineExact = "web_get_user_task_v1"

// AnimeOfflineStatus is an allowlisted observation, never an upstream body.
// ResultID is an unverified object identifier; it is not a video file ID.
type AnimeOfflineStatus struct {
	SchemaVersion int      `json:"schemaVersion"`
	Source        string   `json:"source"`
	Present       bool     `json:"present"`
	InfoHash      string   `json:"infoHash,omitempty"`
	TargetCID     string   `json:"targetCid,omitempty"`
	ResultID      string   `json:"resultId,omitempty"`
	Status        *int64   `json:"status,omitempty"`
	DisplayStatus string   `json:"displayStatus,omitempty"`
	Progress      *float64 `json:"progress,omitempty"`
	Malformed     bool     `json:"malformed"`
	State         string   `json:"state"`
}

// NormalizedState deliberately has no numeric-only fallback. The exact API's
// status=1 is also observed for completed tasks; list status=2 is incompatible.
func (s AnimeOfflineStatus) NormalizedState() string {
	if s.Source != AnimeOfflineExact || !s.Present || s.Malformed || s.Status == nil {
		return "unknown"
	}
	switch s.DisplayStatus {
	case "failed", "stopped", "cancelled", "canceled":
		return "failed"
	}
	if *s.Status != 1 {
		return "unknown"
	}
	switch s.DisplayStatus {
	case "pending", "waiting", "parsing":
		return "accepted"
	case "running", "downloading":
		return "downloading"
	case "finished", "completed", "success":
		if s.Progress != nil && *s.Progress == 100 {
			return "completed"
		}
	}
	return "unknown"
}

func (s AnimeOfflineStatus) Valid() bool {
	if s.SchemaVersion != 1 || s.Source != AnimeOfflineExact || s.State != s.NormalizedState() {
		return false
	}
	if !s.Present {
		return s.InfoHash == "" && s.TargetCID == "" && s.ResultID == "" && s.Status == nil && s.DisplayStatus == "" && s.Progress == nil && !s.Malformed
	}
	if len(s.InfoHash) != 40 || strings.Trim(s.InfoHash, "0123456789abcdef") != "" || s.TargetCID != "" && !ValidAnimeExternalID(s.TargetCID) || s.ResultID != "" && !ValidAnimeExternalID(s.ResultID) {
		return false
	}
	if s.Status != nil && (*s.Status < math.MinInt32 || *s.Status > math.MaxInt32) || s.Progress != nil && (math.IsNaN(*s.Progress) || math.IsInf(*s.Progress, 0) || *s.Progress < 0 || *s.Progress > 100) {
		return false
	}
	switch s.DisplayStatus {
	case "", "pending", "waiting", "parsing", "running", "downloading", "finished", "completed", "success", "failed", "stopped", "cancelled", "canceled":
		return true
	}
	return false
}

// A failed read updates AttemptedAt/Failures but preserves the last successful
// observation and its actual timestamp. This is stored in raw_status_json for
// schema compatibility; it contains only these typed fields.
type AnimeDownloadTracking struct {
	SchemaVersion int                 `json:"schemaVersion"`
	AttemptedAt   int64               `json:"attemptedAt"`
	ObservedAt    int64               `json:"observedAt"`
	Failures      int                 `json:"failures"`
	Observation   *AnimeOfflineStatus `json:"observation"`
}
