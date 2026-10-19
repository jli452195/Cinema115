package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type animeDownloadDirectoryFunc func(context.Context, string, string) (p115.DriveDirectory, error)

func (f animeDownloadDirectoryFunc) ReadDirectory(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	return f(ctx, cid, cookie)
}

type animeDownloadFixture struct {
	service  *AnimeAdminService
	accounts *P115Accounts
	dir      string
	path     string
	settings model.AnimeSettings
}

const animeDownloadTestCookie = "UID=123_A; SEID=private-download-snapshot"

func newAnimeDownloadFixture(t *testing.T) *animeDownloadFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "anime.db")
	db, err := storage.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	accounts, err := OpenP115AccountsSQLite(ctx, db, dir, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	// A healthy secondary exists throughout the tests to expose any fallback.
	secondary, err := accounts.Add("小号")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=456_A; SEID=secondary"
	if err := accounts.Update(secondary, 1, "小号", &cookie); err != nil {
		t.Fatal(err)
	}
	id, err := accounts.AddPrimary("主账号")
	if err != nil {
		t.Fatal(err)
	}
	cookie = animeDownloadTestCookie
	if err := accounts.Update(id, 1, "主账号", &cookie); err != nil {
		t.Fatal(err)
	}
	s := &AnimeAdminService{Store: db, Accounts: accounts, Directories: animeDownloadDirectoryFunc(func(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
		if err := ctx.Err(); err != nil {
			return p115.DriveDirectory{}, err
		}
		if cookie != animeDownloadTestCookie {
			return p115.DriveDirectory{}, errors.New("unexpected credential")
		}
		if cid == "0" {
			return p115.DriveDirectory{CID: cid, Path: "/"}, nil
		}
		return p115.DriveDirectory{CID: cid, Path: "/动漫/" + cid}, nil
	})}
	f := &animeDownloadFixture{service: s, accounts: accounts, dir: dir, path: path}
	f.saveSettings(t, "100")
	return f
}

func (f *animeDownloadFixture) saveSettings(t *testing.T, cid string) {
	t.Helper()
	a, err := f.accounts.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.service.Store.GetAnimeSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	settings, err := f.service.SaveSettings(context.Background(), "admin", fmt.Sprintf("settings_%d", old.Revision), AnimeSettingsInput{ExpectedRevision: old.Revision, DownloadCID: cid, ExpectedPrimaryAccountID: a.ID, ExpectedPrimaryAccountRevision: int64(a.Revision)})
	if err != nil {
		t.Fatal(err)
	}
	f.settings = settings
}

func (f *animeDownloadFixture) prepare(t *testing.T) *AnimeDownloadSession {
	t.Helper()
	s, err := f.service.PrepareDownloadTarget(context.Background(), f.settings.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAnimeDownloadTargetSafeFixedSnapshot(t *testing.T) {
	f := newAnimeDownloadFixture(t)
	session := f.prepare(t)
	target := session.Target()
	if target.AccountID != *f.settings.AccountID || target.AccountRevision != 2 || target.UID != "123" || target.SettingsRevision != f.settings.Revision || target.ParentCID != "100" || target.ParentPath != "/动漫/100" || session.cookie != animeDownloadTestCookie {
		t.Fatal("incorrect task snapshot")
	}
	copy := session.Target()
	copy.ParentCID, copy.AccountID = "999", "different"
	if session.Target() != target {
		t.Fatal("caller mutated runtime target")
	}
	*f.settings.DownloadCID = "777"
	if session.Target() != target {
		t.Fatal("retained settings pointers")
	}
	for _, value := range []any{target, session, *session} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{string(raw), fmt.Sprintf("%v %+v %#v", value, value, value)} {
			if strings.Contains(text, "SEID") || strings.Contains(text, "private-download-snapshot") || strings.Contains(strings.ToLower(text), "cookie") {
				t.Fatal("credential leaked in serialization/formatting")
			}
		}
	}
}

func TestAnimeDownloadTargetKeepsOriginalDirectoryAfterSettingsChange(t *testing.T) {
	f := newAnimeDownloadFixture(t)
	session := f.prepare(t)
	target := session.Target()
	f.saveSettings(t, "200")
	if _, err := f.service.PrepareDownloadTarget(context.Background(), target.SettingsRevision); !errors.Is(err, storage.ErrAnimeSettingsChanged) {
		t.Fatal("stale settings accepted", err)
	}
	if directory, err := session.ReadDirectory(context.Background()); err != nil || directory.CID != "100" {
		t.Fatal("existing session retargeted", directory, err)
	}
	restored, err := f.service.ResumeDownloadTarget(context.Background(), target)
	if err != nil || restored.Target() != target {
		t.Fatal("restored session retargeted", err)
	}
	if next := f.prepare(t); next.Target().ParentCID != "200" {
		t.Fatal("new task did not use new settings")
	}
}

func TestAnimeDownloadTargetDirectoryRenameKeepsCIDAndOriginalSnapshot(t *testing.T) {
	f := newAnimeDownloadFixture(t)
	target := f.prepare(t).Target()
	f.service.Directories = animeDownloadDirectoryFunc(func(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
		if cid != "100" || cookie != animeDownloadTestCookie {
			t.Fatal("directory was resolved by name or with different credentials")
		}
		return p115.DriveDirectory{CID: cid, Path: "/重命名后的目录"}, nil
	})
	resumed, err := f.service.ResumeDownloadTarget(context.Background(), target)
	if err != nil || resumed.Target() != target {
		t.Fatal("rename changed pinned task or lost the directory identity", err)
	}
	if directory, err := resumed.ReadDirectory(context.Background()); err != nil || directory.CID != target.ParentCID || directory.Path != "/重命名后的目录" {
		t.Fatal("failed to read renamed original directory", err)
	}
}

func TestAnimeDownloadTargetAccountChangesRejectWithoutFallback(t *testing.T) {
	for _, action := range []string{"refresh", "replace UID", "clear", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newAnimeDownloadFixture(t)
			session := f.prepare(t)
			target := session.Target()
			a, err := f.accounts.Primary("")
			if err != nil {
				t.Fatal(err)
			}
			cookie := "UID=123_A; SEID=refreshed"
			switch action {
			case "replace UID":
				cookie = "UID=999_A; SEID=replacement"
			case "clear":
				cookie = ""
			case "delete":
				err = f.accounts.Delete(a.ID, a.Revision)
			}
			if action != "delete" {
				err = f.accounts.Update(a.ID, a.Revision, a.Name, &cookie)
			}
			if err != nil {
				t.Fatal(err)
			}
			f.service.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
				t.Fatal("changed/new/secondary credential reached upstream")
				return p115.DriveDirectory{}, nil
			})
			for _, op := range []func() error{
				func() error { return session.Validate(context.Background()) },
				func() error { _, e := session.ReadDirectory(context.Background()); return e },
				func() error { _, e := f.service.ResumeDownloadTarget(context.Background(), target); return e },
				func() error {
					_, e := f.service.PrepareDownloadTarget(context.Background(), f.settings.Revision)
					return e
				},
			} {
				if err := op(); !errors.Is(err, storage.ErrAnimeAccountChanged) {
					t.Fatal("changed account accepted", err)
				}
			}
			if session.cookie != animeDownloadTestCookie || session.Target() != target {
				t.Fatal("old snapshot mutated")
			}
			if action == "replace UID" && f.accounts.PrimaryID() == a.ID {
				t.Fatal("UID replacement retained old configuration ID")
			}
		})
	}
}

func TestAnimeDownloadTargetRejectsUntrustedAccountSnapshot(t *testing.T) {
	for _, change := range []func(*P115Account){
		func(a *P115Account) { a.Cookie = "UID=999_A; SEID=other" },
		func(a *P115Account) { a.Primary = false },
		func(a *P115Account) { a.Ownership = "member" },
		func(a *P115Account) { a.OwnerUserID = strings.Repeat("a", 64) },
		func(a *P115Account) { a.Revision++ },
	} {
		f := newAnimeDownloadFixture(t)
		a, err := f.accounts.Primary("")
		if err != nil {
			t.Fatal(err)
		}
		change(&a)
		f.service.Accounts = &animePrimaryFake{account: a}
		f.service.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
			t.Fatal("untrusted account reached upstream")
			return p115.DriveDirectory{}, nil
		})
		if _, err := f.service.PrepareDownloadTarget(context.Background(), f.settings.Revision); !errors.Is(err, storage.ErrAnimeAccountChanged) {
			t.Fatal("untrusted account accepted", err)
		}
	}
}

func TestAnimeDownloadTargetDirectoryFailuresAreReadOnly(t *testing.T) {
	for _, result := range []struct {
		name string
		dir  p115.DriveDirectory
		err  error
	}{
		{"redirect to root", p115.DriveDirectory{CID: "0", Path: "/"}, nil},
		{"missing breadcrumb", p115.DriveDirectory{CID: "100"}, nil},
		{"invalid path", p115.DriveDirectory{CID: "100", Path: "/../other"}, nil},
		{"missing or inaccessible", p115.DriveDirectory{}, errors.New("not found; " + animeDownloadTestCookie)},
	} {
		t.Run(result.name, func(t *testing.T) {
			f := newAnimeDownloadFixture(t)
			before := f.settings
			calls := 0
			f.service.Directories = animeDownloadDirectoryFunc(func(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
				calls++
				if cid != "100" || cookie != animeDownloadTestCookie {
					t.Fatal("fallback or changed target")
				}
				return result.dir, result.err
			})
			_, err := f.service.PrepareDownloadTarget(context.Background(), before.Revision)
			if err == nil || calls != 1 || strings.Contains(err.Error(), "SEID") {
				t.Fatal("unsafe directory failure", calls, err)
			}
			if result.err != nil && !errors.Is(err, ErrAnimeDirectoryRead) {
				t.Fatal("upstream classification lost", err)
			}
			after, e := f.service.Store.GetAnimeSettings(context.Background())
			if e != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failure altered directory settings", e)
			}
		})
	}
}

func TestAnimeDownloadTargetChangesDuringDirectoryRead(t *testing.T) {
	for _, change := range []string{"settings", "account"} {
		t.Run(change, func(t *testing.T) {
			f := newAnimeDownloadFixture(t)
			directories := f.service.Directories
			f.service.Directories = animeDownloadDirectoryFunc(func(ctx context.Context, cid, cookie string) (p115.DriveDirectory, error) {
				// No SQLite transaction may be held over upstream I/O.
				f.service.Directories = directories
				if change == "settings" {
					f.saveSettings(t, "200")
				} else {
					a, err := f.accounts.Primary("")
					if err != nil {
						t.Fatal(err)
					}
					updated := "UID=123_A; SEID=changed-during-read"
					if err := f.accounts.Update(a.ID, a.Revision, a.Name, &updated); err != nil {
						t.Fatal(err)
					}
				}
				return p115.DriveDirectory{CID: cid, Path: "/动漫/100"}, nil
			})
			revision := f.settings.Revision
			_, err := f.service.PrepareDownloadTarget(context.Background(), revision)
			want := storage.ErrAnimeSettingsChanged
			if change == "account" {
				want = storage.ErrAnimeAccountChanged
			}
			if !errors.Is(err, want) {
				t.Fatal("concurrent change accepted", err)
			}
		})
	}
}

func TestAnimeDownloadTargetRejectsStaleAccountMemory(t *testing.T) {
	f := newAnimeDownloadFixture(t)
	// Another account-service instance updates the database, leaving this
	// instance's in-memory credentials unchanged. The persistent guard wins.
	other, err := OpenP115AccountsSQLite(context.Background(), f.service.Store, f.dir, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	a, err := other.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; SEID=other-instance"
	if err := other.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	f.service.Directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
		t.Fatal("stale persisted account reached upstream")
		return p115.DriveDirectory{}, nil
	})
	if _, err := f.service.PrepareDownloadTarget(context.Background(), f.settings.Revision); !errors.Is(err, storage.ErrAnimeAccountChanged) {
		t.Fatal("stale memory accepted", err)
	}
}

func TestAnimeDownloadTargetRestoreAfterActualDatabaseReopen(t *testing.T) {
	f := newAnimeDownloadFixture(t)
	target := f.prepare(t).Target()
	raw, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts, err := OpenP115AccountsSQLite(context.Background(), db, f.dir, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	s := &AnimeAdminService{Store: db, Accounts: accounts, Directories: f.service.Directories}
	var saved model.AnimeDownloadTarget
	if err := model.DecodeAnimeJSON(strings.NewReader(string(raw)), &saved); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumeDownloadTarget(context.Background(), saved)
	if err != nil || resumed.Target() != target || resumed.cookie != animeDownloadTestCookie {
		t.Fatal("exact target failed to restore", err)
	}
	a, err := accounts.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=123_A; SEID=after-restart"
	if err := accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResumeDownloadTarget(context.Background(), saved); !errors.Is(err, storage.ErrAnimeAccountChanged) {
		t.Fatal("restart silently refreshed old task credential", err)
	}
}

func TestAnimeDownloadTargetCancellationAndDeadline(t *testing.T) {
	f := newAnimeDownloadFixture(t)
	session := f.prepare(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.Validate(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	f.service.Directories = animeDownloadDirectoryFunc(func(ctx context.Context, _, _ string) (p115.DriveDirectory, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Fatal("unbounded directory request")
		}
		<-ctx.Done()
		return p115.DriveDirectory{}, fmt.Errorf("%w: %s", ctx.Err(), animeDownloadTestCookie)
	})
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.service.PrepareDownloadTarget(ctx, f.settings.Revision); !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "SEID") {
		t.Fatal("deadline lost or credentials leaked", err)
	}
}

func TestAnimeDownloadTargetUnconfiguredAndExplicitRoot(t *testing.T) {
	f := newAnimeDownloadFixture(t)
	f.saveSettings(t, "0")
	if target := f.prepare(t).Target(); target.ParentCID != "0" || target.ParentPath != "/" {
		t.Fatal("explicit root was altered")
	}
	var zero AnimeDownloadSession
	if err := zero.Validate(context.Background()); !errors.Is(err, storage.ErrAnimeInvalid) {
		t.Fatal("zero session accepted", err)
	}
	var nilSession *AnimeDownloadSession
	if _, err := nilSession.ReadDirectory(context.Background()); !errors.Is(err, storage.ErrAnimeInvalid) {
		t.Fatal("nil session accepted", err)
	}
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "unconfigured.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &AnimeAdminService{Store: db, Accounts: f.accounts, Directories: f.service.Directories}
	_, err = s.PrepareDownloadTarget(context.Background(), 1)
	var incomplete *AnimeConfigError
	if !errors.As(err, &incomplete) || incomplete.Code != "CONFIG_INCOMPLETE" || len(incomplete.Issues) != 2 {
		t.Fatal("unconfigured task accepted", err)
	}
}
