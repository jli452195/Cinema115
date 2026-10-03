package service

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"strm115/backend/internal/model"
)

var ErrUserPlaybackStopped = errors.New("此播放会话已结束，请重新发起播放")
var ErrUserPlaybackBusy = errors.New("播放会话管理暂忙，请稍后重试")

const userPlaybackIdleTTL = 5 * time.Minute

type userPlaybackStore interface {
	UserPlayback(context.Context) (model.UserPlaybackDocument, error)
	SaveUserPlayback(context.Context, model.UserPlaybackDocument) error
}
type UserPlayback struct {
	devices       map[string]bool
	deviceChanged chan struct{}
	deviceWaiters int
	mu            sync.Mutex
	store         userPlaybackStore
	doc           model.UserPlaybackDocument
	refs          map[uint64]int
	now           func() time.Time
}

func NewUserPlayback(ctx context.Context, store userPlaybackStore) (*UserPlayback, error) {
	d, err := store.UserPlayback(ctx)
	if err != nil {
		return nil, err
	}
	d.DevicePolicy, err = normalizeDevicePolicy(d.DevicePolicy)
	if err != nil {
		return nil, err
	}
	if d.Leases == nil {
		d.Leases = make(map[string]model.UserPlaybackLease)
	}
	if d.Stopped == nil {
		d.Stopped = make(map[string]int64)
	}
	return &UserPlayback{store: store, doc: d, refs: make(map[uint64]int), devices: make(map[string]bool), deviceChanged: make(chan struct{}), now: time.Now}, nil
}
func (p *UserPlayback) clone() model.UserPlaybackDocument {
	d := p.doc
	d.Leases = maps.Clone(d.Leases)
	d.Stopped = maps.Clone(d.Stopped)
	return d
}
func (p *UserPlayback) save(ctx context.Context, d model.UserPlaybackDocument) error {
	if err := p.store.SaveUserPlayback(ctx, d); err != nil {
		return err
	}
	p.doc = d
	return nil
}
func (p *UserPlayback) prune(d *model.UserPlaybackDocument) {
	now := p.now().UnixMilli()
	for k, l := range d.Leases {
		if l.ExpiresAt <= now {
			delete(d.Leases, k)
		}
	}
	for k, t := range d.Stopped {
		if t <= now {
			delete(d.Stopped, k)
		}
	}
}

type UserPlaybackLease struct {
	policyRevision uint64
	policyMode     string
	handoff        *model.UserPlaybackLease
	handoffReason  string
	pool           *UserPlayback
	Record         model.UserPlaybackLease
	closed         bool
}

// A user can own multiple playback sessions. Media-source variants within one
// client session share its lifecycle; each pool target keeps its own UID lease.
func userPlaybackKey(r model.MemberPlayback, req PoolRequest) string {
	return DirectLinkKey("user-playback", r.ServerID, r.EmbyID, req.Session, req.Item)
}

func (p *UserPlayback) Acquire(ctx context.Context, r model.MemberPlayback, req PoolRequest) (*UserPlaybackLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Session == "" || req.Item == "" {
		return nil, ErrLogin
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	p.prune(&d)
	key := userPlaybackKey(r, req)
	session := DirectLinkKey(req.Session, req.Item)
	event := DirectLinkKey(r.ServerID, r.UserID, req.Session, req.Item)
	if d.Stopped[event] > p.now().UnixMilli() {
		return nil, ErrUserPlaybackStopped
	}
	if err := checkDeviceAdmission(d, model.UserPlaybackLease{Key: key, ServerID: r.ServerID, UserID: r.UserID, Device: req.Device}, p.now()); err != nil {
		return nil, err
	}
	if old, ok := d.Leases[key]; ok {
		if d.DevicePolicy.Mode == DevicePolicyHeartbeat && old.Device != req.Device {
			return nil, ErrPlaybackDeviceIdentity
		}
		if p.refs[old.Generation] >= 128 {
			return nil, ErrUserPlaybackBusy
		}
		p.refs[old.Generation]++
		return &UserPlaybackLease{pool: p, Record: old, policyRevision: d.PolicyRevision, policyMode: d.DevicePolicy.Mode, handoffReason: "同会话复用，不新增用户播放记录"}, nil
	}
	if len(d.Leases) >= 4096 || len(p.refs) >= 4096 || len(d.Stopped)+len(d.Leases) >= 8192 {
		return nil, ErrUserPlaybackBusy
	}
	d.Generation++
	l := model.UserPlaybackLease{Key: key, UserID: r.UserID, ServerID: r.ServerID, SessionDigest: session, EventKey: event, Item: req.Item, Generation: d.Generation, UpdatedAt: p.now().UnixMilli(), ExpiresAt: p.now().Add(95 * time.Second).UnixMilli()}
	l.Device, l.PlaybackSource = req.Device, r.Source
	l.AuthSession = req.Session
	var handoff *model.UserPlaybackLease
	reason := "缺少可信设备信息或播放会话 ID，按独立会话处理"
	if req.Device != "" && req.PlaySession != "" && !req.Download && !req.Task {
		// Preparations cannot be predecessors. Multiple issued sessions remain
		// ambiguous; never guess which one the client intended to replace.
		var candidate model.UserPlaybackLease
		matches := 0
		for _, old := range d.Leases {
			if old.Issued && old.Device == req.Device && old.ServerID == r.ServerID && old.UserID == r.UserID {
				candidate = old
				matches++
			}
		}
		if matches == 1 && candidate.PlaybackSource == r.Source && p.refs[candidate.Generation] == 0 && handoffIdle(candidate, p.now()) {
			handoff = &candidate
			reason = "唯一旧会话满足静默条件，准备接替；成功提交前保留旧播放"
		} else if matches == 0 {
			reason = "此设备无旧会话，建立独立播放"
		} else if matches > 1 {
			reason = "此设备有多个候选，不猜测接替目标"
		} else {
			reason = "旧会话仍活跃、准备中或来源不同，按独立会话处理"
		}
	}
	if req.Client != "" {
		l.EventFallback = DirectLinkKey("playback-event", r.ServerID, r.UserID, req.Client, req.Item)
		if d.Stopped["fallback:"+l.EventFallback] > p.now().UnixMilli() {
			l.EventFallback = ""
		}
	}
	if req.PlaySession != "" {
		l.ObservationKey = DirectLinkKey("pool-observation", r.ServerID, r.EmbyID, req.PlaySession, req.Item)
	}
	d.Leases[key] = l
	if err := p.save(ctx, d); err != nil {
		return nil, err
	}
	p.refs[l.Generation]++
	return &UserPlaybackLease{pool: p, Record: l, policyRevision: d.PolicyRevision, policyMode: d.DevicePolicy.Mode, handoff: handoff, handoffReason: reason}, nil
}
func (l *UserPlaybackLease) Current() bool {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.doc.Leases[l.Record.Key]
	return ok && v.Generation == l.Record.Generation && v.ExpiresAt > p.now().UnixMilli() && l.policyRevision == p.doc.PolicyRevision
}
func (l *UserPlaybackLease) Commit(ctx context.Context) error {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	v, ok := d.Leases[l.Record.Key]
	if !ok || v.Generation != l.Record.Generation || v.ExpiresAt <= p.now().UnixMilli() {
		return ErrUserPlaybackStopped
	}
	// Pool retirements require MediaAuthorization's atomic commit path.
	if d.DevicePolicy.Mode == DevicePolicyHeartbeat {
		return ErrUserPlaybackBusy
	}
	if l.policyRevision != d.PolicyRevision {
		return ErrUserPlaybackStopped
	}
	if v.FirstIssuedAt == 0 {
		v.FirstIssuedAt = p.now().UnixMilli()
	}
	v.Issued = true
	v.IssuedAt = p.now().UnixMilli()
	v.UpdatedAt = p.now().UnixMilli()
	v.ExpiresAt = p.now().Add(userPlaybackIdleTTL).UnixMilli()
	d.Leases[v.Key] = v
	return p.save(ctx, d)
}
func (l *UserPlaybackLease) Close() error {
	if l == nil || l.closed {
		return nil
	}
	l.closed = true
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refs[l.Record.Generation]--
	if p.refs[l.Record.Generation] > 0 {
		return nil
	}
	delete(p.refs, l.Record.Generation)
	v, ok := p.doc.Leases[l.Record.Key]
	if !ok || v.Generation != l.Record.Generation || v.Issued {
		return nil
	}
	d := p.clone()
	delete(d.Leases, v.Key)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return p.save(ctx, d)
}

// Capture before forwarding an event to Emby. A delayed response must not
// release a replay which reused the same client PlaySessionId.
func (p *UserPlayback) EventGeneration(event string) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range p.doc.Leases {
		if l.EventKey == event && l.ExpiresAt > p.now().UnixMilli() {
			return l.Generation
		}
	}
	return 0
}

func (p *UserPlayback) Event(ctx context.Context, event string, stopped bool, generation uint64) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	p.prune(&d)
	changed := len(d.Leases) != len(p.doc.Leases) || len(d.Stopped) != len(p.doc.Stopped)
	matched := false
	for key, l := range d.Leases {
		if l.EventKey != event || l.Generation != generation {
			continue
		}
		matched = true
		if stopped {
			changed = true
			delete(d.Leases, key)
			// Normal stops allow immediate replay with the same client session ID.
			// Removed generations still fence already admitted requests.
		} else {
			changed = true
			l.CandidateUntil = 0
			l.LastPlaybackAt = p.now().UnixMilli()
			if l.Issued && (p.now().UnixMilli()-l.UpdatedAt >= 60000 || l.ExpiresAt < p.now().Add(userPlaybackIdleTTL-time.Minute).UnixMilli()) {
				l.UpdatedAt = p.now().UnixMilli()
				l.ExpiresAt = p.now().Add(userPlaybackIdleTTL).UnixMilli()
			}
			d.Leases[key] = l
		}
	}
	if !changed {
		return matched, nil
	}
	return matched, p.save(ctx, d)
}

func (p *UserPlayback) Reconcile(ctx context.Context, keys map[string]bool) error {
	return p.reconcilePlayback(ctx, keys, nil)
}

func (p *UserPlayback) reconcilePlayback(ctx context.Context, keys map[string]bool, playback map[string]int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	p.prune(&d)
	changed := len(d.Leases) != len(p.doc.Leases) || len(d.Stopped) != len(p.doc.Stopped)
	for k, l := range d.Leases {
		if !l.Issued || l.ObservationKey == "" || !keys[l.ObservationKey] {
			continue
		}
		// LastActivityDate includes browsing. Only an actual playback check-in
		// can refresh evidence used by device handoff eligibility.
		evidence := playback[l.ObservationKey]
		if evidence < l.IssuedAt || !freshPlaybackEvidence(evidence, p.now()) {
			continue
		}
		if evidence > l.LastPlaybackAt && evidence <= p.now().UnixMilli() {
			l.CandidateUntil = 0
			l.LastPlaybackAt = evidence
			changed = true
		}
		if evidence-l.UpdatedAt >= 60000 || l.ExpiresAt < time.UnixMilli(evidence).Add(userPlaybackIdleTTL-time.Minute).UnixMilli() {
			l.UpdatedAt = evidence
			l.ExpiresAt = time.UnixMilli(evidence).Add(userPlaybackIdleTTL).UnixMilli()
			changed = true
		}
		d.Leases[k] = l
	}
	if !changed {
		return nil
	}
	return p.save(ctx, d)
}
func (p *UserPlayback) List() []model.UserPlaybackLease {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]model.UserPlaybackLease, 0, len(p.doc.Leases))
	for _, l := range p.doc.Leases {
		if l.ExpiresAt > p.now().UnixMilli() {
			l.SessionDigest = ""
			l.EventKey = ""
			l.ObservationKey = ""
			l.EventFallback = ""
			l.Device = ""
			l.AuthSession = ""
			out = append(out, l)
		}
	}
	return out
}
func (p *UserPlayback) Release(ctx context.Context, key string, generation uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	p.prune(&d)
	l, ok := d.Leases[key]
	if !ok {
		return nil
	}
	if l.Generation != generation {
		return ErrAccountChanged
	}
	delete(d.Leases, key)
	d.Stopped[l.EventKey] = p.now().Add(24 * time.Hour).UnixMilli()
	return p.save(ctx, d)
}

// One holder covers all sequential target attempts of a single HTTP redirect.
type userPlaybackRequestKey struct{}
type UserPlaybackRequest struct {
	lease      *UserPlaybackLease
	devicePool *UserPlayback
	deviceKey  string
}

func WithUserPlaybackRequest(ctx context.Context) (context.Context, *UserPlaybackRequest) {
	r := &UserPlaybackRequest{}
	return context.WithValue(ctx, userPlaybackRequestKey{}, r), r
}
func (r *UserPlaybackRequest) Close() error {
	err := r.lease.Close()
	if r.devicePool != nil {
		r.devicePool.unlockDevice(r.deviceKey)
		r.devicePool = nil
	}
	return err
}
