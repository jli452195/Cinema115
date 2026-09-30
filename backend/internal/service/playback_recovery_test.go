package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/storage"
)

type recoveryFake struct {
	*fastCacheFake
	linkErr                       error
	verifyErr                     error
	directoryErr                  error
	missing                       map[string]bool
	attempts                      map[string]int
	verified, searches, refreshes int
	check                         func(context.Context) error
}

func (f *recoveryFake) DriveDownloadURLWithUA(ctx context.Context, id, pc, cookie, ua string) (string, error) {
	f.attempts[id]++
	if f.missing[id] {
		return "", f.linkErr
	}
	return f.fastCacheFake.DriveDownloadURLWithUA(ctx, id, pc, cookie, ua)
}
func (f *recoveryFake) VerifyPlaybackFile(ctx context.Context, file p115.DrivePlaybackFile, cookie string) error {
	f.verified++
	if cookie != "UID=22_A; SEID=private" {
		return errors.New("wrong fixed credentials")
	}
	if f.check != nil {
		return f.check(ctx)
	}
	return f.verifyErr
}
func (f *recoveryFake) ValidatePlaybackRecoveryDirectory(_ context.Context, scope, cid, cookie string) error {
	f.refreshes++
	if cid != "0" || cookie != "UID=22_A; SEID=private" {
		return errors.New("destination changed")
	}
	return f.directoryErr
}
func (f *recoveryFake) FindPlaybackFileBySHA1Excluding(_ context.Context, hash, cookie string, missing map[string]bool) (*p115.DrivePlaybackFile, error) {
	f.searches++
	if cookie != "UID=22_A; SEID=private" {
		return nil, errors.New("destination changed")
	}
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	for _, v := range f.files {
		if !missing[v.ID] && strings.EqualFold(v.SHA1, hash) {
			copy := v
			return &copy, nil
		}
	}
	return nil, nil
}

func setupRecovery(t *testing.T) (*PlaybackCache, *recoveryFake, *PlaybackCacheSelection, PlaybackCacheEntry) {
	t.Helper()
	s, base, selected := setupFastCache(t)
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player"); err != nil {
		t.Fatal(err)
	}
	old := s.Status().Entries[0]
	f := &recoveryFake{fastCacheFake: base, missing: map[string]bool{old.File.ID: true}, attempts: make(map[string]int), linkErr: p115.ErrDriveRejected, verifyErr: p115.ErrDriveFileMissing}
	s.client = f
	return s, f, selected, old
}

func TestPlaybackRecoveryRebuildsOnceAndPreservesMaterial(t *testing.T) {
	for _, alternative := range []bool{false, true} {
		t.Run(fmt.Sprint(alternative), func(t *testing.T) {
			s, f, selected, old := setupRecovery(t)
			alias := old
			alias.Key = DirectLinkKey("alias")
			alias.SourceKey = DirectLinkKey("other-origin")
			if err := s.put(alias); err != nil {
				t.Fatal(err)
			}
			s.rememberSource(alias.SourceKey, alias.Key)
			if alternative {
				f.files = append(f.files, p115.DrivePlaybackFile{ID: "555", CID: "0", PickCode: "pick555", Size: 100, SHA1: old.File.SHA1})
			}
			_, report, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player")
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if alternative {
				want = 1
			}
			if f.uploads != want || f.attempts[old.File.ID] != 1 || f.verified != 1 || f.searches != 2 {
				t.Fatal("duplicate attempt or upload", f.uploads, f.attempts, f.verified, f.searches)
			}
			if f.refreshes != want-1 {
				t.Fatal("directory was not refreshed before rebuilding")
			}
			if _, ok := s.entryByKey(old.Key); ok {
				t.Fatal("stale copy remains")
			}
			if s.mappedSource(alias.SourceKey) != "" {
				t.Fatal("alias mapping remains")
			}
			if len(s.Status().Materials) != 1 || len(s.Status().Entries) != 1 || report.FileCacheID == old.File.ID {
				t.Fatal("material lost or stale file reused")
			}
			if err := s.TouchDirectLink(context.Background(), old.Key, old.File.ID); err == nil {
				t.Fatal("old direct link remains valid")
			}
			restarted, err := NewPlaybackCache(context.Background(), s.store.(*SessionStore), s.accounts, f, s.logs)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if _, _, err := restarted.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player"); err != nil {
				t.Fatal(err)
			}
			if f.uploads != want || f.attempts[old.File.ID] != 1 {
				t.Fatal("restart repeated recovery")
			}
		})
	}
}

func TestPlaybackRecoveryNeverRebuildsAmbiguousFailures(t *testing.T) {
	for _, mode := range []string{"exists", "login", "rate", "network", "http404", "restricted", "check-error", "directory", "search-error", "cancel", "pending"} {
		t.Run(mode, func(t *testing.T) {
			s, f, selected, old := setupRecovery(t)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			switch mode {
			case "exists":
				f.verifyErr = nil
			case "login":
				f.linkErr = p115.ErrShareLoginRequired
			case "rate":
				f.linkErr = p115.ErrShareThrottled
			case "network":
				f.linkErr = errors.New("secret raw network text")
			case "http404":
				f.linkErr = &p115.HTTPError{Status: 404}
			case "restricted":
				f.linkErr = p115.ErrDriveLinkRestricted
			case "check-error":
				f.verifyErr = errors.New("secret upstream")
			case "directory":
				f.directoryErr = errors.New("secret folder missing")
			case "search-error":
				f.searchErr = errors.New("secret search failed")
			case "cancel":
				f.check = func(ctx context.Context) error { cancel(); <-ctx.Done(); return ctx.Err() }
			case "pending":
				pending := old
				pending.Key = DirectLinkKey("pending")
				pending.Status = "uncertain"
				pending.RetryNotBefore = time.Now().Add(time.Minute)
				if err := s.put(pending); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := s.ResolvePickCodeWithReport(ctx, selected, "pickaaa", "source", "player")
			if err == nil || strings.Contains(err.Error(), "secret") || f.uploads != 1 {
				t.Fatal("failure caused upload or leaked data", mode, err, f.uploads)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			if mode != "directory" && mode != "search-error" && mode != "pending" {
				if _, ok := s.entryByKey(old.Key); !ok {
					t.Fatal("unproven failure deleted record")
				}
			}
			if mode == "exists" || mode == "login" || mode == "network" {
				checks := f.verified
				_, _, _ = s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player")
				if f.attempts[old.File.ID] != 1 || f.verified != checks {
					t.Fatal("cooldown repeated download")
				}
			}
		})
	}
}

func TestPlaybackRecoveryInvalidationIsTransactional(t *testing.T) {
	ctx := context.Background()
	old, f, selected, entry := setupRecovery(t)
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "recovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	counted := &countedPlaybackDB{Store: db}
	s, err := NewSQLitePlaybackCache(ctx, counted, old.store.(*SessionStore), old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.rememberSource("alias", entry.Key)
	counted.fail = true
	if err := s.invalidateMissingCopy(selected.uid, entry.File); err == nil {
		t.Fatal("storage failure hidden")
	}
	if _, ok := s.entryByKey(entry.Key); !ok || s.mappedSource("alias") != entry.Key {
		t.Fatal("failed transaction mutated memory")
	}
	counted.fail = false
	other := entry
	other.Key = DirectLinkKey("other-uid")
	other.UID = "99"
	if err := s.put(other); err != nil {
		t.Fatal(err)
	}
	pending := entry
	pending.Key = DirectLinkKey("pending")
	pending.Status = "uncertain"
	if err := s.put(pending); err != nil {
		t.Fatal(err)
	}
	if err := s.invalidateMissingCopy(selected.uid, entry.File); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.entryByKey(entry.Key); ok || s.mappedSource("alias") != "" {
		t.Fatal("stale handle retained")
	}
	batch := counted.batches[len(counted.batches)-1]
	if len(batch.DeleteCopies) != 1 || len(batch.Materials) != 0 || len(batch.DeleteMaterials) != 0 {
		t.Fatal("invalidated unrelated data")
	}
	restarted, err := NewSQLitePlaybackCache(ctx, counted, nil, old.accounts, f, old.logs)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if len(restarted.Status().Entries) != 2 || len(restarted.Status().Materials) != 1 {
		t.Fatal("lost other UID, pending write or material")
	}
}

type staleQuickRecovery struct {
	*recoveryFake
	candidate p115.DrivePlaybackFile
}

func (f *staleQuickRecovery) FindPlaybackFileBySHA1Quick(context.Context, string, string) (*p115.DrivePlaybackFile, error) {
	copy := f.candidate
	return &copy, p115.ErrDriveRejected
}
func TestPlaybackRecoveryHandlesQuickCandidateMetadataRejection(t *testing.T) {
	s, f, selected, old := setupRecovery(t)
	if err := s.Forget(context.Background(), old.Key); err != nil {
		t.Fatal(err)
	}
	s.client = &staleQuickRecovery{recoveryFake: f, candidate: old.File}
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player"); err != nil {
		t.Fatal(err)
	}
	if f.uploads != 2 || f.verified != 1 || f.searches != 1 || len(f.attempts) != 0 {
		t.Fatal("unverified candidate was used or recovery failed")
	}
}

func TestPlaybackRecoveryBoundsCandidates(t *testing.T) {
	s, f, selected, old := setupRecovery(t)
	for i := 0; i < 4; i++ {
		file := old.File
		file.ID = fmt.Sprint(700 + i)
		file.PickCode = "pick" + file.ID
		f.files = append(f.files, file)
		f.missing[file.ID] = true
	}
	_, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player")
	if err == nil || f.verified != 3 || f.uploads != 1 {
		t.Fatal("unbounded recovery", err, f.verified, f.uploads)
	}
	for _, count := range f.attempts {
		if count != 1 {
			t.Fatal("duplicate file attempt")
		}
	}
}

func TestPlaybackRecoveryUnknownWriteKeepsCooldown(t *testing.T) {
	s, f, selected, old := setupRecovery(t)
	f.uploadErr = errors.New("write interrupted")
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player"); err == nil {
		t.Fatal("unknown write hidden")
	}
	if f.uploads != 2 {
		t.Fatal("recovery did not submit exactly once")
	}
	entries := s.Status().Entries
	if len(entries) != 1 || entries[0].Status != "uncertain" || entries[0].RetryNotBefore.IsZero() {
		t.Fatal("lost write intent")
	}
	f.files = nil
	f.uploadErr = nil
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player"); err == nil {
		t.Fatal("cooldown bypassed")
	}
	if f.uploads != 2 || f.attempts[old.File.ID] != 1 {
		t.Fatal("replayed uncertain recovery write")
	}
}

func TestPlaybackRecoveryPermanentDeletionWireResponse(t *testing.T) {
	s, f, selected, old := setupRecovery(t)
	c := p115.NewShareClient()
	c.HTTP.Transport = prefetchRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/files/get_info" || r.URL.Query().Get("file_id") != old.File.ID || r.Header.Get("Cookie") != selected.cookie {
			t.Error("wrong exact request")
		}
		body := fmt.Sprintf(`{"state":true,"code":0,"data":[{"aid":120,"fid":"%s","pc":"%s","sha":"%s","s":"100"}]}`, old.File.ID, old.File.PickCode, old.File.SHA1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	f.check = func(ctx context.Context) error { return c.VerifyPlaybackFile(ctx, old.File, selected.cookie) }
	_, report, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "source", "player")
	if err != nil || f.uploads != 2 || f.verified != 1 || f.attempts[old.File.ID] != 1 || report.FileCacheID == old.File.ID {
		t.Fatal("permanent deletion did not recover", err)
	}
}
