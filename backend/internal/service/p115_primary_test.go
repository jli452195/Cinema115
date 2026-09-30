package service

import (
	"context"
	"path/filepath"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

func TestPrimaryRolePersistsAndNeverFallsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts, err := OpenP115AccountsSQLite(ctx, db, dir, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := accounts.Add("small")
	if err != nil {
		t.Fatal(err)
	}
	secondaryCookie := "UID=22_A; SEID=secondary"
	if err := accounts.Update(secondary, 1, "small", &secondaryCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Primary(""); err == nil || accounts.PrimaryID() != "" {
		t.Fatal("promoted the only secondary")
	}
	primary, err := accounts.AddPrimary("main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.AddPrimary("duplicate"); err == nil {
		t.Fatal("allowed two primary accounts")
	}
	if _, err := accounts.Primary(""); err == nil {
		t.Fatal("used unconfigured primary")
	}
	cookie := "UID=11_A; SEID=original"
	if err := accounts.Update(primary, 1, "main", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := accounts.ValidatePrimary(secondary); err == nil {
		t.Fatal("secondary accepted for resource management")
	}
	if err := accounts.ValidateSecondary(primary); err == nil {
		t.Fatal("primary accepted as personal playback account")
	}
	snapshot, err := accounts.Primary("")
	if err != nil || snapshot.ID != primary {
		t.Fatal("primary not resolved", err)
	}
	reopened, err := OpenP115AccountsSQLite(ctx, db, dir, "test-secret")
	if err != nil || reopened.PrimaryID() != primary {
		t.Fatal("primary role not persisted", err)
	}
	empty := ""
	if err := reopened.Update(primary, 2, "main", &empty); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Primary(""); err == nil {
		t.Fatal("fell back after credential removal")
	}
	if snapshot.Cookie != cookie {
		t.Fatal("mutated task credential snapshot")
	}
	reopened, err = OpenP115AccountsSQLite(ctx, db, dir, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	other := "UID=99_A; SEID=other"
	if err := reopened.Update(primary, 3, "main", &other); err != nil {
		t.Fatal(err)
	}
	if reopened.PrimaryID() == primary {
		t.Fatal("replacement kept old feature directory identity")
	}
	if err := reopened.Delete(reopened.PrimaryID(), 4); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Primary(""); err == nil {
		t.Fatal("fell back after primary deletion")
	}
	newPrimary, err := reopened.AddPrimary("new")
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Update(newPrimary, 1, "new", &other); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Primary(primary); err == nil {
		t.Fatal("accepted old directory owner")
	}
}

func TestPrimaryChangeInvalidatesFeatureDirectories(t *testing.T) {
	accounts, err := newP115Accounts(&failingAccountStore{}, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("main")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=11_A"
	if err := accounts.Update(id, 1, "main", &cookie); err != nil {
		t.Fatal(err)
	}
	store, err := NewSessionStore(filepath.Join(t.TempDir(), "cloud.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := NewCloudDownloadSettings(store)
	if err != nil {
		t.Fatal(err)
	}
	cloud.PrimaryID, cloud.ValidateAccount = accounts.PrimaryID, accounts.ValidatePrimary
	if _, err := cloud.Save(CloudDownloadConfig{TargetCID: "123", TargetPath: "/downloads"}); err != nil {
		t.Fatal(err)
	}
	if cloud.View().AccountID != id {
		t.Fatal("configuration did not pin directory owner")
	}
	if err := accounts.Delete(id, 2); err != nil {
		t.Fatal(err)
	}
	next, err := accounts.AddPrimary("new")
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.Update(next, 1, "new", &cookie); err != nil {
		t.Fatal(err)
	}
	view := cloud.View()
	if view.AccountID != next || view.TargetCID != "" || view.TargetPath != "" {
		t.Fatal("reused old account's directory", view)
	}
	if _, err := cloud.Save(CloudDownloadConfig{AccountID: id, TargetCID: "123", TargetPath: "/downloads"}); err == nil {
		t.Fatal("stale page saved another account's directory")
	}
}

func TestInstantTargetComesOnlyFromUserBinding(t *testing.T) {
	s, fake, config, _ := setupInstantService(t)
	forged := config
	forged.AccountID, forged.SourceAccountID, forged.TargetCID = config.SourceAccountID, config.AccountID, "999"
	if err := s.Apply(forged); err != nil {
		t.Fatal(err)
	}
	if s.Status().Config != config {
		t.Fatal("client changed source or destination outside user binding")
	}
	s.Playback.doc.Config.Bindings = nil
	if _, err := s.Run(context.Background(), config, "binding-removed-00001", &p115.ShareInfo{Code: "test"}, "1"); err == nil {
		t.Fatal("unbound task started")
	}
	if fake.calls != 0 {
		t.Fatal("unbound task wrote to drive")
	}
}
