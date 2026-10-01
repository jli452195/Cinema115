package service

import (
	"context"
	"errors"
	"path/filepath"
	"strm115/backend/internal/storage"
	"testing"
)

func TestLoginClientMetadataPersistsWithoutCredentialRevision(t *testing.T) {
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
	id, err := s.AddPrimary("主账号")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; SEID=first"
	if err := s.Update(id, 1, "主账号", &cookie); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := s.Get(id)
	if err := s.SupplementLoginClient(snapshot, "115ipad"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenP115AccountsSQLite(ctx, db, dir, "secret")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := reopened.Get(id)
	if got.LoginClient != "115ipad" || got.Cookie != snapshot.Cookie || got.Revision != snapshot.Revision {
		t.Fatal("metadata changed credential identity or did not persist")
	}
	if err := s.Update(id, got.Revision, "新名称", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(id)
	if got.LoginClient != "115ipad" {
		t.Fatal("renaming lost metadata")
	}
	cookie = "UID=123_B; SEID=second"
	if err := s.Update(id, got.Revision, got.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.SupplementLoginClient(snapshot, "ios"); !errors.Is(err, ErrAccountChanged) {
		t.Fatal("stale detection accepted", err)
	}
	got, _ = s.Get(id)
	if got.LoginClient != "" {
		t.Fatal("new Cookie inherited old client")
	}
	if err := s.SupplementLoginClient(got, "ios"); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if err := s.Update(id, got.Revision, got.Name, &empty); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(id)
	if got.LoginClient != "" {
		t.Fatal("cleared credential retained metadata")
	}
}

func TestLoginClientMetadataWriteFailureLeavesMemoryUnchanged(t *testing.T) {
	store := &failingAccountStore{}
	s, err := newP115Accounts(store, "UID=123_A")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := s.Get("default")
	store.err = errors.New("write failed")
	if err := s.SupplementLoginClient(snapshot, "115ipad"); err == nil {
		t.Fatal("expected storage failure")
	}
	got, _ := s.Get("default")
	if got != snapshot {
		t.Fatal("failed metadata save changed memory")
	}
}
