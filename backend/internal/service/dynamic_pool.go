package service

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

var ErrPoolUnavailable = errors.New("共享池暂无可用账号，请稍后重试")
var ErrPoolBusy = errors.New("共享池会话已满或请求过多，请稍后重试")
var ErrPoolCooling = fmt.Errorf("%w：账号正在冷却，请稍后重试", ErrPoolUnavailable)
var ErrPoolInvalid = fmt.Errorf("%w：小号登录或目录失效，请管理员检查", ErrPoolUnavailable)
var ErrPoolEmpty = fmt.Errorf("%w：没有完成配置且参与调度的小号", ErrPoolUnavailable)

type poolStore interface {
	PlaybackPool(context.Context) (model.PlaybackPoolDocument, error)
	SavePlaybackPool(context.Context, model.PlaybackPoolDocument) error
}

// DynamicPool coordinates a single active backend. Every change is persisted
// before publishing. Remote probes always execute outside mu.
type DynamicPool struct {
	mu                  sync.Mutex
	store               poolStore
	doc                 model.PlaybackPoolDocument
	busy                map[string]bool
	waiting             int
	changed             chan struct{}
	now                 func() time.Time
	metrics             PoolMetrics
	latencies           []int64
	latencyNext         int
	recent              map[string]poolPreference
	probes              map[string]time.Time
	lastBackgroundProbe time.Time
	lastReconcile       time.Time
}

func NewDynamicPool(ctx context.Context, store poolStore) (*DynamicPool, error) {
	d, err := store.PlaybackPool(ctx)
	if err != nil {
		return nil, err
	}
	if d.Settings.MaxSessions == 0 {
		d.Settings = model.PlaybackPoolSettings{MaxSessions: 3, MaxAttempts: 3, BudgetSeconds: 90, LeaseMinutes: 5, CooldownSeconds: 30, UIDIntervalMS: 250, GlobalIntervalMS: 50}
	}
	if err := validatePoolSettings(d.Settings); err != nil {
		return nil, err
	}
	if d.Leases == nil {
		d.Leases = make(map[string]model.PlaybackPoolLease)
	}
	if d.Health == nil {
		d.Health = make(map[string]model.PlaybackPoolHealth)
	}
	if d.Modes == nil {
		d.Modes = make(map[string]string)
	}
	return &DynamicPool{store: store, doc: d, busy: make(map[string]bool), changed: make(chan struct{}), now: time.Now, recent: make(map[string]poolPreference), probes: make(map[string]time.Time), metrics: PoolMetrics{StartedAt: time.Now().UnixMilli()}}, nil
}

func validatePoolSettings(s model.PlaybackPoolSettings) error {
	if s.UIDIntervalMS < 100 || s.UIDIntervalMS > 10000 || s.GlobalIntervalMS < 10 || s.GlobalIntervalMS > 1000 || s.GlobalIntervalMS > s.UIDIntervalMS {
		return memberError("UID 请求间隔须为 100–10000 毫秒，全局间隔须为 10–1000 毫秒且不大于 UID 间隔")
	}
	if s.MaxSessions < 1 || s.MaxSessions > 20 || s.MaxAttempts < 1 || s.MaxAttempts > 3 || s.BudgetSeconds < 5 || s.BudgetSeconds > 90 || s.LeaseMinutes < 5 || s.LeaseMinutes > 1440 || s.CooldownSeconds < 5 || s.CooldownSeconds > 3600 {
		return memberError("池设置超出范围：会话 1–20，候选 1–3，预算 5–90 秒，租约 5–1440 分钟，冷却 5–3600 秒")
	}
	return nil
}

func (p *DynamicPool) Settings() model.PlaybackPoolSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doc.Settings
}
func (p *DynamicPool) clone() model.PlaybackPoolDocument {
	d := p.doc
	d.Leases, d.Health, d.Modes = maps.Clone(d.Leases), maps.Clone(d.Health), maps.Clone(d.Modes)
	d.Events = append([]model.PlaybackPoolEvent(nil), d.Events...)
	return d
}
func (p *DynamicPool) save(ctx context.Context, d model.PlaybackPoolDocument) error {
	if err := p.store.SavePlaybackPool(ctx, d); err != nil {
		return err
	}
	p.doc = d
	return nil
}
func (p *DynamicPool) Configure(ctx context.Context, s model.PlaybackPoolSettings) error {
	if err := validatePoolSettings(s); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	d.Settings = s
	return p.save(ctx, d)
}
func (p *DynamicPool) SetMode(ctx context.Context, id, mode string) error {
	if mode != "enabled" && mode != "draining" && mode != "disabled" {
		return memberError("池账号状态无效")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	d.Modes[id] = mode
	return p.save(ctx, d)
}

type PoolAccountStatus struct {
	State     string `json:"state"`
	Mode      string `json:"mode"`
	Sessions  int    `json:"sessions"`
	Preparing int    `json:"preparing"`
	Residual  int    `json:"residual"`
	RetryAt   int64  `json:"retryAt"`
	Active    int    `json:"active"`
	Message   string `json:"message"`
	CheckedAt int64  `json:"checkedAt"`
}

func (p *DynamicPool) AccountStatus(a P115Account) PoolAccountStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := PoolAccountStatus{State: "unknown", Mode: p.doc.Modes[a.ID]}
	if v.Mode == "" {
		v.Mode = "enabled"
	}
	if h, ok := p.doc.Health[a.ID]; ok && h.Revision == a.Revision {
		v.State, v.RetryAt = h.State, h.Until
		v.CheckedAt = h.CheckedAt
		if h.Until > 0 && h.Until <= p.now().UnixMilli() {
			v.State = "pending"
			v.RetryAt = 0
		}
	}
	for _, h := range p.doc.Health {
		if h.UID == a.UID && h.State == "throttled" && h.Until > p.now().UnixMilli() {
			v.State = "throttled"
			v.RetryAt = max(v.RetryAt, h.Until)
		}
	}
	if a.Cookie == "" || a.PlaybackParentCID == "" {
		v.State = "unconfigured"
	}
	if p.doc.GlobalUntil > p.now().UnixMilli() {
		v.State = "throttled"
		v.RetryAt = max(v.RetryAt, p.doc.GlobalUntil)
	}
	occupancy := poolUIDOccupancy(p.doc, a.UID, p.now().UnixMilli())
	v.Sessions, v.Active, v.Preparing, v.Residual = occupancy.total, occupancy.active, occupancy.preparing, occupancy.residual
	v.Message = poolHealthMessage(v.State)
	return v
}

// lockSession serializes overlapping requests for one authenticated session.
// Waiters are bounded by the global request limit and their context deadline.
func (p *DynamicPool) lockSession(ctx context.Context, key string) error {
	p.mu.Lock()
	if p.waiting >= 128 {
		err := p.admissionError(p.doc, PoolWaiterLimit, nil)
		p.mu.Unlock()
		return err
	}
	p.waiting++
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.waiting--; p.mu.Unlock() }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		if !p.busy[key] {
			if len(p.busy) >= 128 {
				err := p.admissionError(p.doc, PoolRequestLimit, nil)
				p.mu.Unlock()
				return err
			}
			p.busy[key] = true
			p.mu.Unlock()
			return nil
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}
func (p *DynamicPool) unlockSession(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.busy, key)
	close(p.changed)
	p.changed = make(chan struct{})
}

type PoolLease struct {
	pool              *DynamicPool
	Record            model.PlaybackPoolLease
	Account           P115Account
	closed            bool
	healthSequence    uint64
	skipHealthRefresh bool // A target read proves no write-directory health.
}

func eligiblePoolAccount(a P115Account) bool {
	return !a.Primary && a.Ownership != "member" && a.OwnerUserID == "" && !IsMemberAccount(a.ID) && a.Cookie != "" && a.UID != "" && p115.CookieUserID(a.Cookie) == a.UID && a.PlaybackParentCID != ""
}

func (p *DynamicPool) reserve(ctx context.Context, key, user string, accounts []P115Account, excluded map[string]bool) (*PoolLease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	now := p.now().UnixMilli()
	if d.GlobalUntil > now {
		return nil, ErrPoolCooling
	}
	counts := make(map[string]int, len(accounts))
	counted := make(map[string]bool, len(d.Leases))
	for k, l := range d.Leases {
		if l.ExpiresAt <= now {
			delete(d.Leases, k)
			continue
		}
		if !borrowedPoolSlot(d, l, now) {
			id := DirectLinkKey(l.UID, poolCapacityKey(k, l))
			if !counted[id] {
				counts[l.UID]++
				counted[id] = true
			}
		}
	}
	req, _ := ctx.Value(poolRequestKey{}).(PoolRequest)
	slotKey := requestPoolSlot(req, user)
	slotTarget := poolSlotTarget(d, slotKey, user, now)
	sharesSlot := func(a P115Account) bool { return poolHasSlot(d, slotKey, a.UID, now) }
	preferredSlot := func(a P115Account) bool {
		return slotTarget.Key != "" && slotTarget.AccountID == a.ID && slotTarget.UID == a.UID && slotTarget.Revision == a.Revision && slotTarget.DirectoryID == a.PlaybackParentCID
	}
	borrow := poolHandoffCandidate(d, req, user, now)
	if slotKey != "" {
		borrow = model.PlaybackPoolLease{}
	}
	if req.transferCapacity && slotKey != "" && req.userGeneration != 0 && !req.Download && !req.Task {
		borrow = poolTransferCandidate(d, user, slotKey, accounts, excluded, now)
	}
	canBorrow := func(a P115Account) bool {
		return borrow.Key != "" && borrow.AccountID == a.ID && borrow.UID == a.UID && borrow.Revision == a.Revision && borrow.DirectoryID == a.PlaybackParentCID && d.Health[a.ID].State == "ready" && d.Health[a.ID].Revision == a.Revision
	}
	previous, exists := d.Leases[key]
	if exists && previous.UserID != user {
		return nil, ErrLogin
	}
	if slotKey != "" {
		pending := 0
		for k, l := range d.Leases {
			if l.SlotKey == slotKey && k != key && !l.Issued {
				pending++
			}
		}
		if pending >= 1 && !exists {
			return nil, ErrPoolBusy
		}
	}
	options := make([]P115Account, 0, len(accounts))
	seen := make(map[string]bool)
	full := false
	blocked := make(map[string]map[string]bool, len(accounts))
	defer func() {
		uids := make([]string, 0, len(blocked))
		for uid := range blocked {
			uids = append(uids, uid)
		}
		sort.Strings(uids)
		for _, uid := range uids {
			reasons := make([]string, 0, len(blocked[uid]))
			for reason := range blocked[uid] {
				reasons = append(reasons, reason)
			}
			sort.Strings(reasons)
			for _, reason := range reasons {
				recordPoolCandidate(ctx, uid, "excluded", reason, 0)
			}
		}
	}()
	cooling, invalid := false, false
	for _, a := range accounts {
		if !eligiblePoolAccount(a) {
			continue
		}
		mode := d.Modes[a.ID]
		if mode == "disabled" {
			blockPoolUID(blocked, a.UID, "disabled")
			continue
		}
		h := d.Health[a.ID]
		if h.Revision == a.Revision && (h.State == "invalid_credentials" || h.State == "invalid_directory" || h.Until > now) {
			blockPoolUID(blocked, a.UID, h.State)
			invalid = invalid || h.State == "invalid_credentials" || h.State == "invalid_directory"
			cooling = cooling || h.Until > now
			continue
		}
		uidCooling := false
		for _, health := range d.Health {
			if health.State == "throttled" && health.UID == a.UID && health.Until > now {
				uidCooling = true
				break
			}
		}
		if uidCooling {
			blockPoolUID(blocked, a.UID, "throttled")
			cooling = true
			continue
		}
		if excluded[a.UID] {
			blockPoolUID(blocked, a.UID, "already_attempted")
			continue
		}
		if exists && previous.AccountID == a.ID && previous.UID == a.UID && previous.Revision == a.Revision && previous.DirectoryID == a.PlaybackParentCID && (slotKey == "" || slotTarget.Key == "" || preferredSlot(a)) {
			req, _ := ctx.Value(poolRequestKey{}).(PoolRequest)
			if previous.Stopped || req.userGeneration != 0 && previous.UserGeneration != req.userGeneration {
				d.Generation++
				previous.Generation = d.Generation
				previous.UserGeneration = req.userGeneration
				previous.Stopped = false
				d.Leases[key] = previous
				if err := p.save(ctx, d); err != nil {
					return nil, err
				}
			}
			p.metrics.Reused++
			recordPoolCandidate(ctx, a.UID, "selected", "existing_lease", previous.Generation)
			return &PoolLease{pool: p, Record: previous, Account: a, healthSequence: h.Sequence}, nil
		}
		if mode == "draining" && !preferredSlot(a) {
			blockPoolUID(blocked, a.UID, string(PoolDraining))
			full = true
			continue
		}
		if counts[a.UID] >= d.Settings.MaxSessions && !canBorrow(a) && !sharesSlot(a) {
			blockPoolUID(blocked, a.UID, string(PoolCapacityFull))
			full = true
			continue
		}
		// A recovering UID admits one preparing request, not a probe storm.
		if slotKey != "" && !sharesSlot(a) {
			uids := make(map[string]bool)
			for _, l := range d.Leases {
				if l.SlotKey == slotKey {
					uids[l.UID] = true
				}
			}
			if len(uids) >= 3 {
				full = true
				blockPoolUID(blocked, a.UID, string(PoolLeaseLimit))
				continue
			}
		}
		if h.State != "ready" {
			preparing := false
			for _, lease := range d.Leases {
				if lease.UID == a.UID && !lease.Issued {
					preparing = true
					break
				}
			}
			if preparing {
				blockPoolUID(blocked, a.UID, string(PoolRecoveryBusy))
				full = true
				continue
			}
		}
		if !seen[a.UID] || canBorrow(a) || preferredSlot(a) {
			options = append(options, a)
			seen[a.UID] = true
		}
	}
	if len(options) == 0 {
		if full {
			return nil, p.admissionError(d, poolBlockedReason(blocked), blocked)
		}
		if cooling {
			return nil, ErrPoolCooling
		}
		if invalid {
			return nil, ErrPoolInvalid
		}
		return nil, ErrPoolEmpty
	}
	if len(d.Leases) >= 4096 || exists && previous.Issued && len(d.Leases) >= 4095 {
		for _, a := range options {
			blockPoolUID(blocked, a.UID, string(PoolLeaseLimit))
		}
		return nil, p.admissionError(d, PoolLeaseLimit, blocked)
	}
	preferred := p.poolPreferences(ctx, user, now)
	// Capacity ranks first. Local copy and expiring preference only break ties.
	sort.Slice(options, func(i, j int) bool {
		if preferredSlot(options[i]) != preferredSlot(options[j]) {
			return preferredSlot(options[i])
		}
		if canBorrow(options[i]) != canBorrow(options[j]) {
			return canBorrow(options[i])
		}
		if counts[options[i].UID] != counts[options[j].UID] {
			return counts[options[i].UID] < counts[options[j].UID]
		}
		if preferred(options[i].UID) != preferred(options[j].UID) {
			return preferred(options[i].UID) > preferred(options[j].UID)
		}
		return options[i].ID < options[j].ID
	})
	n := 1
	for n < len(options) && preferredSlot(options[n]) == preferredSlot(options[0]) && canBorrow(options[n]) == canBorrow(options[0]) && counts[options[n].UID] == counts[options[0].UID] && preferred(options[n].UID) == preferred(options[0].UID) {
		n++
	}
	a := options[int(d.Generation%uint64(n))]
	for _, option := range options {
		if option.ID != a.ID {
			recordPoolCandidate(ctx, option.UID, "eligible", "lower_rank", 0)
		}
	}
	reusesSlot := sharesSlot(a)
	if exists && previous.Issued {
		// A previously issued CDN URL may remain in use even after failover.
		previous.Stopped = true
		previous.ExpiresAt = min(previous.ExpiresAt, p.now().Add(2*time.Minute).UnixMilli())
		d.Leases[fmt.Sprintf("residual:%s:%d", key, previous.Generation)] = previous
	}
	d.Generation++
	l := model.PlaybackPoolLease{Key: key, UserID: user, AccountID: a.ID, UID: a.UID, Revision: a.Revision, DirectoryID: a.PlaybackParentCID, Generation: d.Generation, ExpiresAt: p.now().Add(time.Duration(d.Settings.BudgetSeconds+5) * time.Second).UnixMilli()}
	l.SlotKey = slotKey
	if canBorrow(a) {
		l.BorrowedKey, l.BorrowedGeneration = borrow.Key, borrow.Generation
	}
	if req, ok := ctx.Value(poolRequestKey{}).(PoolRequest); ok {
		l.SessionKey = req.lifecycleKey
		l.EventFallback = req.eventFallback
		l.UserGeneration = req.userGeneration
		l.ObservationKey = req.observationKey
		if req.Task {
			l.ExpiresAt = p.now().Add(85 * time.Second).UnixMilli()
		}
	}
	d.Leases[key] = l
	if err := p.save(ctx, d); err != nil {
		return nil, err
	}
	p.metrics.Allocations++
	recordPoolCandidate(ctx, a.UID, "selected", "reserved", l.Generation)
	additional := 1
	if canBorrow(a) || reusesSlot {
		additional = 0
	}
	p.metrics.PeakUIDSessions = max(p.metrics.PeakUIDSessions, counts[a.UID]+additional)
	if preferred(a.UID) >= 2 {
		p.metrics.CopyPreferred++
	} else if preferred(a.UID) == 1 {
		p.metrics.RecentPreferred++
	}
	return &PoolLease{pool: p, Record: l, Account: a, healthSequence: d.Health[a.ID].Sequence}, nil
}

func (p *DynamicPool) CoolingUntil() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.UnixMilli(p.doc.GlobalUntil)
}

func (p *DynamicPool) Acquire(ctx context.Context, key, user string, accounts []P115Account, probe func(context.Context, P115Account) error) (*PoolLease, error) {
	settings := p.Settings()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(settings.BudgetSeconds)*time.Second)
	defer cancel()
	if err := p.lockSession(ctx, key); err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			p.unlockSession(key)
		}
	}()
	attempts, _ := ctx.Value(poolAttemptsKey{}).(*PoolAttempts)
	if attempts == nil {
		attempts = &PoolAttempts{remaining: settings.MaxAttempts, excluded: make(map[string]bool)}
	}
	for attempts.remaining > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		l, err := p.reserve(ctx, key, user, accounts, attempts.excluded)
		if err != nil {
			return nil, err
		}
		attempts.remaining--
		attempts.excluded[l.Account.UID] = true
		if probe == nil {
			l.skipHealthRefresh = true
			recordPoolCandidate(ctx, l.Account.UID, "probe", "deferred_to_target_read", l.Record.Generation)
			ok = true
			return l, nil
		}
		probeCtx, probeCancel := context.WithTimeout(ctx, 8*time.Second)
		err = probe(probeCtx, l.Account)
		probeCancel()
		recordPoolCandidate(ctx, l.Account.UID, "probe", ClassifyPoolFault(PoolFaultProbe, err).Code, l.Record.Generation)
		if err == nil {
			ok = true
			return l, nil
		}
		if ctx.Err() != nil {
			return nil, errors.Join(ctx.Err(), l.releaseUnissued())
		}
		if errors.Is(err, context.Canceled) {
			return nil, errors.Join(err, l.releaseUnissued())
		}
		if errors.Is(err, p115.ErrRateQueueFull) {
			return nil, errors.Join(err, l.releaseUnissued())
		}
		if !ClassifyPoolFault(PoolFaultProbe, err).SwitchUID {
			return nil, errors.Join(err, l.releaseUnissued())
		}
		if saveErr := p.report(ctx, l.Account, err); saveErr != nil {
			return nil, errors.Join(saveErr, l.releaseUnissued())
		}
		if releaseErr := l.releaseUnissued(); releaseErr != nil {
			return nil, releaseErr
		}
	}
	return nil, ErrPoolUnavailable
}

func (p *DynamicPool) report(ctx context.Context, a P115Account, err error) error {
	return p.reportChecked(ctx, a, err, nil)
}

func (p *DynamicPool) reportChecked(ctx context.Context, a P115Account, err error, sequence *uint64) error {
	decision := ClassifyPoolFault(PoolFaultTarget, err)
	if err != nil && decision.Health == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	old := d.Health[a.ID]
	if old.Revision > a.Revision || sequence != nil && old.Sequence != *sequence {
		return nil
	}
	h := model.PlaybackPoolHealth{Revision: a.Revision, State: "ready", UID: a.UID, CheckedAt: p.now().UnixMilli()}
	h.Sequence = old.Sequence + 1
	h.NextProbe = p.now().Add(5*time.Minute + poolProbeJitter(a.ID)).UnixMilli()
	if err != nil {
		h.Failures = 1
		if old.Revision == a.Revision {
			h.Failures = min(old.Failures+1, 8)
		}
		h.State = decision.Health
		base := time.Duration(d.Settings.CooldownSeconds) * time.Second
		delay := min(base*time.Duration(1<<min(h.Failures-1, 5)), max(base, 30*time.Minute))
		h.Until = p.now().Add(delay + poolProbeJitter(a.ID)).UnixMilli()
		if errors.Is(err, p115.ErrShareLoginRequired) {
			h.State = "invalid_credentials"
			h.Until = 0
		}
		if errors.Is(err, p115.ErrPlaybackDirectory) {
			h.State = "invalid_directory"
			h.Until = 0
		}
		var upstream *p115.HTTPError
		if errors.Is(err, p115.ErrShareThrottled) || errors.As(err, &upstream) && upstream.Status == 429 {
			h.State = "throttled"
			if upstream != nil && upstream.RetryAfter > 0 {
				until := p.now().Add(upstream.RetryAfter).UnixMilli()
				if until > h.Until {
					h.Until = until
				}
			}
			if upstream != nil && upstream.Status == 429 {
				d.GlobalUntil = max(d.GlobalUntil, h.Until)
			}
		}
		h.NextProbe = max(h.NextProbe, h.Until)
	}
	d.Health[a.ID] = h
	p.recordHealthEvent(&d, a.ID, old, h)
	return p.save(ctx, d)
}

func (l *PoolLease) Current() bool {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.doc.Leases[l.Record.Key]
	return ok && !c.Stopped && c.Generation == l.Record.Generation && c.ExpiresAt > p.now().UnixMilli() && p.doc.Modes[c.AccountID] != "disabled"
}

func (l *PoolLease) Commit(ctx context.Context) error {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	c, ok := d.Leases[l.Record.Key]
	if c.BorrowedKey != "" {
		return ErrAccountChanged
	} // Requires atomic user + pool handoff.
	if !ok || c.Generation != l.Record.Generation || c.ExpiresAt <= p.now().UnixMilli() || d.Modes[c.AccountID] == "disabled" {
		return ErrAccountChanged
	}
	old := d.Health[c.AccountID]
	if old.Sequence != l.healthSequence && old.State != "ready" && old.Revision == c.Revision {
		return ErrAccountChanged
	}
	c.Issued = true
	c.IssuedAt = p.now().UnixMilli()
	c.Stopped = false
	c.ExpiresAt = p.now().Add(time.Duration(d.Settings.LeaseMinutes) * time.Minute).UnixMilli()
	d.Leases[c.Key] = c
	// A late successful request cannot clear a newer observed account failure.
	if old.Sequence == l.healthSequence && !l.skipHealthRefresh {
		h := model.PlaybackPoolHealth{Revision: c.Revision, UID: c.UID, State: "ready", CheckedAt: p.now().UnixMilli(), Sequence: old.Sequence + 1, NextProbe: p.now().Add(5*time.Minute + poolProbeJitter(c.AccountID)).UnixMilli()}
		d.Health[c.AccountID] = h
		p.recordHealthEvent(&d, c.AccountID, old, h)
	}
	if err := p.save(ctx, d); err != nil {
		return err
	}
	l.Record = c
	p.rememberPoolPreference(c.UserID, c.UID)
	return nil
}

func (l *PoolLease) releaseUnissued() error {
	p := l.pool
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.doc.Leases[l.Record.Key]
	if !ok || c.Generation != l.Record.Generation || c.Issued {
		return nil
	}
	d := p.clone()
	delete(d.Leases, c.Key)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return p.save(ctx, d)
}

func (l *PoolLease) Close() error {
	if l == nil || l.closed {
		return nil
	}
	l.closed = true
	defer l.pool.unlockSession(l.Record.Key)
	return l.releaseUnissued()
}

// Stops keep two minutes of residual occupancy because an issued CDN URL
// cannot be revoked. Missing events fall back to the configured lease timeout.
func (p *DynamicPool) SessionEvent(ctx context.Context, key string, stopped bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.clone()
	now := p.now()
	changed := false
	for k, l := range d.Leases {
		if l.ExpiresAt <= now.UnixMilli() {
			delete(d.Leases, k)
			changed = true
			continue
		}
		if key == "" || l.SessionKey != key || !l.Issued {
			continue
		}
		if stopped {
			until := now.Add(2 * time.Minute).UnixMilli()
			if l.Stopped {
				continue
			}
			l.ExpiresAt = min(l.ExpiresAt, until)
			l.Stopped = true
		} else {
			// Residual leases and stopped sessions are never revived by late progress.
			if l.Stopped || k != l.Key {
				continue
			}
			until := now.Add(time.Duration(d.Settings.LeaseMinutes) * time.Minute).UnixMilli()
			if until-l.ExpiresAt < int64(time.Minute/time.Millisecond) {
				continue
			}
			l.ExpiresAt = until
		}
		d.Leases[k] = l
		changed = true
	}
	if !changed {
		return nil
	}
	return p.save(ctx, d)
}
