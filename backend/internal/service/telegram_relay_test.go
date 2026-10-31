package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type relayDelivery struct {
	target   string
	payload  tguser.RelayPayload
	randomID int64
}
type relayTransportFake struct {
	mu           sync.Mutex
	owner        int64
	connected    bool
	prepareCalls int
	prepareError error
	deliveries   []relayDelivery
	send         func(context.Context, string) error
	sent         chan struct{}
}

func (f *relayTransportFake) View() tguser.View                 { return tguser.View{HasSession: true} }
func (*relayTransportFake) SavedRelayPeers() []tguser.RelayPeer { return nil }
func (f *relayTransportFake) ListRelayPeers(context.Context, string) (tguser.RelayPeerPage, error) {
	return tguser.RelayPeerPage{}, nil
}
func (*relayTransportFake) RelayAvatar(context.Context, string, string) ([]byte, string, error) {
	return nil, "", tguser.ErrAvatarUnavailable
}
func (f *relayTransportFake) PrepareRelay(_ context.Context, source, target, _, _ string) (tguser.RelayIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepareCalls++
	if f.prepareError != nil {
		return tguser.RelayIdentity{}, f.prepareError
	}
	return tguser.RelayIdentity{OwnerID: f.owner, SourceID: source, SourceTitle: "来源", TargetID: target, TargetTitle: "目标"}, nil
}
func (f *relayTransportFake) RunRelay(ctx context.Context, _ tguser.RelayStateStore, handle func(context.Context, int64, *tg.Message) error) error {
	f.mu.Lock()
	f.connected = true
	owner := f.owner
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.connected = false; f.mu.Unlock() }()
	if err := handle(ctx, owner, nil); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
func (f *relayTransportFake) RelayConnected() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connected
}
func (f *relayTransportFake) RelaySend(ctx context.Context, owner int64, target string, payload tguser.RelayPayload, id int64) error {
	f.mu.Lock()
	if owner != f.owner {
		f.mu.Unlock()
		return tguser.ErrLogin
	}
	f.deliveries = append(f.deliveries, relayDelivery{target, payload, id})
	send := f.send
	sent := f.sent
	f.mu.Unlock()
	if send != nil {
		if err := send(ctx, target); err != nil {
			return err
		}
	}
	if sent != nil {
		select {
		case sent <- struct{}{}:
		default:
		}
	}
	return nil
}

type relayFixtureStore struct {
	*storage.Store
	inspect *sql.DB
}

func relayFixture(t *testing.T) (*TelegramRelay, *relayFixtureStore, *relayTransportFake) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "relay.db")
	database, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	inspect, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db := &relayFixtureStore{database, inspect}
	t.Cleanup(func() { inspect.Close(); db.Close() })
	f := &relayTransportFake{owner: 42}
	s, err := NewTelegramRelay(context.Background(), db, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, db, f
}
func relayRuleInput(source, target string) TelegramRelayRuleInput {
	return TelegramRelayRuleInput{SourceID: source, TargetID: target, Enabled: true, LinkTypes: []string{"115", "ed2k"}}
}
func saveRelayRule(t *testing.T, s *TelegramRelay, input TelegramRelayRuleInput) model.TelegramRelayRule {
	t.Helper()
	rule, err := s.Save(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}
func relayMessage(id int, text string) *tg.Message {
	return &tg.Message{ID: id, Date: int(time.Now().Unix()) + 1, PeerID: &tg.PeerChannel{ChannelID: 100}, Message: text}
}
func relayRecords(t *testing.T, s *TelegramRelay, id string) []model.TelegramRelayJob {
	t.Helper()
	// Inspect the isolated test DB directly; production has no history API.
	rows, err := s.store.(*relayFixtureStore).inspect.Query(`SELECT id,rule_id,message_id,payload,random_id,attempts,due FROM telegram_relay_jobs WHERE rule_id=? ORDER BY id DESC`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var jobs []model.TelegramRelayJob
	for rows.Next() {
		var job model.TelegramRelayJob
		var due int64
		if err := rows.Scan(&job.ID, &job.RuleID, &job.MessageID, &job.Payload, &job.RandomID, &job.Attempts, &due); err != nil {
			t.Fatal(err)
		}
		job.Due = time.UnixMilli(due)
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return jobs
}

func TestTelegramRelayQueueDedupEditAndDelivery(t *testing.T) {
	s, _, f := relayFixture(t)
	ctx := context.Background()
	rule := saveRelayRule(t, s, relayRuleInput("channel:100", "user:200"))
	message := relayMessage(1, "影片 1080p\nhttps://115.com/s/abc123\ned2k://|file|A.mkv|123|0123456789ABCDEF0123456789ABCDEF|/")
	message.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 9, AccessHash: 12, FileReference: []byte{1, 2}}}
	for i := 0; i < 2; i++ {
		if err := s.receive(ctx, 42, message); err != nil {
			t.Fatal(err)
		}
	}
	jobs := relayRecords(t, s, rule.ID)
	if len(jobs) != 1 || len(f.deliveries) != 0 {
		t.Fatal("save/receive sent messages or duplicate queue", jobs)
	}
	var card tguser.RelayPayload
	if err := json.Unmarshal([]byte(jobs[0].Payload), &card); err != nil || card.Photo == nil || card.Photo.ID != 9 {
		t.Fatal("photo not retained", card, err)
	}
	message.Message += "\n编辑文案"
	if err := s.receive(ctx, 42, message); err != nil {
		t.Fatal(err)
	}
	if len(relayRecords(t, s, rule.ID)) != 1 {
		t.Fatal("text edit replayed old resources")
	}
	message.Message += "\nhttps://115.com/s/new456"
	if err := s.receive(ctx, 42, message); err != nil {
		t.Fatal(err)
	}
	jobs = relayRecords(t, s, rule.ID)
	if len(jobs) != 2 {
		t.Fatal("new edited link missing", jobs)
	}
	if strings.Contains(jobs[0].Payload, "abc123") || !strings.Contains(jobs[0].Payload, "new456") {
		t.Fatal("edited card repeats old link", jobs[0].Payload)
	}
	for i := 0; i < 2; i++ {
		if err := s.sendDue(ctx, 42); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.deliveries) != 2 || len(relayRecords(t, s, rule.ID)) != 0 {
		t.Fatal("successful messages retained in queue")
	}
	if f.deliveries[0].randomID != jobs[1].RandomID || f.deliveries[1].randomID != jobs[0].RandomID {
		t.Fatal("delivery order or random id changed")
	}
}

func TestTelegramRelayValidationFilteringAndOwnerIsolation(t *testing.T) {
	s, _, f := relayFixture(t)
	ctx := context.Background()
	bad := relayRuleInput("channel:100", "user:200")
	bad.IncludePattern = "["
	if _, err := s.Save(ctx, bad); !errors.Is(err, ErrTelegramRelayInput) || f.prepareCalls != 0 {
		t.Fatal("invalid regex reached Telegram", err)
	}
	input := relayRuleInput("channel:100", "channel:200")
	input.IncludePattern = "1080p"
	input.ExcludePattern = "广告"
	input.LinkTypes = []string{"115"}
	rule := saveRelayRule(t, s, input)
	if _, err := s.Save(ctx, relayRuleInput("channel:100", "user:201")); !errors.Is(err, ErrTelegramRelayInput) {
		t.Fatal("duplicate source accepted", err)
	}
	if _, err := s.Save(ctx, relayRuleInput("channel:200", "channel:100")); !errors.Is(err, ErrTelegramRelayInput) {
		t.Fatal("relay cycle accepted", err)
	}
	for _, text := range []string{"影片 720p https://115.com/s/abc123", "广告 1080p https://115.com/s/abc123", "影片 1080p ed2k://|file|A.mkv|123|0123456789ABCDEF0123456789ABCDEF|/"} {
		if err := s.receive(ctx, 42, relayMessage(1, text)); err != nil {
			t.Fatal(err)
		}
	}
	for _, variant := range []string{"owner", "old", "protected", "outgoing", "other-channel"} {
		m := relayMessage(2, "影片 1080p https://115.com/s/abc123")
		owner := int64(42)
		switch variant {
		case "owner":
			owner = 43
		case "old":
			m.Date = int(rule.StartedAt.Unix()) - 1
		case "protected":
			m.Noforwards = true
		case "outgoing":
			m.Out = true
		case "other-channel":
			m.PeerID = &tg.PeerChannel{ChannelID: 999}
		}
		if err := s.receive(ctx, owner, m); err != nil {
			t.Fatal(err)
		}
	}
	if len(relayRecords(t, s, rule.ID)) != 0 {
		t.Fatal("filtered message queued")
	}
	if err := s.receive(ctx, 42, relayMessage(3, "影片 1080p https://115.com/s/abc123")); err != nil {
		t.Fatal(err)
	}
	if err := s.sendDue(ctx, 43); err != nil || len(f.deliveries) != 0 {
		t.Fatal("sent another owner's queue", err)
	}
	if len(relayRecords(t, s, rule.ID)) != 1 {
		t.Fatal("matching message missing")
	}
}

func TestTelegramRelayPauseResumeAndIdentityChange(t *testing.T) {
	s, db, f := relayFixture(t)
	ctx := context.Background()
	input := relayRuleInput("channel:100", "user:200")
	rule := saveRelayRule(t, s, input)
	if err := s.receive(ctx, 42, relayMessage(1, "https://115.com/s/abc123")); err != nil {
		t.Fatal(err)
	}
	stale := relayRecords(t, s, rule.ID)[0]
	input.ID = rule.ID
	input.Enabled = false
	paused := saveRelayRule(t, s, input)
	if !paused.StartedAt.Equal(rule.StartedAt) {
		t.Fatal("pause reset start point")
	}
	if err := s.sendDue(ctx, 42); err != nil || len(f.deliveries) != 0 {
		t.Fatal("paused rule sent", err)
	}
	if err := s.receive(ctx, 42, relayMessage(2, "https://115.com/s/new456")); err != nil {
		t.Fatal(err)
	}
	if len(relayRecords(t, s, rule.ID)) != 1 {
		t.Fatal("paused rule accepted new message")
	}
	input.Enabled = true
	saveRelayRule(t, s, input)
	if err := s.sendDue(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if len(f.deliveries) != 1 {
		t.Fatal("resume lost pending message")
	}
	input.TargetID = "user:300"
	saveRelayRule(t, s, input)
	if len(relayRecords(t, s, rule.ID)) != 0 {
		t.Fatal("changed target kept old jobs")
	}
	state, err := db.LoadTelegramRelayState(ctx, 42, "message:"+rule.ID+":1")
	if err != nil || state != "" {
		t.Fatal("changed identity kept old seen state", state, err)
	}
	if err := s.sendJob(ctx, 42, stale, rule); err != nil || len(f.deliveries) != 1 {
		t.Fatal("stale job sent to new target", err)
	}
	if err := s.Delete(ctx, rule.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.List(ctx)
	if err != nil || len(view.Rules) != 0 {
		t.Fatal("delete left cached rule", view, err)
	}
}

func TestTelegramRelayRetriesUseStableRandomIDs(t *testing.T) {
	s, db, f := relayFixture(t)
	ctx := context.Background()
	rule := saveRelayRule(t, s, relayRuleInput("channel:100", "user:200"))
	if err := s.receive(ctx, 42, relayMessage(1, "https://115.com/s/abc123")); err != nil {
		t.Fatal(err)
	}
	f.send = func(context.Context, string) error { return context.DeadlineExceeded }
	for i := 0; i < 5; i++ {
		job := relayRecords(t, s, rule.ID)[0]
		if err := s.sendJob(ctx, 42, job, rule); err != nil {
			t.Fatal(err)
		}
	}
	job := relayRecords(t, s, rule.ID)[0]
	if job.Attempts != 5 || len(f.deliveries) != 5 {
		t.Fatal("network failures lost pending message", job)
	}
	for _, delivery := range f.deliveries {
		if delivery.randomID != job.RandomID {
			t.Fatal("retry generated duplicate Telegram identity")
		}
	}
	restarted, err := NewTelegramRelay(ctx, db, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pending := relayRecords(t, restarted, rule.ID); len(pending) != 1 || pending[0].RandomID != job.RandomID || pending[0].Attempts != 5 {
		t.Fatal("restart lost retry state", pending)
	}
	f.send = nil
	if err := restarted.sendJob(ctx, 42, job, rule); err != nil {
		t.Fatal(err)
	}
	if len(relayRecords(t, restarted, rule.ID)) != 0 {
		t.Fatal("completed retry retained unnecessary history")
	}
}

func TestTelegramRelaySlowTargetAndFloodDoNotLoseJobs(t *testing.T) {
	s, _, f := relayFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a := saveRelayRule(t, s, relayRuleInput("channel:100", "user:200"))
	b := saveRelayRule(t, s, relayRuleInput("channel:101", "user:201"))
	for i, rule := range []model.TelegramRelayRule{a, b} {
		m := relayMessage(i+1, "https://115.com/s/abc123")
		if i == 1 {
			m.PeerID = &tg.PeerChannel{ChannelID: 101}
		}
		if err := s.receive(ctx, 42, m); err != nil {
			t.Fatal(rule.ID, err)
		}
	}
	healthy := make(chan struct{})
	f.send = func(ctx context.Context, target string) error {
		if target == "user:201" {
			close(healthy)
			return nil
		}
		select {
		case <-healthy:
			return context.DeadlineExceeded
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := s.sendDue(ctx, 42); err != nil || ctx.Err() != nil {
		t.Fatal("slow target blocked another target", err, ctx.Err())
	}
	if len(relayRecords(t, s, b.ID)) != 0 || len(relayRecords(t, s, a.ID)) != 1 {
		t.Fatal("target failures were coupled")
	}
	retryAt := time.Now().Add(time.Minute)
	f.send = func(context.Context, string) error {
		return &tguser.OperationError{Code: "flood_wait", Message: "limited", RetryAt: &retryAt}
	}
	if err := s.sendJob(ctx, 42, relayRecords(t, s, a.ID)[0], a); err != nil {
		t.Fatal(err)
	}
	job := relayRecords(t, s, a.ID)[0]
	if job.Due.Before(retryAt.Truncate(time.Millisecond)) || !s.cooldown.Equal(retryAt) {
		t.Fatal("flood wait discarded pending work", job)
	}
	shorter := time.Now().Add(10 * time.Second)
	f.send = func(context.Context, string) error {
		return &tguser.OperationError{Code: "flood_wait", RetryAt: &shorter}
	}
	if err := s.sendJob(ctx, 42, job, a); err != nil || !s.cooldown.Equal(retryAt) {
		t.Fatal("short flood wait shortened account cooldown", err)
	}
}

func TestTelegramRelayRestartDrainsQueueWithoutNewPostsAndStops(t *testing.T) {
	s, db, f := relayFixture(t)
	ctx := context.Background()
	rule := saveRelayRule(t, s, relayRuleInput("channel:100", "user:200"))
	if err := s.receive(ctx, 42, relayMessage(1, "https://115.com/s/abc123")); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewTelegramRelay(ctx, db, f, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.sent = make(chan struct{}, 1)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- restarted.Run(runCtx) }()
	select {
	case <-f.sent:
	case <-time.After(3 * time.Second):
		t.Fatal("restart required new post to drain pending queue")
	}
	deadline := time.Now().Add(time.Second)
	for len(relayRecords(t, restarted, rule.ID)) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("restart did not persist successful delivery")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay goroutines did not stop")
	}
	if len(relayRecords(t, restarted, rule.ID)) != 0 {
		t.Fatal("restart did not clear completed queue")
	}
}

func TestTelegramRelayCanPauseOfflineWithoutNetwork(t *testing.T) {
	s, _, f := relayFixture(t)
	ctx := context.Background()
	input := relayRuleInput("channel:100", "user:200")
	rule := saveRelayRule(t, s, input)
	f.prepareError = tguser.ErrLogin
	input.ID = rule.ID
	input.Enabled = false
	paused, err := s.Save(ctx, input)
	if err != nil || paused.Enabled || f.prepareCalls != 1 {
		t.Fatal("offline pause requires Telegram", err, f.prepareCalls)
	}
	input.Name = "改名"
	if _, err := s.Save(ctx, input); err != nil || f.prepareCalls != 1 {
		t.Fatal("offline metadata edit requires Telegram", err)
	}
	input.Enabled = true
	if _, err := s.Save(ctx, input); !errors.Is(err, tguser.ErrLogin) {
		t.Fatal("enable did not recheck identity", err)
	}
	view, err := s.List(ctx)
	if err != nil || view.Rules[0].Enabled {
		t.Fatal("failed enable changed persisted state", view, err)
	}
}

func TestTelegramRelayOversizedLinkDoesNotPoisonOtherUpdates(t *testing.T) {
	s, _, _ := relayFixture(t)
	ctx := context.Background()
	rule := saveRelayRule(t, s, relayRuleInput("channel:100", "user:200"))
	bad := "ed2k://|file|" + strings.Repeat("A", 3800) + "|123|0123456789ABCDEF0123456789ABCDEF|/"
	message := relayMessage(1, "影片\n"+bad+"\nhttps://115.com/s/abc123")
	for i := 0; i < 2; i++ {
		if err := s.receive(ctx, 42, message); err != nil {
			t.Fatal("bad content blocked update progress", err)
		}
	}
	jobs := relayRecords(t, s, rule.ID)
	if len(jobs) != 1 || strings.Contains(jobs[0].Payload, bad) || !strings.Contains(jobs[0].Payload, "abc123") {
		t.Fatal("valid resource lost with oversized link", jobs)
	}
	if err := s.receive(ctx, 42, relayMessage(2, "https://115.com/s/next456")); err != nil {
		t.Fatal(err)
	}
	if len(relayRecords(t, s, rule.ID)) != 2 {
		t.Fatal("later post blocked")
	}
}
