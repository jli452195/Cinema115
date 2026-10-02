package service

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"strm115/backend/internal/model"
)

var ErrUserPlaybackConflict = errors.New("该用户已在别处播放,请先停止当前播放后再重试")
var ErrUserPlaybackStopped = errors.New("此播放会话已结束，请重新发起播放")
var ErrUserPlaybackBusy = errors.New("播放会话管理暂忙，请稍后重试")

type userPlaybackStore interface {
	UserPlayback(context.Context) (model.UserPlaybackDocument, error)
	SaveUserPlayback(context.Context, model.UserPlaybackDocument) error
}
type UserPlayback struct {
	mu    sync.Mutex
	store userPlaybackStore
	doc   model.UserPlaybackDocument
	refs  map[uint64]int
	now   func() time.Time
}

func NewUserPlayback(ctx context.Context, store userPlaybackStore) (*UserPlayback, error) {
	d, err := store.UserPlayback(ctx)
	if err != nil {
		return nil, err
	}
	if d.Leases == nil {
		d.Leases = make(map[string]model.UserPlaybackLease)
	}
	if d.Stopped == nil {
		d.Stopped = make(map[string]int64)
	}
	return &UserPlayback{store: store, doc: d, refs: make(map[uint64]int), now: time.Now}, nil
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
	pool   *UserPlayback
	Record model.UserPlaybackLease
	closed bool
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
	key := DirectLinkKey("user-playback", r.ServerID, r.EmbyID)
	session := DirectLinkKey(req.Session, req.Item, req.Source)
	event := DirectLinkKey(r.ServerID, r.UserID, req.Session, req.Item)
	if d.Stopped[event] > p.now().UnixMilli() {
		return nil, ErrUserPlaybackStopped
	}
	if old, ok := d.Leases[key]; ok {
		if old.SessionDigest != session {
			return nil, ErrUserPlaybackConflict
		}
		if p.refs[old.Generation] >= 128 {
			return nil, ErrUserPlaybackBusy
		}
		p.refs[old.Generation]++
		return &UserPlaybackLease{pool: p, Record: old}, nil
	}
	if len(d.Leases) >= 4096 || len(p.refs) >= 4096 || len(d.Stopped)+len(d.Leases) >= 8192 {
		return nil, ErrUserPlaybackBusy
	}
	d.Generation++
	l := model.UserPlaybackLease{Key: key, UserID: r.UserID, ServerID: r.ServerID, SessionDigest: session, EventKey: event, Item: req.Item, Generation: d.Generation, UpdatedAt: p.now().UnixMilli(), ExpiresAt: p.now().Add(95 * time.Second).UnixMilli()}
	if req.PlaySession != "" {
		l.ObservationKey = DirectLinkKey("pool-observation", r.ServerID, r.EmbyID, req.PlaySession, req.Item)
	}
	d.Leases[key] = l
	if err := p.save(ctx, d); err != nil {
		return nil, err
	}
	p.refs[l.Generation]++
	return &UserPlaybackLease{pool: p, Record: l}, nil
}
func (l *UserPlaybackLease) Current() bool {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.doc.Leases[l.Record.Key]
	return ok && v.Generation == l.Record.Generation && v.ExpiresAt > p.now().UnixMilli()
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
	v.Issued = true
	v.UpdatedAt = p.now().UnixMilli()
	v.ExpiresAt = p.now().Add(240 * time.Minute).UnixMilli()
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
		} else if l.Issued && p.now().UnixMilli()-l.UpdatedAt >= 60000 {
			changed = true
			l.UpdatedAt = p.now().UnixMilli()
			l.ExpiresAt = p.now().Add(240 * time.Minute).UnixMilli()
			d.Leases[key] = l
		}
	}
	if !changed {
		return matched, nil
	}
	return matched, p.save(ctx, d)
}

func (p *UserPlayback) Reconcile(ctx context.Context, keys map[string]bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	p.prune(&d)
	changed := len(d.Leases) != len(p.doc.Leases) || len(d.Stopped) != len(p.doc.Stopped)
	for k, l := range d.Leases {
		if !l.Issued || l.ObservationKey == "" || !keys[l.ObservationKey] || p.now().UnixMilli()-l.UpdatedAt < 60000 {
			continue
		}
		l.UpdatedAt = p.now().UnixMilli()
		l.ExpiresAt = p.now().Add(240 * time.Minute).UnixMilli()
		d.Leases[k] = l
		changed = true
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
type UserPlaybackRequest struct{ lease *UserPlaybackLease }

func WithUserPlaybackRequest(ctx context.Context) (context.Context, *UserPlaybackRequest) {
	r := &UserPlaybackRequest{}
	return context.WithValue(ctx, userPlaybackRequestKey{}, r), r
}
func (r *UserPlaybackRequest) Close() error { return r.lease.Close() }
