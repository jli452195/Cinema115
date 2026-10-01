package service

import (
	"context"
	"errors"
	"path/filepath"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"testing"
)

func TestAccountSummaryPersistenceAndCredentialIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "account.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddPrimary("main")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; SEID=test"
	if err = s.Update(id, 1, "main", &cookie); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Get(id)
	summary := model.P115Summary{VIP: "vip", SpaceKnown: true, RemainingBytes: 12345, CheckedAt: "2026-10-01T00:00:00Z"}
	if err = s.SaveSummary(a, summary); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reopened.Get(id)
	if got.Summary != summary || got.Revision != a.Revision || reopened.ManagedList()[0].Summary != summary {
		t.Fatal("metadata persistence changed revision or lost values")
	}
	cookie = "UID=123_B; SEID=changed"
	if err = s.Update(id, a.Revision, "main", &cookie); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(id)
	if got.Summary != (model.P115Summary{}) {
		t.Fatal("new credential retained stale metadata")
	}
	if err = s.SaveSummary(a, summary); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale metadata accepted")
	}
	if err = s.SaveSummary(got, summary); err != nil {
		t.Fatal(err)
	}
	cookie = ""
	if err = s.Update(id, got.Revision, "main", &cookie); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(id)
	if got.Summary != (model.P115Summary{}) {
		t.Fatal("clear retained metadata")
	}
}
