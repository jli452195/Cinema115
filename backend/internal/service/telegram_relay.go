package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"golang.org/x/sync/errgroup"
	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/model"
)

var ErrTelegramRelayInput = errors.New("转发规则无效")

// The relay depends only on Telegram transport and its own persistence. It does
// not call Bot automation, resource search, 115, subscriptions or STRM services.
type TelegramRelayTransport interface {
	View() tguser.View
	ListRelayPeers(context.Context, string) (tguser.RelayPeerPage, error)
	SavedRelayPeers() []tguser.RelayPeer
	RelayAvatar(context.Context, string, string) ([]byte, string, error)
	PrepareRelay(context.Context, string, string, string, string) (tguser.RelayIdentity, error)
	RunRelay(context.Context, tguser.RelayStateStore, func(context.Context, int64, *tg.Message) error) error
	RelaySend(context.Context, int64, string, tguser.RelayPayload, int64) error
	RelayConnected() bool
}

type TelegramRelayStore interface {
	tguser.RelayStateStore
	InitTelegramRelay(context.Context) error
	SaveTelegramRelayRule(context.Context, model.TelegramRelayRule) error
	DeleteTelegramRelayRule(context.Context, string) error
	DueTelegramRelayJobs(context.Context, int64, time.Time, int) ([]model.TelegramRelayJob, error)
	RemoveTelegramRelayJob(context.Context, int64) error
	DeferTelegramRelayJob(context.Context, int64, time.Time) error
}

type TelegramRelayRuleInput struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	SourceID             string   `json:"sourceId"`
	TargetID             string   `json:"targetId"`
	SourceSelectionToken string   `json:"sourceSelectionToken"`
	TargetSelectionToken string   `json:"targetSelectionToken"`
	Enabled              bool     `json:"enabled"`
	LinkTypes            []string `json:"linkTypes"`
	IncludePattern       string   `json:"includePattern"`
	ExcludePattern       string   `json:"excludePattern"`
}

type TelegramRelayView struct {
	Rules   []model.TelegramRelayRule `json:"rules"`
	Peers   []tguser.RelayPeer        `json:"peers"`
	Status  TelegramRelayStatus       `json:"status"`
	Account tguser.View               `json:"account"`
}

type TelegramRelayStatus struct {
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

type compiledRelayRule struct {
	rule             model.TelegramRelayRule
	include, exclude *regexp.Regexp
}

type TelegramRelay struct {
	store               TelegramRelayStore
	transport           TelegramRelayTransport
	logs                *GenerationLogs
	configMu            sync.Mutex
	mu                  sync.Mutex
	rules               map[string]compiledRelayRule
	locks               map[string]*sync.Mutex
	lastError           string
	ownerID             int64
	cooldown            time.Time
	wakeWatch, wakeSend chan struct{}
}

func NewTelegramRelay(ctx context.Context, store TelegramRelayStore, transport TelegramRelayTransport, logs *GenerationLogs) (*TelegramRelay, error) {
	if err := store.InitTelegramRelay(ctx); err != nil {
		return nil, err
	}
	rules, err := store.ListTelegramRelayRules(ctx)
	if err != nil {
		return nil, err
	}
	s := &TelegramRelay{store: store, transport: transport, logs: logs, rules: make(map[string]compiledRelayRule), locks: make(map[string]*sync.Mutex), wakeWatch: make(chan struct{}, 1), wakeSend: make(chan struct{}, 1)}
	for _, rule := range rules {
		compiled, err := compileRelay(rule)
		if err != nil {
			return nil, err
		}
		s.rules[rule.ID] = compiled
	}
	return s, nil
}

func compileRelay(rule model.TelegramRelayRule) (compiledRelayRule, error) {
	result := compiledRelayRule{rule: rule}
	for _, item := range []struct {
		source string
		target **regexp.Regexp
		label  string
	}{{rule.IncludePattern, &result.include, "白名单"}, {rule.ExcludePattern, &result.exclude, "黑名单"}} {
		if len(item.source) > 2048 {
			return result, fmt.Errorf("%w：%s正则过长", ErrTelegramRelayInput, item.label)
		}
		if item.source != "" {
			pattern, err := regexp.Compile(item.source)
			if err != nil {
				return result, fmt.Errorf("%w：%s正则语法错误", ErrTelegramRelayInput, item.label)
			}
			*item.target = pattern
		}
	}
	return result, nil
}

func (s *TelegramRelay) ruleLock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks[id] == nil {
		s.locks[id] = &sync.Mutex{}
	}
	return s.locks[id]
}

func (s *TelegramRelay) notify() {
	for _, ch := range []chan struct{}{s.wakeWatch, s.wakeSend} {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *TelegramRelay) List(ctx context.Context) (TelegramRelayView, error) {
	s.mu.Lock()
	rules := make([]model.TelegramRelayRule, 0, len(s.rules))
	for _, value := range s.rules {
		rules = append(rules, value.rule)
	}
	status := TelegramRelayStatus{Error: s.lastError}
	s.mu.Unlock()
	slices.SortFunc(rules, func(a, b model.TelegramRelayRule) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	result := TelegramRelayView{Rules: rules, Peers: s.transport.SavedRelayPeers(), Status: status, Account: s.transport.View()}
	result.Status.Connected = s.transport.RelayConnected()
	return result, nil
}

func (s *TelegramRelay) Peers(ctx context.Context, cursor string) (tguser.RelayPeerPage, error) {
	return s.transport.ListRelayPeers(ctx, cursor)
}

func (s *TelegramRelay) Avatar(ctx context.Context, peerID, avatarID string) ([]byte, string, error) {
	return s.transport.RelayAvatar(ctx, peerID, avatarID)
}

func (s *TelegramRelay) Save(ctx context.Context, input TelegramRelayRuleInput) (model.TelegramRelayRule, error) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.mu.Lock()
	previous, exists := s.rules[input.ID]
	s.mu.Unlock()
	if input.ID != "" && !exists {
		return model.TelegramRelayRule{}, sql.ErrNoRows
	}
	name := strings.TrimSpace(input.Name)
	if len(name) > 240 || input.SourceID == "" || input.TargetID == "" || len(input.SourceID) > 80 || len(input.TargetID) > 80 || len(input.SourceSelectionToken) > 64 || len(input.TargetSelectionToken) > 64 || input.SourceID == input.TargetID {
		return model.TelegramRelayRule{}, fmt.Errorf("%w：请选择不同的来源频道和发送目标", ErrTelegramRelayInput)
	}
	if len(input.LinkTypes) == 0 || len(input.LinkTypes) > 2 {
		return model.TelegramRelayRule{}, fmt.Errorf("%w：请选择链接类型", ErrTelegramRelayInput)
	}
	seen := map[string]bool{}
	for _, kind := range input.LinkTypes {
		if (kind != "115" && kind != "ed2k") || seen[kind] {
			return model.TelegramRelayRule{}, ErrTelegramRelayInput
		}
		seen[kind] = true
	}
	rule := model.TelegramRelayRule{ID: input.ID, Name: name, SourceID: input.SourceID, TargetID: input.TargetID, Enabled: input.Enabled, LinkTypes: slices.Clone(input.LinkTypes), IncludePattern: strings.TrimSpace(input.IncludePattern), ExcludePattern: strings.TrimSpace(input.ExcludePattern)}
	compiled, err := compileRelay(rule)
	if err != nil {
		return model.TelegramRelayRule{}, err
	}
	old := previous.rule
	identity := tguser.RelayIdentity{OwnerID: old.OwnerID, SourceID: old.SourceID, SourceTitle: old.SourceTitle, TargetID: old.TargetID, TargetTitle: old.TargetTitle}
	if !exists || old.SourceID != input.SourceID || old.TargetID != input.TargetID || (!old.Enabled && input.Enabled) || input.SourceSelectionToken != "" || input.TargetSelectionToken != "" {
		identity, err = s.transport.PrepareRelay(ctx, input.SourceID, input.TargetID, input.SourceSelectionToken, input.TargetSelectionToken)
		if err != nil {
			return model.TelegramRelayRule{}, err
		}
	}
	rule.OwnerID, rule.SourceID, rule.SourceTitle, rule.TargetID, rule.TargetTitle = identity.OwnerID, identity.SourceID, identity.SourceTitle, identity.TargetID, identity.TargetTitle
	rule.SourcePts = identity.SourcePts
	if rule.OwnerID <= 0 || rule.SourceID == rule.TargetID {
		return model.TelegramRelayRule{}, ErrTelegramRelayInput
	}
	if rule.ID == "" {
		id, err := relayID()
		if err != nil {
			return model.TelegramRelayRule{}, err
		}
		rule.ID = id
	}
	lock := s.ruleLock(rule.ID)
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	if !exists && len(s.rules) >= 100 {
		s.mu.Unlock()
		return model.TelegramRelayRule{}, fmt.Errorf("%w：最多保存100条转发规则", ErrTelegramRelayInput)
	}
	for id, other := range s.rules {
		if id != rule.ID && other.rule.OwnerID == rule.OwnerID && other.rule.SourceID == rule.SourceID {
			s.mu.Unlock()
			return model.TelegramRelayRule{}, fmt.Errorf("%w：此频道已有转发规则，请编辑原规则", ErrTelegramRelayInput)
		}
	}
	if relayHasCycle(s.rules, rule) {
		s.mu.Unlock()
		return model.TelegramRelayRule{}, fmt.Errorf("%w：来源与目标形成转发循环", ErrTelegramRelayInput)
	}
	s.mu.Unlock()
	now := time.Now().UTC()
	rule.UpdatedAt = now
	rule.StartedAt = now
	if exists && previous.rule.OwnerID == rule.OwnerID && previous.rule.SourceID == rule.SourceID && previous.rule.TargetID == rule.TargetID {
		rule.StartedAt = previous.rule.StartedAt
	}
	if rule.Name == "" {
		rule.Name = rule.SourceTitle
	}
	compiled.rule = rule
	if err := s.store.SaveTelegramRelayRule(ctx, rule); err != nil {
		return model.TelegramRelayRule{}, err
	}
	s.mu.Lock()
	s.rules[rule.ID] = compiled
	s.mu.Unlock()
	s.notify()
	s.logs.Append("Telegram", rule.ID, "频道转发", "INFO", "已保存转发规则："+rule.Name)
	return rule, nil
}

func relayHasCycle(rules map[string]compiledRelayRule, proposed model.TelegramRelayRule) bool {
	visited := map[string]bool{}
	for peer := proposed.TargetID; peer != ""; {
		if peer == proposed.SourceID {
			return true
		}
		if visited[peer] {
			return false
		}
		visited[peer] = true
		next := ""
		for id, entry := range rules {
			if id != proposed.ID && entry.rule.OwnerID == proposed.OwnerID && entry.rule.SourceID == peer {
				next = entry.rule.TargetID
				break
			}
		}
		peer = next
	}
	return false
}

func (s *TelegramRelay) Delete(ctx context.Context, id string) error {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	lock := s.ruleLock(id)
	lock.Lock()
	defer lock.Unlock()
	if err := s.store.DeleteTelegramRelayRule(ctx, id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.rules, id)
	s.mu.Unlock()
	s.notify()
	s.logs.Append("Telegram", id, "频道转发", "INFO", "已删除转发规则及未发送消息")
	return nil
}

// Receive commits new resource identities and the outgoing card together. An
// edited post only queues newly introduced links; ordinary text edits are inert.
func (s *TelegramRelay) receive(ctx context.Context, ownerID int64, message *tg.Message) error {
	if message == nil {
		s.mu.Lock()
		s.ownerID = ownerID
		s.lastError = ""
		s.mu.Unlock()
		select {
		case s.wakeSend <- struct{}{}:
		default:
		}
		return nil
	}
	peer, ok := message.PeerID.(*tg.PeerChannel)
	if !ok {
		return nil
	}
	source := "channel:" + strconv.FormatInt(peer.ChannelID, 10)
	s.mu.Lock()
	s.ownerID = ownerID
	s.lastError = ""
	var entry compiledRelayRule
	for _, candidate := range s.rules {
		if candidate.rule.OwnerID == ownerID && candidate.rule.SourceID == source {
			entry = candidate
			break
		}
	}
	s.mu.Unlock()
	if entry.rule.ID == "" {
		return nil
	}
	lock := s.ruleLock(entry.rule.ID)
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	entry = s.rules[entry.rule.ID]
	s.mu.Unlock()
	rule := entry.rule
	if !rule.Enabled || rule.OwnerID != ownerID || rule.SourceID != source || message.Date < int(rule.StartedAt.Unix()) {
		return nil
	}
	resource, ok := tguser.RelayResource(message, tguser.Channel{ID: strconv.FormatInt(peer.ChannelID, 10), Title: rule.SourceTitle})
	if !ok {
		return nil
	}
	text := resource.Text
	for _, link := range resource.Links {
		text += "\n" + link.URL
	}
	if entry.include != nil && !entry.include.MatchString(text) || entry.exclude != nil && entry.exclude.MatchString(text) {
		return nil
	}
	key := "message:" + rule.ID + ":" + strconv.Itoa(message.ID)
	state, err := s.store.LoadTelegramRelayState(ctx, ownerID, key)
	if err != nil {
		return err
	}
	var seen []string
	if state != "" {
		if err := json.Unmarshal([]byte(state), &seen); err != nil {
			return fmt.Errorf("读取转发去重状态: %w", err)
		}
	}
	links := make([]tguser.ChannelLink, 0, len(resource.Links))
	for _, link := range resource.Links {
		if slices.Contains(rule.LinkTypes, link.Type) && !slices.Contains(seen, link.URL) {
			links = append(links, link)
			seen = append(seen, link.URL)
		}
	}
	if len(links) == 0 {
		return nil
	}
	cards, cardError := tguser.RelayCardText(resource, links)
	if cardError != nil && !errors.Is(cardError, tguser.ErrRelayCardTooLong) {
		return cardError
	}
	jobs := make([]model.TelegramRelayJob, 0, len(cards))
	now := time.Now().UTC()
	for _, text := range cards {
		randomID, err := relayRandomID()
		if err != nil {
			return err
		}
		card := tguser.RelayPayload{Text: text}
		if media, ok := message.Media.(*tg.MessageMediaPhoto); ok {
			if photo, ok := media.Photo.(*tg.Photo); ok {
				card.Photo = &tguser.RelayPhoto{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: slices.Clone(photo.FileReference)}
			}
		}
		payload, err := json.Marshal(card)
		if err != nil {
			return err
		}
		fingerprint := sha256.Sum256([]byte(text))
		jobs = append(jobs, model.TelegramRelayJob{RuleID: rule.ID, MessageID: message.ID, Fingerprint: hex.EncodeToString(fingerprint[:]), Payload: string(payload), RandomID: randomID, Due: now, CreatedAt: now})
	}
	encoded, err := json.Marshal(seen)
	if err != nil {
		return err
	}
	if err := s.store.CommitTelegramRelayUpdate(ctx, ownerID, key, string(encoded), jobs); err != nil {
		return err
	}
	if cardError != nil {
		s.logs.Append("Telegram", rule.ID, "频道转发", "WARNING", rule.Name+"：已跳过无法完整投递的超长资源链接，其他链接继续投递")
	}
	select {
	case s.wakeSend <- struct{}{}:
	default:
	}
	if len(jobs) > 0 {
		s.logs.Append("Telegram", rule.ID, "频道转发", "INFO", fmt.Sprintf("%s：收到资源消息，已加入%d条投递任务", rule.Name, len(jobs)))
	}
	return nil
}

func relayID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
func relayRandomID() (int64, error) {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, err
	}
	id := int64(binary.LittleEndian.Uint64(value[:]))
	if id == 0 {
		id = 1
	}
	return id, nil
}

func (s *TelegramRelay) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range s.rules {
		if rule.rule.Enabled {
			return true
		}
	}
	return false
}

func (s *TelegramRelay) Run(ctx context.Context) error {
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error { return s.watch(ctx) })
	group.Go(func() error { return s.sendLoop(ctx) })
	return group.Wait()
}

func (s *TelegramRelay) watch(ctx context.Context) error {
	for ctx.Err() == nil {
		if !s.active() || !s.transport.View().HasSession {
			select {
			case <-ctx.Done():
				return nil
			case <-s.wakeWatch:
			case <-time.After(5 * time.Second):
			}
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- s.transport.RunRelay(runCtx, s.store, s.receive) }()
		var err error
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return nil
		case <-s.wakeWatch:
			cancel()
			<-done
			continue
		case err = <-done:
			cancel()
		}
		if err != nil && ctx.Err() == nil {
			message := "Telegram连接中断，等待重新连接"
			if errors.Is(err, tguser.ErrLogin) {
				message = "Telegram登录已失效，请重新登录"
			}
			var operation *tguser.OperationError
			if errors.As(err, &operation) {
				message = operation.Message
				if operation.Code == "update_gap" {
					message = "频道积压超出可恢复窗口，已处理可获取的新消息；更早的消息可能缺失"
				}
			}
			s.mu.Lock()
			changed := s.lastError != message
			s.lastError = message
			s.mu.Unlock()
			if changed {
				s.logs.Append("Telegram", "", "频道转发", "WARNING", message)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.wakeWatch:
		case <-time.After(10 * time.Second):
		}
	}
	return nil
}

func (s *TelegramRelay) sendLoop(ctx context.Context) error {
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.wakeSend:
		case <-timer.C:
		}
		if !s.transport.RelayConnected() {
			continue
		}
		s.mu.Lock()
		owner, cooldown := s.ownerID, s.cooldown
		s.mu.Unlock()
		if owner <= 0 || time.Now().Before(cooldown) {
			continue
		}
		if err := s.sendDue(ctx, owner); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

func (s *TelegramRelay) sendDue(ctx context.Context, ownerID int64) error {
	jobs, err := s.store.DueTelegramRelayJobs(ctx, ownerID, time.Now(), 100)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(2)
	for _, job := range jobs {
		s.mu.Lock()
		rule := s.rules[job.RuleID].rule
		s.mu.Unlock()
		if seen[rule.TargetID] {
			continue
		}
		seen[rule.TargetID] = true
		job := job
		group.Go(func() error { return s.sendJob(ctx, ownerID, job, rule) })
	}
	return group.Wait()
}

func (s *TelegramRelay) sendJob(ctx context.Context, ownerID int64, job model.TelegramRelayJob, expected model.TelegramRelayRule) error {
	lock := s.ruleLock(job.RuleID)
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	rule, exists := s.rules[job.RuleID]
	s.mu.Unlock()
	if !exists || !rule.rule.Enabled || rule.rule.OwnerID != ownerID || rule.rule.SourceID != expected.SourceID || rule.rule.TargetID != expected.TargetID || !rule.rule.StartedAt.Equal(expected.StartedAt) {
		return nil
	}
	var payload tguser.RelayPayload
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		s.logs.Append("Telegram", job.RuleID, "频道转发", "WARNING", rule.rule.Name+"：已跳过损坏的待发送消息")
		return s.store.RemoveTelegramRelayJob(ctx, job.ID)
	}
	opCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err := s.transport.RelaySend(opCtx, ownerID, rule.rule.TargetID, payload, job.RandomID)
	cancel()
	if ctx.Err() != nil {
		return nil
	}
	if err == nil {
		if err := s.store.RemoveTelegramRelayJob(ctx, job.ID); err != nil {
			return err
		}
		s.logs.Append("Telegram", job.RuleID, "频道转发", "INFO", rule.rule.Name+"：消息已投递到 "+rule.rule.TargetTitle)
		return nil
	}
	message := "Telegram投递失败，请检查目标权限或稍后重试"
	due := time.Now().Add(time.Duration(min(120, 5*(1<<min(job.Attempts, 5)))) * time.Second)
	var detail *tguser.OperationError
	if errors.As(err, &detail) && detail.RetryAt != nil {
		due = *detail.RetryAt
		message = "Telegram限流，已安排稍后投递"
		if detail.Code == "flood_wait" {
			s.mu.Lock()
			if due.After(s.cooldown) {
				s.cooldown = due
			}
			s.mu.Unlock()
		}
	} else if errors.Is(err, tguser.ErrBusy) {
		message = "Telegram正在执行账号操作，稍后继续投递"
	} else if errors.Is(err, tguser.ErrLogin) {
		message = "Telegram登录已失效，请重新登录"
	}
	if job.Attempts == 0 {
		s.logs.Append("Telegram", job.RuleID, "频道转发", "WARNING", rule.rule.Name+"："+message+"；保留消息稍后继续")
	}
	return s.store.DeferTelegramRelayJob(ctx, job.ID, due)
}
