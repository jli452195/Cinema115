package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type DriveSTRMLifeClient interface {
	LifeEvents(context.Context, int, int, string) (p115.LifeEventPage, error)
}

type DriveSTRMEventState struct {
	LastCheckedAt time.Time                         `json:"lastCheckedAt,omitempty"`
	LastError     string                            `json:"lastError,omitempty"`
	Consumers     map[string]LifeEventConsumerState `json:"consumers,omitempty"`
}

// DriveSTRMEvents 管理唯一采集器和独立消费者，网络核验不占用采集生命周期。
type DriveSTRMEvents struct {
	Organizer *MediaOrganizer
	Hub       *P115LifeEvents
	service   *DriveSTRMService
	client    DriveSTRMLifeClient
	gate      chan struct{}
	mu        sync.RWMutex
	state     DriveSTRMEventState
	refresh   chan struct{}
	running   context.Context
	inflight  *lifeEventRefresh
	strm      *P115LifeSubscription
	organize  *P115LifeSubscription
}

type lifeEventRefresh struct {
	done    chan struct{}
	err     error
	started bool
}

type lifeEventWork struct {
	batch       P115LifeEventBatch
	directories []string
	invalidated []string
	offset      int
	unresolved  []string
	resolved    int
	roots       []string
	scopeSet    map[string]bool
	knownScopes map[string][]string
	scopeCount  int
	parentID    string
	parentSeen  map[string]bool
	parentOwned map[string]bool
}

func NewDriveSTRMEvents(service *DriveSTRMService, client DriveSTRMLifeClient) *DriveSTRMEvents {
	var store lifeEventCursorStore
	if service != nil {
		store = service.Store
	}
	hub := NewP115LifeEvents(store)
	strm := hub.addSubscription("strm")
	organize := hub.addSubscription("organize")
	return &DriveSTRMEvents{service: service, client: client, Hub: hub, strm: strm, organize: organize, gate: make(chan struct{}, 1), refresh: make(chan struct{}, 1)}
}

func (r *DriveSTRMEvents) State() DriveSTRMEventState {
	r.mu.RLock()
	value := r.state
	r.mu.RUnlock()
	value.Consumers = r.Hub.states()
	return value
}

func (r *DriveSTRMEvents) hasAdditionalConsumers() bool {
	r.Hub.mu.Lock()
	defer r.Hub.mu.Unlock()
	return len(r.Hub.consumers) > 2
}

// Check 合并并发刷新；服务运行时调用者取消只结束自身等待，不取消共享采集。
func (r *DriveSTRMEvents) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	flight := r.inflight
	owner := false
	if flight == nil {
		flight = &lifeEventRefresh{done: make(chan struct{})}
		r.inflight = flight
		owner = r.running == nil
		flight.started = owner
		if !owner {
			select {
			case r.refresh <- struct{}{}:
			default:
			}
		}
	}
	r.mu.Unlock()
	if owner {
		r.collect(ctx, flight)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-flight.done:
		return flight.err
	}
}

// RequestRefresh 异步申请共享刷新，已有采集进行时合并请求，不创建临时协程。
func (r *DriveSTRMEvents) RequestRefresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running == nil || r.running.Err() != nil {
		return fmt.Errorf("生活事件采集器尚未运行")
	}
	if r.inflight == nil {
		r.inflight = &lifeEventRefresh{done: make(chan struct{})}
		select {
		case r.refresh <- struct{}{}:
		default:
		}
	}
	return nil
}

// collect 的上下文由常驻采集器拥有，页面超时不会撤销共享请求。
func (r *DriveSTRMEvents) collect(ctx context.Context, flight *lifeEventRefresh) {
	var err error
	select {
	case r.gate <- struct{}{}:
		err = r.check(ctx)
		<-r.gate
	case <-ctx.Done():
		err = ctx.Err()
	}
	if r.Organizer != nil && (err != nil || r.Hub.states()["organize"].LastError == "") {
		r.Organizer.lifeEventStatus(err == nil)
	}
	r.mu.Lock()
	r.state.LastCheckedAt = time.Now().UTC()
	r.state.LastError = ""
	if err != nil {
		r.state.LastError = err.Error()
	}
	flight.err = err
	if r.inflight == flight {
		r.inflight = nil
	}
	select {
	case <-flight.done:
	default:
		close(flight.done)
	}
	r.mu.Unlock()
}

// Run 统一启动并回收采集和消费协程，上游故障退避不影响已有缓存的消费。
func (r *DriveSTRMEvents) Run(ctx context.Context) {
	r.run(ctx, 5*time.Second)
}

// 周期变化只影响下一轮采集；消费者始终可处理已经接收的事件。
func (r *DriveSTRMEvents) run(ctx context.Context, refresh time.Duration) {
	if r.service == nil || r.client == nil {
		return
	}
	r.mu.Lock()
	if r.running != nil {
		r.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.running = runCtx
	r.mu.Unlock()
	if r.Organizer != nil {
		r.Organizer.EnableLifeEventConsumer()
	}
	var workers sync.WaitGroup
	for _, subscription := range []*P115LifeSubscription{r.strm, r.organize} {
		workers.Add(1)
		go func(s *P115LifeSubscription) { defer workers.Done(); r.consumeLoop(runCtx, s) }(subscription)
	}
	defer func() {
		cancel()
		workers.Wait()
		r.mu.Lock()
		r.running = nil
		if r.inflight != nil {
			if !r.inflight.started {
				r.inflight.err = runCtx.Err()
				close(r.inflight.done)
				r.inflight = nil
			}
		}
		r.mu.Unlock()
	}()
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	var lastAttempt time.Time
	failures := 0
	for {
		if runCtx.Err() != nil {
			return
		}
		settings, err := r.service.Store.GetDriveSTRMSettings(runCtx)
		if r.hasAdditionalConsumers() {
			if errors.Is(err, storage.ErrNotFound) {
				settings, err = model.DefaultDriveSTRMSettings(), nil
			}
			if settings.EventIntervalSeconds == 0 {
				settings.EventIntervalSeconds = model.DefaultDriveSTRMSettings().EventIntervalSeconds
			}
		}
		if len(r.Organizer.event115Roots("")) > 0 {
			if errors.Is(err, storage.ErrNotFound) {
				settings, err = model.DefaultDriveSTRMSettings(), nil
			}
			if settings.EventIntervalSeconds == 0 || settings.EventIntervalSeconds > 15 {
				settings.EventIntervalSeconds = 15
			}
		}
		r.mu.Lock()
		flight := r.inflight
		if flight == nil && err == nil && driveSTRMEventDue(settings, lastAttempt, failures, time.Now()) {
			flight = &lifeEventRefresh{done: make(chan struct{})}
			r.inflight = flight
		}
		if flight != nil {
			if flight.started {
				flight = nil
			} else {
				flight.started = true
			}
		}
		r.mu.Unlock()
		if flight != nil {
			r.collect(runCtx, flight)
			err = flight.err
			lastAttempt = time.Now()
			if err == nil {
				failures = 0
			} else {
				failures++
			}
		}
		select {
		case <-runCtx.Done():
			return
		case <-ticker.C:
		case <-r.refresh:
		}
	}
}

func driveSTRMEventDue(settings model.DriveSTRMSettings, last time.Time, failures int, now time.Time) bool {
	if settings.EventIntervalSeconds == 0 {
		return false
	}
	interval := time.Duration(settings.EventIntervalSeconds) * time.Second
	delay := min(interval*time.Duration(1<<min(failures, 4)), max(interval, 5*time.Minute))
	return last.IsZero() || !last.Add(delay).After(now)
}

func (r *DriveSTRMEvents) check(ctx context.Context) error {
	if r.service == nil || r.client == nil {
		return fmt.Errorf("目录 STRM 事件服务尚未初始化")
	}
	s := r.service
	accountID := s.Accounts.PrimaryID()
	organizerRoots := r.Organizer.event115Roots(accountID)
	if _, err := s.Store.GetDriveSTRMSettings(ctx); err != nil {
		if errors.Is(err, storage.ErrNotFound) && len(organizerRoots) == 0 && !r.hasAdditionalConsumers() {
			return nil
		}
		if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
	}
	values, err := s.Store.ListDriveSTRM(ctx)
	if err != nil {
		return err
	}
	roots := make([]string, 0, len(values))
	for _, value := range values {
		if value.Enabled && value.AccountID == accountID {
			roots = append(roots, value.SourceCID)
		}
	}
	roots = append(roots, organizerRoots...)
	if len(roots) == 0 && !r.hasAdditionalConsumers() {
		return nil
	}
	account, err := s.Accounts.Primary(accountID)
	if err != nil {
		return err
	}
	uid := p115.CookieUserID(account.Cookie)
	cursor, err := r.Hub.account(ctx, account.ID, uid)
	if err != nil {
		return err
	}
	var watermark uint64
	if cursor != "" {
		watermark, err = strconv.ParseUint(cursor, 10, 64)
		if err != nil {
			return fmt.Errorf("目录 STRM 生活事件游标无效: %w", err)
		}
	}
	latest := watermark
	events := make([]p115.LifeEvent, 0, 64)
	seen := make(map[string]bool)
	repair := false
	queueRoots := func() {
		repair = true
	}
	for offset := 0; ; {
		limit := 1000
		if offset == 0 {
			limit = 64
		}
		if cursor == "" {
			limit = 1
		} else if offset+limit > 10000 {
			limit = 10000 - offset
		}
		pageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		page, err := r.client.LifeEvents(pageCtx, offset, limit, account.Cookie)
		cancel()
		if err != nil {
			return fmt.Errorf("读取生活事件分页: %w", err)
		}
		reached := false
		for _, event := range page.Events {
			id, err := strconv.ParseUint(event.ID, 10, 64)
			if err != nil || id == 0 {
				return fmt.Errorf("目录 STRM 收到无效生活事件编号")
			}
			if offset == 0 && id > latest {
				latest = id
			}
			// 固定首屏水位；分页期间出现的新记录留给下一轮，不能越过未读取的缺口。
			if offset > 0 && id > latest {
				continue
			}
			if id <= watermark {
				reached = true
				continue
			}
			if !seen[event.ID] {
				events = append(events, event)
				seen[event.ID] = true
			}
		}
		if cursor == "" {
			// 首次建立基线后分别补扫，已运行的旧扫描不能代替本轮交接。
			queueRoots()
			break
		}
		offset += len(page.Events)
		if reached {
			break
		}
		if len(page.Events) == 0 || offset >= 10000 || offset >= page.Count {
			// 历史截断或清空时补扫一次，空历史基线设为零，避免闲置时重复全扫。
			if watermark > 0 {
				queueRoots()
				if offset == 0 {
					latest = 0
				}
			}
			break
		}
	}
	current, err := s.Accounts.Primary(account.ID)
	if err != nil || p115.CookieUserID(current.Cookie) != uid {
		return ErrAccountChanged
	}
	batch := P115LifeEventBatch{AccountID: account.ID, UID: uid, Events: events, Repair: repair}
	if r.Organizer != nil {
		changed := make([]string, 0)
		for _, event := range events {
			if event.FileID != "" && (event.Type == 5 || event.Type == 6 || event.Type == 20) {
				changed = append(changed, event.FileID)
			}
		}
		// 先保存已知目录类型再失效；新结构事件使旧批次在途核验的版本立即过期。
		batch.KnownDirectories = r.Organizer.event115KnownDirectories(account.ID, changed)
		r.Organizer.invalidate115EventDirectories(account.ID, changed)
	}
	r.Hub.publish(batch, strconv.FormatUint(latest, 10))
	return nil
}
