package service

import (
	"context"
	"errors"
	"fmt"
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
	qrApp                                 string
	directoryCookie                       string
	cookie                                string
	valid                                 bool
	users                                 map[string]emby.ManagedUser
	created, dirs                         int
	policyWrites                          int
	ignoreRestrictions                    bool
	policyResponseLost                    bool
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

func (f *memberTestRemote) CreateQRCode(_ context.Context, app string) (*p115.QRCode, error) {
	f.qrApp = app
	return &p115.QRCode{ID: "qr-" + app, ImageURL: "qr-text", ExpiresAt: time.Now().Add(3 * time.Minute)}, nil
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
	f.directoryCookie = cookie
	if (cid != "123" && cid != "456" && cid != "0") || !f.valid || cookie == "" {
		return errors.New("invalid")
	}
	return nil
}
func (f *memberTestRemote) Users(context.Context) ([]emby.User, error) {
	out := []emby.User{}
	for _, u := range f.users {
		user := emby.User{ID: u.ID, Name: u.Name}
		user.Policy.IsAdministrator = u.Policy["IsAdministrator"] == true
		user.Policy.IsDisabled = u.Policy["IsDisabled"] == true
		if allowed, ok := u.Policy["EnableMediaPlayback"].(bool); ok {
			user.Policy.EnableMediaPlayback = &allowed
		}
		if allowed, ok := u.Policy["EnableContentDownloading"].(bool); ok {
			user.Policy.EnableContentDownloading = &allowed
		}
		out = append(out, user)
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
	u := emby.ManagedUser{ID: fmt.Sprintf("%032x", f.created+100), Name: name, Policy: map[string]any{}}
	f.users[u.ID] = u
	if f.failCreate {
		return emby.ManagedUser{}, errors.New("response lost")
	}
	return u, nil
}
func (f *memberTestRemote) SetManagedPolicy(_ context.Context, id string, p map[string]any) error {
	copyPolicy := make(map[string]any, len(p))
	for k, v := range p {
		copyPolicy[k] = v
	}
	p = copyPolicy
	f.policyWrites++
	if f.ignoreRestrictions {
		p["EnablePlaybackRemuxing"] = true
	}
	if f.failEnable && p["EnableMediaPlayback"] == true {
		return errors.New("unknown")
	}
	u := f.users[id]
	u.Policy = p
	if f.ignoreFolders {
		u.Policy["EnableAllFolders"] = true
	}
	f.users[id] = u
	if f.policyResponseLost {
		return errors.New("response lost")
	}
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
	id := embyProfileID("server", strings.Repeat("a", 32))
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
		return EmbyProxyStatus{Listening: true, Config: EmbyProxyConfig{Enabled: true, URL: "http://emby"}}
	}
	if err := s.SetSettings(ctx, model.MemberPlaybackSettings{Enabled: true, LibraryIDs: []string{"library"}, PublicURL: "https://play.example.com"}); err != nil {
		t.Fatal(err)
	}
	remote := emby.ManagedUser{ID: strings.Repeat("a", 32), Name: "reader", Policy: map[string]any{"IsDisabled": false, "IsAdministrator": false, "EnableMediaPlayback": true, "EnableContentDownloading": true}}
	f.users[remote.ID] = remote
	if _, err := s.ObserveUser(ctx, embyService.snapshot(), remote); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSource(ctx, id, "personal", ""); err != nil {
		t.Fatal(err)
	}
	return s, f, id
}
func (f *memberTestRemote) DriveFiles(_ context.Context, cid string, offset int, cookie string) (*p115.SharePage, error) {
	f.directoryCookie = cookie
	return &p115.SharePage{Files: []p115.ShareFile{{ID: "123", Name: "Folder", Directory: true}, {ID: "999", Name: "file.mkv"}}, Count: 2, Offset: offset}, nil
}
func memberQRInput(s *MemberPlaybackService, id string, replace bool) MemberQRInput {
	v := s.View(id)
	return MemberQRInput{AccountID: v.AccountID, Revision: v.Revision, Replace: replace}
}
func memberDirectoryInput(s *MemberPlaybackService, id, cid string, create bool) MemberDirectoryInput {
	a := s.View(id).DirectoryAccount
	return MemberDirectoryInput{AccountID: a.AccountID, Revision: a.Revision, DirectoryID: cid, Create: create}
}
func memberScan(t *testing.T, s *MemberPlaybackService, id string) {
	t.Helper()
	ctx := context.Background()
	qr, err := s.CreateQR(ctx, id, memberQRInput(s, id, false))
	if err != nil {
		t.Fatal(err)
	}
	if status, err := s.PollQR(ctx, id, qr.ID); err != nil || status != "success" {
		t.Fatal(status, err)
	}
	if s.record(id).PendingAccountID != "" {
		if err := s.SetPersonalDirectory(ctx, id, memberDirectoryInput(s, id, "123", false)); err != nil {
			t.Fatal(err)
		}
	}

}

func (f *memberTestRemote) AuthenticateUser(_ context.Context, name, password string) (emby.ManagedUser, string, error) {
	for _, u := range f.users {
		if u.Name == name && password == "password123" {
			return u, "server", nil
		}
	}
	return emby.ManagedUser{}, "", ErrLogin
}
func TestEmbyPortalIdentityAndRevocation(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	auth := NewPortalAuth(s.db, "admin", "password123", "secret")
	auth.Members = s
	token, u, err := auth.Login(ctx, "reader", "password123")
	if err != nil || u.ID != id {
		t.Fatal(u, err)
	}
	if _, _, err := auth.Login(ctx, "admin", "password123"); err == nil {
		t.Fatal("admin credentials accepted as member")
	}
	if _, _, err := auth.LoginAdmin(ctx, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, token); err != nil {
		t.Fatal(err)
	}
	remote := f.users[u.EmbyID]
	remote.Name = "renamed"
	f.users[u.EmbyID] = remote
	if got, err := auth.Authenticate(ctx, token); err != nil || got.ID != id || got.Username != "renamed" {
		t.Fatal(got, err)
	}
	remote.Policy["IsDisabled"] = true
	f.users[u.EmbyID] = remote
	if _, err := auth.Authenticate(ctx, token); err == nil {
		t.Fatal("disabled identity accepted")
	}
	delete(f.users, u.EmbyID)
	if _, err := auth.Authenticate(ctx, token); err == nil {
		t.Fatal("deleted identity accepted")
	}
	f.users[strings.Repeat("d", 32)] = emby.ManagedUser{ID: strings.Repeat("d", 32), Name: "renamed", Policy: map[string]any{}}
	_, replacement, err := auth.Login(ctx, "renamed", "password123")
	if err != nil || replacement.ID == id {
		t.Fatal("same-name identity inherited", replacement, err)
	}
	if _, err := s.Authorize(ctx, "http://emby", replacement.EmbyID); err == nil {
		t.Fatal("uninvited login granted playback")
	}
	s.emby.config.ServerID = "changed"
	if _, err := auth.Authenticate(ctx, token); err == nil {
		t.Fatal("server identity changed")
	}
}
func TestPersonalDriveReplacementAndNoFallback(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	if _, err := s.Authorize(ctx, "http://emby", s.record(id).EmbyID); err == nil {
		t.Fatal("unbound played")
	}
	memberScan(t, s, id)
	old := s.record(id)
	if old.Stage != "ready" || old.AccountID == "" {
		t.Fatal(old)
	}
	if _, err := s.Authorize(ctx, "http://emby", old.EmbyID); err != nil {
		t.Fatal(err)
	}
	f.cookie = "UID=999_A; SEID=new"
	qr, err := s.CreateQR(ctx, id, memberQRInput(s, id, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollQR(ctx, id, qr.ID); err == nil {
		t.Fatal("implicit replacement allowed")
	}
	delete(s.sessions, id)
	f.failDirectory = true
	qr, err = s.CreateQR(ctx, id, memberQRInput(s, id, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollQR(ctx, id, qr.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersonalDirectory(ctx, id, memberDirectoryInput(s, id, "0", true)); err == nil {
		t.Fatal("uncertain directory accepted")
	}
	pending := s.record(id)
	if pending.AccountID != old.AccountID || pending.PendingAccountID == "" {
		t.Fatal("old binding changed", pending)
	}
	if _, err := s.Authorize(ctx, "http://emby", old.EmbyID); err != nil {
		t.Fatal("failed replacement stopped old playback", err)
	}
	f.failDirectory = false
	if err := s.FinishDriveChange(ctx, id, false); err == nil {
		t.Fatal("uncertain create retried")
	}
	if err := s.SetPersonalDirectory(ctx, id, memberDirectoryInput(s, id, "123", false)); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishDriveChange(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	reopened, err := newP115Accounts(s.accounts.store, "")
	if err != nil {
		t.Fatal("replacement did not survive restart", err)
	}
	s.accounts = reopened
	current := s.record(id)
	if current.AccountID == old.AccountID || current.PendingAccountID != "" {
		t.Fatal(current)
	}
	a, _ := s.accounts.Get(old.AccountID)
	if a.Cookie != "" || a.UID != "123" {
		t.Fatal("old identity mutated or credentials retained")
	}
	if err := s.Suspend(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", old.EmbyID); err == nil {
		t.Fatal("suspended drive fell back")
	}
	memberScan(t, s, id)
	if _, err := s.Authorize(ctx, "http://emby", old.EmbyID); err != nil {
		t.Fatal(err)
	}
	blocked := true
	if err := setPlaybackForTest(ctx, s, id, !blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(ctx, "http://emby", old.EmbyID); err == nil {
		t.Fatal("revocation allowed playback")
	}
	if f.users[old.EmbyID].Policy["EnableMediaPlayback"] != false {
		t.Fatal("explicit playback edit not applied")
	}
}
func TestInvitationsPoolAndRetry(t *testing.T) {
	s, f, _ := memberFixture(t)
	ctx := context.Background()
	auth := NewPortalAuth(s.db, "admin", "password123", "secret")
	auth.Members = s
	invites, err := auth.GenerateInvites(ctx, "admin", "", 2, 7, "assigned")
	if err != nil {
		t.Fatal(err)
	}
	first, err := auth.Register(ctx, "newreader", "password123", invites[0].Code)
	if err != nil {
		t.Fatal(err)
	}
	if s.record(first.ID).Source != "assigned" {
		t.Fatal(s.record(first.ID))
	}
	if _, err := auth.Register(ctx, "newreader", "password123", invites[0].Code); err == nil {
		t.Fatal("invite used twice")
	}
	pool, err := s.accounts.Add("pool")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=777_A; SEID=pool"
	if err := s.accounts.Update(pool, 1, "pool", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.UpdatePlaybackDirectory(pool, 2, "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if s.record(first.ID).Source != "assigned" {
		t.Fatal("primary user silently reassigned", s.record(first.ID))
	}
	if err := s.SetSource(ctx, first.ID, "assigned", ""); err != nil {
		t.Fatal(err)
	}
	if s.record(first.ID).Source != "assigned" || s.record(first.ID).Stage != "ready" {
		t.Fatal(s.record(first.ID))
	}
	f.failCreate = true
	if _, err := auth.Register(ctx, "secondreader", "password123", invites[1].Code); err == nil {
		t.Fatal("lost response accepted")
	}
	count := f.created
	f.failCreate = false
	second, err := auth.Register(ctx, "secondreader", "password123", invites[1].Code)
	if err != nil {
		t.Fatal(err)
	}
	if f.created != count || s.record(second.ID).Source != "assigned" {
		t.Fatal("duplicate creation or assignment")
	}
	if err := s.SetSource(ctx, second.ID, "assigned", ""); err != nil {
		t.Fatal(err)
	}

	a, _ := s.accounts.Get(pool)
	if err := s.accounts.Delete(pool, a.Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncUser(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if s.record(first.ID).Source != "assigned" {
		t.Fatal("lost unavailable binding")
	}
}
func TestPortalPoliciesAndIndependentSameUIDCredentials(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	admin, err := s.accounts.Add("admin-owned")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.Update(admin, 1, "admin-owned", &f.cookie); err != nil {
		t.Fatal(err)
	}
	qr, err := s.CreateQR(ctx, id, memberQRInput(s, id, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PollQR(ctx, id, qr.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersonalDirectory(ctx, id, memberDirectoryInput(s, id, "123", false)); err != nil {
		t.Fatal(err)
	}
	a, _ := s.accounts.Get(admin)
	if err := s.accounts.Delete(admin, a.Revision); err != nil {
		t.Fatal(err)
	}
	delete(s.sessions, id)
	memberScan(t, s, id)
	policy := f.users[s.record(id).EmbyID].Policy
	for _, key := range []string{"EnableVideoPlaybackTranscoding", "EnableAudioPlaybackTranscoding", "EnablePlaybackRemuxing"} {
		if policy[key] != false {
			t.Fatal(key)
		}
	}
	if memberPolicyMatches(map[string]any{}, true, s.Settings(), true) {
		t.Fatal("missing policy accepted")
	}
	if _, err := s.Authorize(ctx, "http://emby", "unknown"); err == nil {
		t.Fatal("unknown user fell back")
	}
	if _, err := s.Authorize(ctx, "http://emby", ""); err == nil {
		t.Fatal("anonymous playback")
	}
}

func TestSyncNeverDowngradesEmbyAdministrator(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	r := s.record(id)
	remote := f.users[r.EmbyID]
	remote.Policy["IsAdministrator"] = true
	f.users[r.EmbyID] = remote
	if err := s.SyncUser(ctx, id); err != nil {
		t.Fatal(err)
	}
	if f.users[r.EmbyID].Policy["IsAdministrator"] != true {
		t.Fatal("administrator downgraded")
	}
}
func TestBindTransactionFailureKeepsActiveAssociation(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	r := s.record(id)
	a, _ := s.accounts.Get(r.AccountID)
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := s.accounts.BindMember(ctx, r, "reader", "UID=999_A; SEID=new", a.Revision, true)
	if err == nil {
		t.Fatal("write failure hidden")
	}
	if len(s.accounts.List()) != 1 || s.record(id).AccountID != r.AccountID {
		t.Fatal("memory changed before commit")
	}
	if a2, _ := s.accounts.Get(r.AccountID); a2.Cookie != a.Cookie {
		t.Fatal("active credentials overwritten")
	}
	_ = f
}

func TestCancelUncertainDriveRevokesCredentialsWithoutRetryingCreate(t *testing.T) {
	s, f, id := memberFixture(t)
	ctx := context.Background()
	memberScan(t, s, id)
	f.cookie = "UID=999_A; SEID=new"
	f.failDirectory = true
	qr, err := s.CreateQR(ctx, id, memberQRInput(s, id, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollQR(ctx, id, qr.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersonalDirectory(ctx, id, memberDirectoryInput(s, id, "0", true)); err == nil {
		t.Fatal("uncertain directory accepted")
	}
	pending := s.record(id).PendingAccountID
	if err := s.FinishDriveChange(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	a, _ := s.accounts.Get(pending)
	if a.Cookie != "" {
		t.Fatal("cancel retained credentials")
	}
	f.failDirectory = false
	count := f.dirs
	qr, err = s.CreateQR(ctx, id, memberQRInput(s, id, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PollQR(ctx, id, qr.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPersonalDirectory(ctx, id, memberDirectoryInput(s, id, "0", true)); err == nil {
		t.Fatal("forgot uncertain create")
	}
	if f.dirs != count {
		t.Fatal("repeated uncertain directory creation")
	}
}

func TestPoolAllowsIndependent115Identity(t *testing.T) {
	s, _, _ := memberFixture(t)
	one, err := s.accounts.Add("one")
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.accounts.Add("two")
	if err != nil {
		t.Fatal(err)
	}
	cookie := "UID=777_A; SEID=pool"
	if err := s.accounts.Update(one, 1, "one", &cookie); err != nil {
		t.Fatal(err)
	}
	if err := s.accounts.Update(two, 1, "two", &cookie); err != nil {
		t.Fatal(err)
	}
}

func TestPlaybackRevocationPreservesEmbyBrowsing(t *testing.T) {
	s, _, id := memberFixture(t)
	ctx := context.Background()
	blocked := true
	if err := setPlaybackForTest(ctx, s, id, !blocked); err != nil {
		t.Fatal(err)
	}
	r := s.record(id)
	if _, err := s.Authorize(ctx, "http://emby", r.EmbyID); err == nil {
		t.Fatal("blocked playback")
	}
	if err := s.AuthorizeLogin(ctx, "http://emby", r.EmbyID, r.EmbyName); err != nil {
		t.Fatal("playback revocation blocked login", err)
	}
}
