package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"strm115/backend/internal/client/emby"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"testing"
	"time"
)

type memberTestRemote struct {
	qrStatus                              string
	cookie                                string
	valid                                 bool
	users                                 map[string]emby.ManagedUser
	created, dirs                         int
	failCreate, failDirectory, failEnable bool
	ignoreFolders                         bool
	failLibraries                         bool
	renameResponseLost                    bool
	passwordTarget                        string
}

func (f *memberTestRemote) Libraries(context.Context) ([]emby.Library, error) {
	if f.failLibraries {
		return nil, errors.New("offline")
	}
	return []emby.Library{{ID: "library", GUID: "guid-movies", Name: "Movies"}, {ID: "library2", GUID: "guid-tv", Name: "TV"}}, nil
}

func (f *memberTestRemote) CreateQRCode(context.Context, string) (*p115.QRCode, error) {
	return &p115.QRCode{ID: "qr", ImageURL: "qr-text", ExpiresAt: time.Now().Add(3 * time.Minute)}, nil
}
func (f *memberTestRemote) CheckQRCode(context.Context, string) (*p115.QRCodeStatus, error) {
	if f.qrStatus != "" {
		return &p115.QRCodeStatus{Status: f.qrStatus}, nil
	}
	return &p115.QRCodeStatus{Status: "success", Cookie: f.cookie}, nil
}
func (f *memberTestRemote) CompleteQRCodeLogin(context.Context, string) (string, error) {
	return f.cookie, nil
}
func (f *memberTestRemote) CheckCookie(context.Context, string) (bool, error) { return f.valid, nil }
func (f *memberTestRemote) CreateDirectory(context.Context, string, string, string) (string, error) {
	f.dirs++
	if f.failDirectory {
		return "", errors.New("uncertain")
	}
	return "123", nil
}
func (f *memberTestRemote) ValidateCacheParent(_ context.Context, cid, cookie string) error {
	if cid != "123" || !f.valid || cookie == "" {
		return errors.New("invalid")
	}
	return nil
}
func (f *memberTestRemote) Users(context.Context) ([]emby.User, error) {
	out := []emby.User{}
	for _, u := range f.users {
		out = append(out, emby.User{ID: u.ID, Name: u.Name})
	}
	return out, nil
}
func (f *memberTestRemote) ManagedUser(_ context.Context, id string) (emby.ManagedUser, error) {
	u, ok := f.users[id]
	if !ok {
		return u, errors.New("missing")
	}
	return u, nil
}
func (f *memberTestRemote) CreateManagedUser(_ context.Context, name string) (emby.ManagedUser, error) {
	f.created++
	u := emby.ManagedUser{ID: strings.Repeat("e", 32), Name: name, Policy: map[string]any{}}
	f.users[u.ID] = u
	if f.failCreate {
		return emby.ManagedUser{}, errors.New("response lost")
	}
	return u, nil
}
func (f *memberTestRemote) SetManagedPolicy(_ context.Context, id string, p map[string]any) error {
	if f.failEnable && p["EnableMediaPlayback"] == true {
		return errors.New("unknown")
	}
	u := f.users[id]
	u.Policy = p
	if f.ignoreFolders {
		u.Policy["EnableAllFolders"] = true
	}
	f.users[id] = u
	return nil
}
func (f *memberTestRemote) SetManagedPassword(_ context.Context, id, _ string) error {
	f.passwordTarget = id
	return nil
}
func (f *memberTestRemote) RenameManagedUser(_ context.Context, id, name string) error {
	u, ok := f.users[id]
	if !ok {
		return errors.New("missing")
	}
	u.Name = name
	f.users[id] = u
	if f.renameResponseLost {
		return errors.New("response lost")
	}
	return nil
}
func memberFixture(t *testing.T) (*MemberPlaybackService, *memberTestRemote, string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "member.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	id := strings.Repeat("a", 32)
	if err := db.CreateWebsiteUser(ctx, model.WebsiteUser{ID: id, Username: "reader", PasswordHash: "hash", CreatedAt: "now"}); err != nil {
		t.Fatal(err)
	}
	accounts, err := OpenP115AccountsSQLite(ctx, db, t.TempDir(), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	f := &memberTestRemote{cookie: "UID=123_A; SEID=secret-cookie", valid: true, users: map[string]emby.ManagedUser{strings.Repeat("b", 32): {ID: strings.Repeat("b", 32), Name: "template", Policy: map[string]any{"IsDisabled": true, "IsAdministrator": false, "EnableAllFolders": false, "EnabledFolders": []string{"library"}}}}}
	embyService := &EmbyService{config: EmbyConfig{URL: "http://emby", APIKey: "secret", ServerID: "server"}}
	s, err := NewMemberPlayback(ctx, db, accounts, embyService, f, f, f)
	if err != nil {
		t.Fatal(err)
	}
	s.NewEmby = func(string, string) memberEmbyClient { return f }
	s.ProxyStatus = func() EmbyProxyStatus {
		return EmbyProxyStatus{Listening: true, Config: EmbyProxyConfig{URL: "http://emby"}}
	}
	if err := s.SetSettings(ctx, model.MemberPlaybackSettings{Enabled: true, LibraryIDs: []string{"library"}, PublicURL: "https://play.example.com"}); err != nil {
		t.Fatal(err)
	}
	return s, f, id
}
func memberScan(t *testing.T, s *MemberPlaybackService, id string) {
	t.Helper()
	ctx := context.Background()
	qr, err := s.CreateQR(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if status, err := s.PollQR(ctx, id, qr.ID); err != nil || status != "success" {
		t.Fatal(status, err)
	}
}
func TestMemberPlaybackLifecycleAndNoFallback(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	pending := s.record(id)
	if pending.Stage != "pending" || !pending.Provisioned || f.dirs != 0 || len(s.accounts.List()) != 0 {
		t.Fatal("registration requires drive", pending)
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", pending.EmbyID, pending.EmbyName); err != nil {
		t.Fatal("browse login denied", err)
	}
	if _, err := s.Authorize(ctx, "http://emby", pending.EmbyID); err == nil {
		t.Fatal("pending user fell back to primary")
	}
	memberScan(t, s, id)
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	if r.Stage != "ready" || !r.Provisioned || f.created != 1 || f.dirs != 1 {
		t.Fatal(r, f.created, f.dirs)
	}
	if managed, err := s.Authorize(ctx, "http://emby", r.EmbyID); !managed || err != nil {
		t.Fatal(managed, err)
	}
	u := f.users[r.EmbyID]
	if u.Policy["EnableContentDownloading"] != false || u.Policy["EnableContentDeletion"] != false || u.Policy["IsAdministrator"] != false || u.Policy["EnableAllFolders"] != false {
		t.Fatal("unsafe policy", u.Policy)
	}
	bindings := s.Bindings(PlaybackCacheConfig{EmbyURL: "http://emby", Bindings: []PlaybackBinding{{UserID: r.EmbyID, AccountID: "primary"}}})
	if len(bindings.Bindings) != 1 || bindings.Bindings[0].AccountID != r.AccountID {
		t.Fatal("mandatory binding replaced", bindings)
	}
	f.valid = false
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("invalid cookie allowed")
	}
	f.valid = true
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if s.View(id).Stage != "ready" {
		t.Fatal("valid cookie did not recover")
	}
	disabled := true
	if err := s.db.UpdateWebsiteUser(ctx, id, "", &disabled); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("disabled website user allowed before sync")
	}
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if f.users[r.EmbyID].Policy["IsDisabled"] != true {
		t.Fatal("Emby not disabled")
	}
	disabled = false
	_ = s.db.UpdateWebsiteUser(ctx, id, "", &disabled)
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Suspend(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("unbound fell back")
	}
	memberScan(t, s, id)
	if s.View(id).Stage != "ready" {
		t.Fatal("same UID did not restore", s.View(id))
	}
	// A direct Emby administrator disable must remain blocked after QR refresh.
	u = f.users[r.EmbyID]
	u.Policy = memberPolicy(u.Policy, false, s.Settings())
	f.users[r.EmbyID] = u
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	memberScan(t, s, id)
	if !s.record(id).AdminBlocked || s.record(id).Stage == "ready" {
		t.Fatal("scan cleared admin block")
	}
	no := false
	if err := s.AdminUpdate(ctx, id, &no, "", false); err != nil {
		t.Fatal(err)
	}
	if s.record(id).Stage != "ready" {
		t.Fatal("explicit admin unblock failed")
	}
	if err := s.accounts.Delete(r.AccountID, s.accounts.List()[0].Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("deleted target fell back")
	}
	if len(s.Bindings(PlaybackCacheConfig{}).Bindings) != 1 {
		t.Fatal("deleted target binding lost")
	}
}
func TestMemberProvisionRecoveryAndIdentity(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	f.failCreate = true
	if err := s.Provision(ctx, id, "password123"); err == nil {
		t.Fatal("lost response accepted")
	}
	if s.record(id).Stage != "creating" {
		t.Fatal("reservation not saved")
	}
	f.failCreate = false
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	if f.created != 1 || f.dirs != 1 {
		t.Fatal("created twice")
	}
	// UID uniqueness applies to either direction of administrator/member edits.
	other, err := s.accounts.Add("other")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.Update(other, 1, "other", &f.cookie); err == nil {
		t.Fatal("UID shared with administrator config")
	}
	f.cookie = "UID=999_A; SEID=another"
	qr, err := s.CreateQR(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollQR(ctx, strings.Repeat("c", 32), qr.ID); err == nil {
		t.Fatal("another user consumed QR")
	}
	if _, err := s.PollQR(ctx, id, qr.ID); err == nil {
		t.Fatal("UID silently changed")
	}
}
func TestMemberUncertainDirectoryAndEnableStayLocked(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	f.failDirectory = true
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", s.record(id).EmbyID); err == nil {
		t.Fatal("uncertain directory enabled playback")
	}
	if err := s.Provision(ctx, id, "password123"); err != nil || f.dirs != 1 {
		t.Fatal("directory creation replayed")
	}
	f.failDirectory = false
	if err := s.AdminUpdate(ctx, id, nil, "123", false); err != nil {
		t.Fatal(err)
	}
	f.failEnable = true
	if err := s.Provision(ctx, id, "password123"); err == nil {
		t.Fatal("unknown policy accepted")
	}
	r := s.record(id)
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("uncertain enable allowed playback")
	}
	f.failEnable = false
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if s.record(id).Stage != "ready" {
		t.Fatal("recovery failed")
	}
}

func TestMemberLibrarySettingsRevisionAndRetry(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	settings := s.Settings()
	settings.LibraryIDs = []string{"missing"}
	if err := s.SetSettings(ctx, settings); err == nil {
		t.Fatal("unknown library accepted")
	}
	settings.LibraryIDs = nil
	if err := s.SetSettings(ctx, settings); err == nil {
		t.Fatal("empty enabled libraries accepted")
	}
	settings.LibraryIDs = []string{"library2", "library2"}
	settings.AllowDownload = true
	settings.Revision = 999 // only the server allocates revisions
	if err := s.SetSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	settings = s.Settings()
	if settings.Revision != 2 || len(settings.LibraryIDs) != 1 {
		t.Fatal(settings)
	}
	settings.LibraryIDs[0] = "external mutation"
	if s.Settings().LibraryIDs[0] != "library2" {
		t.Fatal("settings slice escaped")
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("old library permissions used")
	}
	f.failEnable = true
	if err := s.SyncUser(ctx, id); err == nil {
		t.Fatal("sync uncertainty ignored")
	}
	if s.record(id).SyncError == "" || !s.View(id).SyncPending {
		t.Fatal("sync failure not displayed")
	}
	f.failEnable = false
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err != nil {
		t.Fatal(err)
	}
	p := f.users[r.EmbyID].Policy
	if p["EnableContentDownloading"] != true || p["EnableAllFolders"] != false {
		t.Fatal("download/library policy incorrect", p)
	}
	if folders := p["EnabledFolders"].([]string); len(folders) != 1 || folders[0] != "guid-tv" {
		t.Fatal("unselected library allowed", folders)
	}
	for _, key := range []string{"EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing", "EnableContentDeletion", "IsAdministrator"} {
		if p[key] != false {
			t.Fatal("unsafe policy", key)
		}
	}
	settings = s.Settings()
	settings.AllowDownload = false
	if err := s.SetSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("revoked download still authorized")
	}
	f.ignoreFolders = true
	if err := s.SyncUser(ctx, id); err == nil {
		t.Fatal("ignored library restriction accepted")
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("bad remote policy unlocked playback")
	}
	f.ignoreFolders = false
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	persisted, err := s.db.MemberPlaybackSettings(ctx)
	if err != nil || persisted.Revision != s.Settings().Revision || persisted.LibraryIDs[0] != "library2" {
		t.Fatal(persisted, err)
	}
	// An offline Emby server must never prevent turning self-service off.
	f.failLibraries = true
	settings = s.Settings()
	settings.Enabled = false
	if err := s.SetSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("disabled service authorized playback")
	}
}

func TestMemberDenyPolicyMustBeVerifiedBeforeProvisioning(t *testing.T) {
	s, f, id := memberFixture(t)
	memberScan(t, s, id)
	f.ignoreFolders = true
	if err := s.Provision(context.Background(), id, "password123"); err == nil {
		t.Fatal("unsafe creation policy accepted")
	}
	if s.record(id).Provisioned {
		t.Fatal("unsafe creation marked provisioned")
	}
}

func TestMemberBrowsePolicyUsesLibraryGUIDWithoutGrantingPlayback(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	p := f.users[r.EmbyID].Policy
	folders := p["EnabledFolders"].([]string)
	if len(folders) != 1 || folders[0] != "guid-movies" || s.Settings().LibraryIDs[0] != "library" {
		t.Fatal("item ID used for policy", folders)
	}
	if p["IsDisabled"] != false || p["EnableMediaPlayback"] != false || p["EnableAllFolders"] != false {
		t.Fatal("browse-only policy changed", p)
	}
	for _, key := range []string{"EnableAudioPlaybackTranscoding", "EnableVideoPlaybackTranscoding", "EnablePlaybackRemuxing", "EnableSyncTranscoding", "EnableMediaConversion"} {
		if p[key] != false {
			t.Fatal("transcoding allowed", key)
		}
	}
	settings := s.Settings()
	settings.LibraryIDs = []string{"deleted"}
	if _, err := memberPolicySettings(ctx, f, settings); err == nil {
		t.Fatal("deleted library accepted")
	}
	f.failLibraries = true
	if err := s.SyncUser(ctx, id); err == nil {
		t.Fatal("sync succeeded without library mapping")
	}
	if s.record(id).SyncError == "" {
		t.Fatal("failed mapping marked synced")
	}
}

func TestMemberLoginRejectsUnfinishedReservations(t *testing.T) {
	s, _, id := memberFixture(t)
	ctx := context.Background()
	if err := s.AuthorizeLogin(ctx, "http://emby", strings.Repeat("e", 32), "c115_"+id); err == nil {
		t.Fatal("reserved user received login before ID persistence")
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", "ordinary", "existing-user"); err != nil {
		t.Fatal(err)
	}
	memberScan(t, s, id)
	if err := s.Provision(ctx, id, "password123"); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	if err := s.AuthorizeLogin(ctx, "http://emby", r.EmbyID, r.EmbyName); err != nil {
		t.Fatal(err)
	}
	settings := s.Settings()
	settings.LibraryIDs = []string{"library2"}
	if err := s.SetSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", r.EmbyID, r.EmbyName); err == nil {
		t.Fatal("pending policy received login")
	}
}
