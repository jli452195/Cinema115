package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type fastCacheFake struct {
	*cacheDriveFake
	reads, prepares, completes, checks int
	createErr, completeErr, linkErr    bool
	deleted                            []string
}

func (f *fastCacheFake) PreparePlaybackAccount(context.Context, string, string) (*p115.PlaybackUploadTicket, error) {
	f.prepares++
	return &p115.PlaybackUploadTicket{}, nil
}
func (f *fastCacheFake) ValidatePlaybackDirectory(context.Context, string, string, string) error {
	f.checks++
	return nil
}
func (f *fastCacheFake) PickCodeInstantInfo(_ context.Context, pc, cookie string) (*p115.DownloadInfo, error) {
	f.reads++
	return &p115.DownloadInfo{Name: pc + ".mkv", Size: 100, SHA1: strings.Repeat(strings.ToUpper(pc[len(pc)-1:]), 40), URL: "https://cdn.115.com/source"}, nil
}
func (f *fastCacheFake) CreateDirectory(ctx context.Context, parent, name, cookie string) (string, error) {
	if f.createErr {
		f.creates++
		return "", errors.New("uncertain")
	}
	return f.cacheDriveFake.CreateDirectory(ctx, parent, name, cookie)
}
func (f *fastCacheFake) UploadPlayback(ctx context.Context, info *p115.DownloadInfo, scope, cid, cookie string, ticket *p115.PlaybackUploadTicket, progress func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	f.uploads++
	if f.uploadErr != nil {
		return nil, f.uploadErr
	}
	progress(p115.InstantProgress{Stage: "init"})
	id := fmt.Sprint(900 + f.uploads)
	file := p115.DrivePlaybackFile{ID: id, CID: cid, Name: info.Name, Size: info.Size, SHA1: info.SHA1, PickCode: "pick" + id}
	f.files = append(f.files, file)
	return &p115.InstantUploadResult{FileID: id, PickCode: file.PickCode}, nil
}
func (f *fastCacheFake) CompletePlaybackUpload(_ context.Context, _ *p115.DownloadInfo, _ string, result *p115.InstantUploadResult, _, _ string) (p115.DrivePlaybackFile, string, error) {
	f.completes++
	if f.completeErr {
		return p115.DrivePlaybackFile{}, "", errors.New("invalid")
	}
	for _, file := range f.files {
		if file.ID == result.FileID {
			return file, "https://cdn.115.com/friend", nil
		}
	}
	return p115.DrivePlaybackFile{}, "", errors.New("missing")
}
func (f *fastCacheFake) DriveDownloadURLWithUA(ctx context.Context, id, pc, cookie, ua string) (string, error) {
	if f.linkErr {
		return "", p115.ErrDriveRejected
	}
	return f.cacheDriveFake.DriveDownloadURLWithUA(ctx, id, pc, cookie, ua)
}
func (f *fastCacheFake) SharedCacheDirectoryFiles(context.Context, string, string) ([]p115.DrivePlaybackFile, error) {
	return append([]p115.DrivePlaybackFile{}, f.files...), nil
}
func (f *fastCacheFake) DeleteEntries(_ context.Context, ids []string, _ string) error {
	f.deleted = append(f.deleted, ids...)
	for _, id := range ids {
		for i, file := range f.files {
			if file.ID == id {
				f.files = append(f.files[:i], f.files[i+1:]...)
				break
			}
		}
	}
	return nil
}
func setupFastCache(t *testing.T) (*PlaybackCache, *fastCacheFake, *PlaybackCacheSelection) {
	s, base, selected := setupPlaybackCache(t)
	f := &fastCacheFake{cacheDriveFake: base}
	s.client = f
	return s, f, selected
}

func TestFastPreflightFailureDoesNotLockFile(t *testing.T) {
	s, f, selected := setupFastCache(t)
	f.uploadErr = &p115.PlaybackPreflightError{Stage: "目录", Err: errors.New("目录身份不一致")}
	_, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player")
	if err == nil || !strings.Contains(err.Error(), "尚未提交上传") || len(s.Status().Entries) != 0 {
		t.Fatal(err)
	}
	f.uploadErr = nil
	if _, _, err = s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player"); err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 || len(s.Status().Directories) != 0 || f.files[0].CID != "0" {
		t.Fatal("created folder or wrong parent")
	}
}
func TestFastSHA1ReuseAndCredentialIsolation(t *testing.T) {
	s, f, selected := setupFastCache(t)
	for _, pc := range []string{"pickaaa", "pickbbb"} {
		if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, pc, "src", "player", "source"); err != nil {
			t.Fatal(err)
		}
	}
	if f.creates != 0 || f.uploads != 2 {
		t.Fatal("wrong uploads")
	}
	_, r, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "newUA", "source")
	if err != nil || r.UsedSource || f.reads != 2 || f.ua != "newUA" {
		t.Fatal("mapping failed", err)
	}
	for _, pair := range [][2]string{{"changed", "source"}, {"changed", "other"}} {
		if _, _, err = s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", pair[0], "player", pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if f.reads != 3 || f.uploads != 2 || f.prepares != 2 {
		t.Fatal("credential isolation or SHA1 reuse failed")
	}
}
func TestFastSHA1OnlyHitSkipsUploadPreparation(t *testing.T) {
	s, f, selected := setupFastCache(t)
	f.files = []p115.DrivePlaybackFile{{ID: "123", CID: "77", Name: "renamed", Size: 100, SHA1: strings.Repeat("a", 40), PickCode: "pick123"}}
	_, r, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player")
	if err != nil || f.uploads != 0 || f.prepares != 0 || f.creates != 0 || r.FileCacheID != "123" {
		t.Fatal("SHA1-only reuse failed", err)
	}
	f.searchErr = errors.New("network")
	s.sourceMappings = nil
	if _, _, err = s.ResolvePickCodeWithReport(context.Background(), selected, "pickbbb", "src", "player"); err == nil || f.uploads != 0 {
		t.Fatal("search failure triggered upload")
	}
}
func TestFastUncertainRecoveryAndRestart(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			s, f, selected := setupFastCache(t)
			if complete {
				f.completeErr = true
			} else {
				f.uploadErr = p115.ErrInstantUncertain
			}
			if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player"); err == nil {
				t.Fatal("failure hidden")
			}
			restarted, err := NewPlaybackCache(context.Background(), s.store.(*SessionStore), s.accounts, f, s.logs)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			_, _, err = restarted.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player")
			if (err == nil) != complete || f.uploads != 1 {
				t.Fatal("unknown write replayed or positive SHA1 failed recovery", err)
			}
			e := restarted.Status().Entries[0]
			e.LastUsed = time.Now().Add(-1000 * time.Hour)
			_ = restarted.put(e)
			if err := restarted.Cleanup(context.Background()); err != nil || len(f.deleted) != 0 {
				t.Fatal("cleanup deleted file")
			}
		})
	}
}
func TestFastFailedMappingResearchesAndReuploadsDeletedFile(t *testing.T) {
	s, f, selected := setupFastCache(t)
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player"); err != nil {
		t.Fatal(err)
	}
	f.files = nil
	f.linkErr = true
	if _, _, err := s.ResolvePickCodeWithReport(context.Background(), selected, "pickaaa", "src", "player"); err != nil {
		t.Fatal(err)
	}
	if f.uploads != 2 || f.creates != 0 {
		t.Fatal("missing file did not recover")
	}
}

func TestPlaybackRestartDisablesPersistedCleanup(t *testing.T) {
	s, f, _ := setupFastCache(t)
	doc := s.Status()
	doc.Config.CleanupEnabled = true
	if err := s.saveLocked(doc); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPlaybackCache(context.Background(), s.store.(*SessionStore), s.accounts, f, s.logs)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.Status().Config.CleanupEnabled {
		t.Fatal("legacy cleanup still enabled")
	}
	if err := restarted.Cleanup(context.Background()); err != nil || len(f.deleted) != 0 {
		t.Fatal("retired cleanup performed I/O")
	}
	if err := restarted.ApplyCleanup(true, 72); err == nil {
		t.Fatal("cleanup re-enabled")
	}
}
