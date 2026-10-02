package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestDistributedPersonalSnapshotAndAdminPrivacy(t *testing.T) {
	s, _, selected := setupFastCache(t)
	id := selected.binding.AccountID
	s.accounts.mu.Lock()
	for i := range s.accounts.value.Accounts {
		a := &s.accounts.value.Accounts[i]
		if a.ID == id {
			a.Ownership, a.OwnerUserID = "member", "owner"
		}
	}
	s.accounts.mu.Unlock()
	r := model.MemberPlayback{UserID: "owner", EmbyID: cacheTestUser, ServerID: "server", EmbyURL: "http://emby", AccountID: id, DirectoryID: "0", Source: "personal", Stage: "ready", Provisioned: true}
	s.Members = &MemberPlaybackService{settings: model.MemberPlaybackSettings{Enabled: true, ServerID: "server", EmbyURL: "http://emby"}, records: map[string]model.MemberPlayback{"owner": r}}
	s.Members.ProxyStatus = func() EmbyProxyStatus {
		return EmbyProxyStatus{Listening: true, Config: EmbyProxyConfig{Enabled: true}}
	}
	for _, source := range []string{"personal", "primary", "assigned"} {
		r.Source = source
		s.Members.records["owner"] = r
		// Preserve a second target to inspect donors when the owner uses primary.
		s.mu.Lock()
		s.doc.Config.Bindings = []PlaybackBinding{{UserID: "other", AccountID: id}}
		s.mu.Unlock()
		if got := s.Snapshot().selections["other"].donors; len(got) != 1 || got[0].Cookie != selected.cookie {
			t.Fatalf("source %s excluded valid personal donor", source)
		}
	}
	for _, mode := range []string{"suspended", "pending", "unbound"} {
		v := r
		switch mode {
		case "suspended":
			v.Suspended = true
		case "pending":
			v.Stage = "preparing"
		case "unbound":
			v.AccountID = ""
		}
		s.Members.records["owner"] = v
		if len(s.Snapshot().selections["other"].donors) != 0 {
			t.Fatal("ineligible donor", mode)
		}
	}
	e := PlaybackCacheEntry{Key: DirectLinkKey("private"), AccountID: id, UID: selected.uid, DirectoryID: "private-dir", File: p115.DrivePlaybackFile{ID: "private-file", Name: "private-name"}}
	if err := s.put(e); err != nil {
		t.Fatal(err)
	}
	view := s.AdminStatus()
	if view.PrivateCopies != 1 || len(view.Entries) != 0 {
		t.Fatal("private copies exposed")
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{id, "private-dir", "private-file", "private-name", selected.cookie} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private metadata leaked")
		}
	}
}

func TestDistributedCopySurvivesConfigurationReplacement(t *testing.T) {
	s, fast, selected := setupFastCache(t)
	f := &materialFake{fastCacheFake: fast}
	s.client = f
	ctx := context.WithValue(context.Background(), playbackMaterialOrigin{}, DirectLinkKey("origin"))
	load := func() (*p115.DownloadInfo, error) {
		return &p115.DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
	}
	if _, err := s.resolveInstant(ctx, selected, "main", "player", &PlaybackResolution{}, load); err != nil {
		t.Fatal(err)
	}
	old := s.Status().Entries[0]
	replacement := *selected
	replacement.binding.AccountID = "new-config"
	replacement.cookie = "UID=22_A; SEID=new-independent-cookie"
	replacement.accountRevision++
	if _, err := s.resolveInstant(ctx, &replacement, "", "player", &PlaybackResolution{}, func() (*p115.DownloadInfo, error) { t.Fatal("source read"); return nil, nil }); err != nil {
		t.Fatal(err)
	}
	got := s.Status().Entries
	if len(got) != 1 || got[0].Key != old.Key || got[0].AccountID != "new-config" || f.uploads != 1 || f.donorCookie != replacement.cookie {
		t.Fatal("configuration replacement changed ownership or copied credentials")
	}
	// An old config ID is irrelevant for donor selection; UID and file ID are checked.
	target := replacement
	target.uid = "33"
	target.binding.AccountID = "target"
	target.donors = []P115Account{{ID: "third-config", UID: "22", Cookie: "UID=22_A; SEID=donor"}}
	if _, err := s.readPlaybackMaterialRange(ctx, f, &target, got, s.Status().Materials[0], 1, 3); err != nil {
		t.Fatal(err)
	}
}

type distributedRangeReader struct {
	calls     int
	slow      bool
	deadlines []time.Time
}

func (f *distributedRangeReader) PlaybackPickCodeSource(ctx context.Context, _, _, _ string) (*p115.DownloadInfo, error) {
	f.calls++
	deadline, _ := ctx.Deadline()
	f.deadlines = append(f.deadlines, deadline)
	if f.slow {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, &p115.HTTPError{Status: 429, RetryAfter: 2 * time.Minute}
}
func (f *distributedRangeReader) PlaybackRangeSHA1(context.Context, *p115.DownloadInfo, int64, int64) (string, error) {
	return "", errors.New("unexpected range")
}

func distributedCandidates() (*PlaybackCacheSelection, []PlaybackCacheEntry, PlaybackMaterial) {
	m := PlaybackMaterial{SHA1: strings.Repeat("A", 40), Size: 100}
	sel := &PlaybackCacheSelection{uid: "target"}
	entries := make([]PlaybackCacheEntry, 0, 5)
	for i := 0; i < 5; i++ {
		uid := fmt.Sprint(i + 1)
		sel.donors = append(sel.donors, P115Account{ID: "config" + uid, UID: uid, Cookie: "UID=" + uid + "_A"})
		entries = append(entries, PlaybackCacheEntry{Key: uid, AccountID: "deleted-config", UID: uid, Status: "ready", File: p115.DrivePlaybackFile{ID: uid, PickCode: "pc" + uid, SHA1: m.SHA1, Size: m.Size}})
	}
	sel.donors = append(sel.donors, sel.donors[0])
	return sel, entries, m
}
func TestDistributedDonorBudgetDedupAndCooldown(t *testing.T) {
	s := &PlaybackCache{}
	selected, entries, m := distributedCandidates()
	f := &distributedRangeReader{}
	if _, err := s.readPlaybackMaterialRange(context.Background(), f, selected, entries, m, 1, 3); err == nil {
		t.Fatal("failed donors accepted")
	}
	if f.calls != 3 || !f.deadlines[0].Equal(f.deadlines[2]) {
		t.Fatal("attempts have independent budgets or duplicate UIDs")
	}
	if len(s.donorCandidates(selected, entries, m)) != 2 {
		t.Fatal("UID cooldown not applied")
	}
	for _, state := range s.donorStates {
		if time.Until(state.until) < time.Minute {
			t.Fatal("Retry-After ignored")
		}
	}
	s = &PlaybackCache{}
	f = &distributedRangeReader{slow: true}
	started := time.Now()
	if _, err := s.readPlaybackMaterialRange(context.Background(), f, selected, entries, m, 1, 3); err == nil {
		t.Fatal("timeout hidden")
	}
	if elapsed := time.Since(started); elapsed < playbackDonorBudget || elapsed > playbackDonorBudget+time.Second || f.calls != 1 {
		t.Fatal("shared deadline exceeded", elapsed, f.calls)
	}
}

func TestDistributedSchedulerAndConcurrentMaterialMerge(t *testing.T) {
	s, _, _ := setupFastCache(t)
	ctx := context.Background()
	first, err := s.acquirePlayback(ctx, "1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.acquirePlayback(ctx, "2")
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := s.acquirePlayback(blocked, "1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("same UID did not serialize", err)
	}
	maintenance, cancelMaintenance := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancelMaintenance()
	if _, err := s.acquirePlayback(maintenance, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("maintenance passed active requests", err)
	}
	second()
	first()
	maintenanceRelease, err := s.acquirePlayback(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.acquirePlayback(PlaybackPrewarmContext(ctx), "3"); err == nil {
		t.Fatal("prewarm passed maintenance")
	}
	maintenanceRelease()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := PlaybackMaterial{ID: playbackMaterialID(strings.Repeat("A", 40), 100), Name: "v.mkv", SHA1: strings.Repeat("A", 40), Size: 100, VerifiedAt: time.Now(), Origins: []string{DirectLinkKey(fmt.Sprint(i))}, Ranges: []PlaybackRange{{Start: int64(i), End: int64(i), SHA1: strings.Repeat("B", 40), VerifiedAt: time.Now()}}}
			if err := s.putMaterial(m); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	m := s.Status().Materials[0]
	if len(m.Ranges) != 8 || len(m.Origins) != 8 {
		t.Fatal("concurrent material update lost data")
	}
	m.Ranges[0].SHA1 = strings.Repeat("C", 40)
	if err := s.putMaterial(m); err == nil {
		t.Fatal("conflicting range accepted")
	}
}

func TestDistributedSQLiteCopyPromotionIsAtomic(t *testing.T) {
	ctx := context.Background()
	old, f, _ := setupFastCache(t)
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "copy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	counted := &countedPlaybackDB{Store: db}
	s, err := NewSQLitePlaybackCache(ctx, counted, nil, old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	e := PlaybackCacheEntry{Key: DirectLinkKey("intent"), UID: "22", Status: "uploading", File: p115.DrivePlaybackFile{ID: "901"}}
	if err := s.put(e); err != nil {
		t.Fatal(err)
	}
	e.Status = "ready"
	counted.fail = true
	if _, err := s.storeReadyCopy(e); err == nil {
		t.Fatal("failed promotion accepted")
	}
	if got, _ := s.entryByKey(e.Key); got.Status != "uploading" {
		t.Fatal("failed promotion changed memory")
	}
	counted.fail = false
	ready, err := s.storeReadyCopy(e)
	if err != nil {
		t.Fatal(err)
	}
	b := counted.batches[len(counted.batches)-1]
	if len(b.Copies) != 1 || len(b.DeleteCopies) != 1 || len(b.Materials) != 0 || b.Settings != nil {
		t.Fatal("promotion was not a single incremental transaction")
	}
	restarted, err := NewSQLitePlaybackCache(ctx, counted, nil, old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	if len(restarted.Status().Entries) != 1 || restarted.Status().Entries[0].Key != ready.Key {
		t.Fatal("promotion lost on restart")
	}
}

type concurrentPlaybackFake struct {
	*fastCacheFake
	mu      sync.Mutex
	started chan struct{}
	proceed chan struct{}
}

func (f *concurrentPlaybackFake) FindPlaybackFileBySHA1(context.Context, string, string) (*p115.DrivePlaybackFile, error) {
	return nil, nil
}
func (f *concurrentPlaybackFake) PreparePlaybackAccount(ctx context.Context, id, cookie string) (*p115.PlaybackUploadTicket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fastCacheFake.PreparePlaybackAccount(ctx, id, cookie)
}
func (f *concurrentPlaybackFake) UploadPlayback(ctx context.Context, info *p115.DownloadInfo, id, cid, cookie string, ticket *p115.PlaybackUploadTicket, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	select {
	case f.started <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-f.proceed:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fastCacheFake.UploadPlayback(ctx, info, id, cid, cookie, ticket, progress)
}
func (f *concurrentPlaybackFake) CompletePlaybackUpload(ctx context.Context, info *p115.DownloadInfo, cid string, result *p115.InstantUploadResult, cookie, ua string) (p115.DrivePlaybackFile, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fastCacheFake.CompletePlaybackUpload(ctx, info, cid, result, cookie, ua)
}
func TestDistributedDifferentUIDsUploadConcurrentlyToOwnTargets(t *testing.T) {
	s, base, selected := setupFastCache(t)
	f := &concurrentPlaybackFake{fastCacheFake: base, started: make(chan struct{}), proceed: make(chan struct{})}
	s.client = f
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, playbackMaterialOrigin{}, DirectLinkKey("same-origin"))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := *selected
			target.uid = fmt.Sprint(30 + i)
			target.cookie = "UID=" + target.uid + "_A"
			target.binding.AccountID = "config" + target.uid
			target.binding.ParentCID = "dir" + target.uid
			_, errs[i] = s.resolveInstant(ctx, &target, "main", "player", &PlaybackResolution{}, func() (*p115.DownloadInfo, error) {
				return &p115.DownloadInfo{Name: "v.mkv", Size: 100, SHA1: strings.Repeat("A", 40), URL: "https://cdn.115.com/source"}, nil
			})
		}(i)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-f.started:
		case <-ctx.Done():
			cancel()
			wg.Wait()
			t.Fatal("different targets serialized globally")
		}
	}
	close(f.proceed)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	status := s.Status()
	if len(status.Materials) != 1 || len(status.Entries) != 2 || f.uploads != 2 {
		t.Fatal("concurrent targets lost material/copy")
	}
	for _, e := range status.Entries {
		if e.AccountID != "config"+e.UID || e.File.CID != "dir"+e.UID {
			t.Fatal("cross-target write")
		}
	}
}

func TestDistributedSuccessfulDonorsRotateAndFileFailureIsLocal(t *testing.T) {
	s := &PlaybackCache{}
	sel, entries, m := distributedCandidates()
	first := s.donorCandidates(sel, entries, m)[0]
	if !s.leaseDonor(first.account.UID) {
		t.Fatal("cannot lease")
	}
	s.finishDonor(first.account.UID, first.entry.Key, nil)
	if s.donorCandidates(sel, entries, m)[0].account.UID == first.account.UID {
		t.Fatal("successful donor monopolizes traffic")
	}
	if !s.leaseDonor(first.account.UID) {
		t.Fatal("success unexpectedly cooled down")
	}
	s.finishDonor(first.account.UID, first.entry.Key, errPlaybackDonorIdentity)
	if len(s.donorCandidates(sel, entries, m)) != 4 {
		t.Fatal("bad file not skipped")
	}
	if !s.leaseDonor(first.account.UID) {
		t.Fatal("file error disabled entire account")
	}
	s.finishDonor(first.account.UID, "other-file", nil)
}
