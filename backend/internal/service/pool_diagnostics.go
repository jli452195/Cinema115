package service

import (
	"context"
	"errors"
	"sync"
)

type poolDiagnosticKey struct{}
type PoolCandidateDiagnostic struct {
	UIDSummary string `json:"uidSummary,omitempty"`
	Stage      string `json:"stage"`
	Reason     string `json:"reason"`
	Generation uint64 `json:"generation,omitempty"`
}
type PoolDiagnostic struct {
	mu      sync.Mutex
	events  []PoolCandidateDiagnostic
	dropped int
	outcome string
}
type PoolDiagnosticSnapshot struct {
	Events  []PoolCandidateDiagnostic `json:"events"`
	Dropped int                       `json:"dropped"`
	Outcome string                    `json:"outcome,omitempty"`
}

func WithPoolDiagnostic(ctx context.Context) (context.Context, *PoolDiagnostic) {
	d := &PoolDiagnostic{events: make([]PoolCandidateDiagnostic, 0, 32)}
	return context.WithValue(ctx, poolDiagnosticKey{}, d), d
}
func (d *PoolDiagnostic) Snapshot() PoolDiagnosticSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return PoolDiagnosticSnapshot{Events: append([]PoolCandidateDiagnostic(nil), d.events...), Dropped: d.dropped, Outcome: d.outcome}
}
func recordPoolCandidate(ctx context.Context, uid, stage, reason string, generation uint64) {
	d, _ := ctx.Value(poolDiagnosticKey{}).(*PoolDiagnostic)
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.events) >= 32 {
		d.dropped++
		return
	}
	summary := ""
	if uid != "" {
		summary = DirectLinkKey("pool-uid-log", uid)[:12]
	}
	d.events = append(d.events, PoolCandidateDiagnostic{UIDSummary: summary, Stage: stage, Reason: reason, Generation: generation})
}

// Only terminal admission errors set the outcome; retries remain diagnostics.
func ObservePoolAdmission(ctx context.Context, err error) {
	d, _ := ctx.Value(poolDiagnosticKey{}).(*PoolDiagnostic)
	if d == nil {
		return
	}
	var admission *PoolAdmissionError
	outcome := ""
	if errors.Is(err, ErrPlaybackDeviceConflict) {
		outcome = "device_conflict"
	}
	if errors.As(err, &admission) && admission.Reason == PoolCapacityFull {
		outcome = "uid_capacity_full"
	}
	if outcome != "" {
		d.mu.Lock()
		d.outcome = outcome
		d.mu.Unlock()
	}
}

func (p *DynamicPool) ObserveAdmissionOutcome(outcome string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch outcome {
	case "device_conflict":
		p.metrics.DeviceConflicts++
	case "uid_capacity_full":
		p.metrics.CapacityDenials++
	}
}
