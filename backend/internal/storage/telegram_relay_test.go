package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func relayTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, path := testStore(t)
	if err := s.InitTelegramRelay(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func relayTestRule(id, source string, owner int64) model.TelegramRelayRule {
	if !strings.Contains(source, ":") {
		source = "channel:" + source
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	return model.TelegramRelayRule{ID: id, Name: "频道 " + id, OwnerID: owner, SourceID: source, SourceTitle: "来源 " + source, TargetID: "user:200", TargetTitle: "目标 Bot", Enabled: true, LinkTypes: []string{"115", "ed2k"}, StartedAt: now, UpdatedAt: now}
}

func relayTestJob(rule, fingerprint string, messageID int) model.TelegramRelayJob {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return model.TelegramRelayJob{RuleID: rule, MessageID: messageID, Fingerprint: fingerprint, RandomID: 9000 + int64(messageID), Payload: `{"text":"test card"}`, Due: now, CreatedAt: now}
}

func relayJobCount(t *testing.T, s *Store, rule string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM telegram_relay_jobs WHERE rule_id=?`, rule).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestTelegramRelayPendingRestartsDeduplicatesAndClearsAfterSend(t *testing.T) {
	s, path := relayTestStore(t)
	ctx := context.Background()
	rule, job := relayTestRule("first", "100", 42), relayTestJob("first", "link-a", 1)
	if err := s.SaveTelegramRelayRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitTelegramRelayUpdate(ctx, 42, "message:first:1", "seen-1", []model.TelegramRelayJob{job}); err != nil {
		t.Fatal(err)
	}
	duplicate := job
	duplicate.RandomID = 999999
	duplicate.Payload = "must not replace original card"
	if err := s.CommitTelegramRelayUpdate(ctx, 42, "message:first:1", "seen-2", []model.TelegramRelayJob{duplicate}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	s = reopened
	if err := s.InitTelegramRelay(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetTelegramRelayRule(ctx, rule.ID)
	if err != nil || !reflect.DeepEqual(stored, rule) {
		t.Fatal("rule changed after restart", stored, err)
	}
	state, err := s.LoadTelegramRelayState(ctx, 42, "message:first:1")
	if err != nil || state != "seen-2" {
		t.Fatal("dedup checkpoint lost", state, err)
	}
	jobs, err := s.DueTelegramRelayJobs(ctx, 42, job.Due.Add(time.Second), 20)
	if err != nil || len(jobs) != 1 || jobs[0].RandomID != job.RandomID || jobs[0].Payload != job.Payload {
		t.Fatal("pending identity changed on restart/replay", jobs, err)
	}
	id, due := jobs[0].ID, job.Due.Add(time.Minute)
	if err := s.DeferTelegramRelayJob(ctx, id, due); err != nil {
		t.Fatal(err)
	}
	if jobs, err := s.DueTelegramRelayJobs(ctx, 42, due.Add(-time.Second), 20); err != nil || len(jobs) != 0 {
		t.Fatal("retry ignored deadline", jobs, err)
	}
	jobs, err = s.DueTelegramRelayJobs(ctx, 42, due.Add(time.Second), 20)
	if err != nil || len(jobs) != 1 || jobs[0].RandomID != job.RandomID || jobs[0].Attempts != 1 || jobs[0].Payload != job.Payload {
		t.Fatal("retry changed task identity", jobs, err)
	}
	if err := s.RemoveTelegramRelayJob(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.DeferTelegramRelayJob(ctx, id, due.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if count := relayJobCount(t, s, rule.ID); count != 0 {
		t.Fatal("successful send retained history or payload", count)
	}
	state, err = s.LoadTelegramRelayState(ctx, 42, "message:first:1")
	if err != nil || state != "seen-2" {
		t.Fatal("queue cleanup removed replay checkpoint", state, err)
	}
}

func TestTelegramRelayScopePauseAndIdentityReset(t *testing.T) {
	s, _ := relayTestStore(t)
	ctx := context.Background()
	first, other := relayTestRule("first", "100", 42), relayTestRule("other", "200", 43)
	for _, rule := range []model.TelegramRelayRule{first, other} {
		if err := s.SaveTelegramRelayRule(ctx, rule); err != nil {
			t.Fatal(err)
		}
	}
	one, two := relayTestJob(first.ID, "one", 1), relayTestJob(other.ID, "two", 2)
	if err := s.CommitTelegramRelayUpdate(ctx, 42, "channel:100", "first-state", []model.TelegramRelayJob{one, two}); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitTelegramRelayUpdate(ctx, 43, "channel:200", "other-state", []model.TelegramRelayJob{two}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []struct {
		owner int64
		key   string
	}{{42, "message:first:1"}, {43, "message:other:2"}, {43, "message:other-extra:2"}} {
		if err := s.CommitTelegramRelayUpdate(ctx, value.owner, value.key, "seen", nil); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.DueTelegramRelayJobs(ctx, 42, time.Now().Add(time.Hour), 20)
	if err != nil || len(jobs) != 1 || jobs[0].RuleID != first.ID {
		t.Fatal("another owner queued or visible", jobs, err)
	}
	first.Enabled = false
	if err := s.SaveTelegramRelayRule(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitTelegramRelayUpdate(ctx, 42, "channel:100", "paused-state", []model.TelegramRelayJob{relayTestJob(first.ID, "paused", 3)}); err != nil {
		t.Fatal(err)
	}
	if state, err := s.LoadTelegramRelayState(ctx, 42, "channel:100"); err != nil || state != "first-state" {
		t.Fatal("paused callback advanced pts", state, err)
	}
	if jobs, err := s.DueTelegramRelayJobs(ctx, 42, time.Now().Add(time.Hour), 20); err != nil || len(jobs) != 0 {
		t.Fatal("paused rule runnable", jobs, err)
	}
	if count := relayJobCount(t, s, first.ID); count != 1 {
		t.Fatal("pause removed pending job or added new job", count)
	}
	first.Enabled = true
	if err := s.SaveTelegramRelayRule(ctx, first); err != nil {
		t.Fatal(err)
	}
	if jobs, err := s.DueTelegramRelayJobs(ctx, 42, time.Now().Add(time.Hour), 20); err != nil || len(jobs) != 1 {
		t.Fatal("resume lost original pending job", jobs, err)
	}
	first.TargetID = "channel:300"
	if err := s.SaveTelegramRelayRule(ctx, first); err != nil {
		t.Fatal(err)
	}
	if count := relayJobCount(t, s, first.ID); count != 0 {
		t.Fatal("old card was retargeted", count)
	}
	for _, key := range []string{"channel:100", "message:first:1"} {
		if value, err := s.LoadTelegramRelayState(ctx, 42, key); err != nil || value != "" {
			t.Fatal("changed target retained old checkpoint", key, value, err)
		}
	}
	if err := s.DeleteTelegramRelayRule(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if count := relayJobCount(t, s, other.ID); count != 0 {
		t.Fatal("delete left pending cards", count)
	}
	for _, key := range []string{"channel:200", "message:other:2"} {
		if value, err := s.LoadTelegramRelayState(ctx, 43, key); err != nil || value != "" {
			t.Fatal("delete left checkpoint", key, value, err)
		}
	}
	if value, err := s.LoadTelegramRelayState(ctx, 43, "message:other-extra:2"); err != nil || value != "seen" {
		t.Fatal("delete touched another rule", value, err)
	}
	if _, err := s.GetTelegramRelayRule(ctx, other.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("deleted rule still exists", err)
	}
}

func TestTelegramRelayTransactionRollbackValidationAndCancellation(t *testing.T) {
	s, _ := relayTestStore(t)
	ctx := context.Background()
	rule, duplicate := relayTestRule("first", "100", 42), relayTestRule("duplicate", "100", 42)
	if err := s.SaveTelegramRelayRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTelegramRelayRule(ctx, duplicate); err == nil {
		t.Fatal("one source acquired multiple target rules")
	}
	if err := s.CommitTelegramRelayUpdate(ctx, 42, "global", "original", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER telegram_relay_test_abort BEFORE INSERT ON telegram_relay_jobs WHEN NEW.fingerprint='reject' BEGIN SELECT RAISE(ABORT,'fixture rejection'); END;`); err != nil {
		t.Fatal(err)
	}
	jobs := []model.TelegramRelayJob{relayTestJob(rule.ID, "first", 1), relayTestJob(rule.ID, "reject", 2)}
	if err := s.CommitTelegramRelayUpdate(ctx, 42, "global", "must rollback", jobs); err == nil {
		t.Fatal("failed batch accepted")
	}
	if state, err := s.LoadTelegramRelayState(ctx, 42, "global"); err != nil || state != "original" {
		t.Fatal("checkpoint advanced beyond rejected card", state, err)
	}
	if count := relayJobCount(t, s, rule.ID); count != 0 {
		t.Fatal("partial card survived failed transaction", count)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.CommitTelegramRelayUpdate(canceled, 42, "global", "canceled", []model.TelegramRelayJob{relayTestJob(rule.ID, "canceled", 3)}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled write continued", err)
	}
	invalid := rule
	invalid.LinkTypes = []string{"115", "115"}
	if err := s.SaveTelegramRelayRule(ctx, invalid); err == nil {
		t.Fatal("duplicate link type accepted")
	}
}

func TestTelegramRelayTargetHeadsAreFairAndOrdered(t *testing.T) {
	s, _ := relayTestStore(t)
	ctx := context.Background()
	first, shared, other := relayTestRule("one", "100", 42), relayTestRule("shared", "101", 42), relayTestRule("two", "102", 42)
	other.TargetID = "chat:300"
	for _, rule := range []model.TelegramRelayRule{first, shared, other} {
		if err := s.SaveTelegramRelayRule(ctx, rule); err != nil {
			t.Fatal(err)
		}
	}
	jobs := make([]model.TelegramRelayJob, 0, 152)
	for i := 1; i <= 150; i++ {
		jobs = append(jobs, relayTestJob(first.ID, "card", i))
	}
	jobs = append(jobs, relayTestJob(shared.ID, "shared", 151), relayTestJob(other.ID, "other", 152))
	if err := s.CommitTelegramRelayUpdate(ctx, 42, "global", "position", jobs); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Minute)
	heads, err := s.DueTelegramRelayJobs(ctx, 42, now, 100)
	if err != nil || len(heads) != 2 || heads[0].MessageID != 1 || heads[1].MessageID != 152 {
		t.Fatal("large backlog starved another target", heads, err)
	}
	if err := s.DeferTelegramRelayJob(ctx, heads[0].ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	delayed, err := s.DueTelegramRelayJobs(ctx, 42, now, 100)
	if err != nil || len(delayed) != 1 || delayed[0].MessageID != 152 {
		t.Fatal("new message overtook delayed target head", delayed, err)
	}
	for _, head := range heads {
		if err := s.RemoveTelegramRelayJob(ctx, head.ID); err != nil {
			t.Fatal(err)
		}
	}
	next, err := s.DueTelegramRelayJobs(ctx, 42, now, 100)
	if err != nil || len(next) != 1 || next[0].MessageID != 2 {
		t.Fatal("next pending job blocked after success", next, err)
	}
	first.Enabled = false
	if err := s.SaveTelegramRelayRule(ctx, first); err != nil {
		t.Fatal(err)
	}
	next, err = s.DueTelegramRelayJobs(ctx, 42, now, 100)
	if err != nil || len(next) != 1 || next[0].RuleID != shared.ID {
		t.Fatal("paused source blocked another rule targeting same peer", next, err)
	}
}

func TestTelegramRelayInitialCheckpointSurvivesPauseBeforeFirstConnection(t *testing.T) {
	s, _ := relayTestStore(t)
	ctx := context.Background()
	rule := relayTestRule("first", "100", 42)
	rule.SourcePts = 20
	rule.Enabled = false
	if err := s.SaveTelegramRelayRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if state, err := s.LoadTelegramRelayState(ctx, 42, rule.SourceID); err != nil || state != "20" {
		t.Fatal("initial pts was not atomic with rule", state, err)
	}
	rule.Enabled = true
	rule.SourcePts = 90
	if err := s.SaveTelegramRelayRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if state, err := s.LoadTelegramRelayState(ctx, 42, rule.SourceID); err != nil || state != "20" {
		t.Fatal("early pause/resume skipped messages", state, err)
	}
	encoded, err := json.Marshal(rule)
	if err != nil || strings.Contains(strings.ToLower(string(encoded)), "sourcepts") {
		t.Fatal("internal baseline became setting", string(encoded), err)
	}
	rule.TargetID = "chat:999"
	rule.SourcePts = 100
	if err := s.SaveTelegramRelayRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if state, err := s.LoadTelegramRelayState(ctx, 42, rule.SourceID); err != nil || state != "100" {
		t.Fatal("changed target retained old stream baseline", state, err)
	}
}

func TestTelegramRelayInitializationClearsOldHistoryOnly(t *testing.T) {
	s, _ := relayTestStore(t)
	ctx := context.Background()
	rule := relayTestRule("first", "100", 42)
	if err := s.SaveTelegramRelayRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	jobs := []model.TelegramRelayJob{relayTestJob(rule.ID, "pending", 1), relayTestJob(rule.ID, "sent", 2), relayTestJob(rule.ID, "failed", 3)}
	if err := s.CommitTelegramRelayUpdate(ctx, 42, "global", "seen", jobs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE telegram_relay_jobs SET status=CASE WHEN message_id=2 THEN 'sent' ELSE 'failed' END WHERE message_id>1`); err != nil {
		t.Fatal(err)
	}
	if err := s.InitTelegramRelay(ctx); err != nil {
		t.Fatal(err)
	}
	if count := relayJobCount(t, s, rule.ID); count != 1 {
		t.Fatal("old history retained or pending deleted", count)
	}
	if value, err := s.LoadTelegramRelayState(ctx, 42, "global"); err != nil || value != "seen" {
		t.Fatal("history cleanup removed checkpoint", value, err)
	}
	due, err := s.DueTelegramRelayJobs(ctx, 42, time.Now().Add(time.Hour), 10)
	if err != nil || len(due) != 1 || due[0].MessageID != 1 {
		t.Fatal("pending recovery broken after cleanup", due, err)
	}
}
