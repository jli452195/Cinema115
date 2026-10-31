package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/utils/torrentmeta"
)

type downloadReaderFunc func(context.Context, DownloadResourceInput) (DownloadFiles, error)

func (f downloadReaderFunc) Files(ctx context.Context, in DownloadResourceInput) (DownloadFiles, error) {
	return f(ctx, in)
}

type downloadMetadataCounter struct{ calls int }

func (m *downloadMetadataCounter) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	m.calls++
	return (organizeFakeMetadata{}).Details(ctx, kind, id)
}

func downloadBatchFixture(t *testing.T, count int) (*DownloadOrganize, *organizeFakeDrive, DownloadBatchInput, *atomic.Int32) {
	t.Helper()
	s, drive, _, _, _, common := gardenFixture(t)
	calls := &atomic.Int32{}
	s.Resources = downloadReaderFunc(func(_ context.Context, in DownloadResourceInput) (DownloadFiles, error) {
		calls.Add(1)
		return organizeResourceFiles(strings.TrimPrefix(in.Magnet, "magnet:?xt=urn:btih:"), []torrentmeta.File{
			{Path: "same/Show [01].mkv", Size: 1200},
			{Path: "same/Show [01].zh-CN.ass", Size: 30},
		})
	})
	input := DownloadBatchInput{DownloadOrganizeInput: common, Resources: make([]DownloadOrganizeInput, count)}
	for i := range input.Resources {
		source := DownloadResourceInput{Source: "garden", Provider: "dmhy", ProviderID: fmt.Sprint(i + 1), Magnet: fmt.Sprintf("magnet:?xt=urn:btih:%040x", i+1)}
		files, err := s.Files(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		input.Resources[i] = DownloadOrganizeInput{DownloadResourceInput: source, ManifestID: files.ManifestID, Files: []OrganizeSelection{{ID: files.Files[0].ID, Season: 1, Episode: i + 1}}}
	}
	return s, drive, input, calls
}

func TestDownloadBatchPreparedManifestsNamespaceAndAtomicAcceptance(t *testing.T) {
	s, drive, input, reads := downloadBatchFixture(t, 61)
	m := &downloadMetadataCounter{}
	s.Organize.metadata = m
	preview, err := s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 61 || m.calls != 1 || drive.listCalls["20"] != 1 || len(preview.Jobs) != 61 {
		t.Fatal("batch repeated preparation or destination reads", reads.Load(), m.calls, drive.listCalls)
	}
	for i, job := range preview.Jobs {
		if job.Download.BatchID != preview.ID || len(job.Items) != 2 || job.Items[0].File.ID != "same/Show [01].mkv" || job.Items[1].File.SourcePath != "/same/Show [01].zh-CN.ass" || job.Items[0].File.ParentID != "same" || !strings.Contains(job.Items[1].Name, fmt.Sprintf("E%02d", i+1)) || job.Items[0].Conflict != "" {
			t.Fatal("cross-resource identities/subtitle mapping leaked", i, job)
		}
	}
	// Client mutation cannot change the retained server plan.
	preview.Jobs[0].Items[0].Name = "tampered.mkv"
	store := s.Organize.store.(*failingAccountStore)
	before := store.data
	store.err = errors.New("disk full")
	if _, err = s.StartBatch(preview.ID); err == nil || len(s.live) != 0 || len(s.History()) != 0 || store.data != before {
		t.Fatal("failed commit partially accepted the batch", err)
	}
	store.err = nil
	accepted, err := s.StartBatch(preview.ID)
	if err != nil || len(accepted.Jobs) != 61 || len(s.live) != 61 || len(s.History()) != 61 || s.Target().TargetCID != "20" || accepted.Jobs[0].Items[0].Name == "tampered.mkv" {
		t.Fatal("whole batch was not accepted", err, len(s.live), len(s.History()))
	}
	if _, err = s.StartBatch(preview.ID); err != nil || len(s.History()) != 61 || reads.Load() != 61 || drive.creates != 0 || drive.renames != 0 || drive.moves != 0 {
		t.Fatal("receipt retry duplicated work", err)
	}
	// A fresh service returns the persisted batch, without requiring previews
	// or replaying any submissions. Unstarted items can explicitly rejoin.
	engine, err := NewOrganizeEngine(store, s.Organize.accounts, drive, m, s.Organize.templates)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewDownloadOrganize(engine, s.Resources, s.Downloads, s.Settings, s.Reader)
	result, err := restarted.StartBatch(preview.ID)
	if err != nil || len(result.Jobs) != 61 || len(restarted.live) != 0 {
		t.Fatal("batch lost durable idempotency", err)
	}
	job, err := restarted.Resume(result.Jobs[0].ID)
	if err != nil || job.Status != "queued" || len(restarted.live) != 1 || !job.Stats.StartedAt.IsZero() {
		t.Fatal("unstarted queued intent cannot rejoin after restart", err, job)
	}
}

func TestDownloadBatchRejectsCrossResourceConflictsAndAllowsVersions(t *testing.T) {
	s, drive, input, _ := downloadBatchFixture(t, 2)
	input.Resources[1].Files[0].Episode = 1
	preview, err := s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range preview.Jobs {
		if job.Items[0].Conflict == "" || job.Items[1].Conflict == "" {
			t.Fatal("cross-source video/subtitle collisions not checked", job)
		}
	}
	if _, err = s.StartBatch(preview.ID); !errors.Is(err, ErrOrganizeInput) || len(s.live) != 0 || drive.creates != 0 {
		t.Fatal("conflicting batch caused side effects", err)
	}
	input.Resources[1].Fansub = "OtherGroup"
	input.Resources[1].ManifestID = ""
	preview, err = s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartBatch(preview.ID); err != nil || len(s.live) != 2 {
		t.Fatal("different versions of one episode should coexist", err)
	}
	// A later batch cannot reserve a target already held by queued work.
	input.Resources[1].Fansub = ""
	input.Resources = input.Resources[1:]
	input.Resources[0].DownloadResourceInput.Magnet = fmt.Sprintf("magnet:?xt=urn:btih:%040x", 3)
	files, err := s.Files(context.Background(), input.Resources[0].DownloadResourceInput)
	if err != nil {
		t.Fatal(err)
	}
	input.Resources[0].ManifestID = files.ManifestID
	preview, err = s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartBatch(preview.ID); !errors.Is(err, ErrOrganizeConflict) || len(s.live) != 2 {
		t.Fatal("queued target reservation was ignored", err)
	}
}

func TestDownloadManifestIdentityExpiryAndIsolation(t *testing.T) {
	s, _, input, reads := downloadBatchFixture(t, 1)
	resource := input.Resources[0]
	files, err := s.manifest(context.Background(), resource)
	if err != nil {
		t.Fatal(err)
	}
	files.Files[0].Name = "tampered.mkv"
	files.Specs[files.Files[0].ID] = DownloadFileSpec{Fansub: "tampered"}
	got, err := s.manifest(context.Background(), resource)
	if err != nil || got.Files[0].Name == "tampered.mkv" || got.Specs[got.Files[0].ID].Fansub == "tampered" {
		t.Fatal("manifest was mutable through API results", err)
	}
	wrong := resource
	wrong.Magnet = fmt.Sprintf("magnet:?xt=urn:btih:%040x", 2)
	if _, err = s.manifest(context.Background(), wrong); !errors.Is(err, ErrOrganizeConflict) {
		t.Fatal("manifest token allowed another source", err)
	}
	entry := s.manifests[resource.ManifestID]
	entry.at = time.Now().Add(-31 * time.Minute)
	s.manifests[resource.ManifestID] = entry
	if _, err = s.manifest(context.Background(), resource); !errors.Is(err, ErrOrganizeConflict) || reads.Load() != 1 {
		t.Fatal("expired token silently refetched resource", err, reads.Load())
	}
	for i := 0; i < 130; i++ {
		if _, err = s.Files(context.Background(), resource.DownloadResourceInput); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.manifests) != 128 {
		t.Fatal("manifest retention not bounded", len(s.manifests))
	}
}

func TestDownloadBatchCapacitySameAccountRefreshAndDuplicateHash(t *testing.T) {
	s, _, input, _ := downloadBatchFixture(t, 100)
	preview, err := s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Organize.account(input.AccountID)
	s.live["existing"] = newDownloadPending(OrganizeJob{Download: &ResourceDownload{Hash: "other"}}, a)
	if _, err = s.StartBatch(preview.ID); !errors.Is(err, ErrOrganizeConflict) || len(s.live) != 1 || len(s.History()) != 0 {
		t.Fatal("overflowing batch partially accepted", err)
	}
	delete(s.live, "existing")
	if _, err = s.StartBatch(preview.ID); err != nil || len(s.live) != 100 {
		t.Fatal("queue should accept exactly 100 tasks", err)
	}
	s, _, input, _ = downloadBatchFixture(t, 2)
	duplicate := input
	duplicate.Resources = []DownloadOrganizeInput{input.Resources[0], input.Resources[0]}
	if _, err = s.PreviewBatch(context.Background(), duplicate); !errors.Is(err, ErrOrganizeInput) {
		t.Fatal("same torrent accepted twice", err)
	}
	preview, err = s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	a, _ = s.Organize.account(input.AccountID)
	cookie := a.Cookie + "; refresh=1"
	if err = s.Organize.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartBatch(preview.ID); err != nil || len(s.live) != 2 {
		t.Fatal("same-account refresh invalidated confirmed batch", err)
	}
	for _, p := range s.live {
		if p.account.Cookie != cookie {
			t.Fatal("new execution did not use current account credentials")
		}
	}
}

func TestDownloadOriginalAccountUnavailableStopsBeforeCloudWork(t *testing.T) {
	for _, action := range []string{"clear", "replace", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, drive, adder, tracker, _, input := gardenFixture(t)
			plan, err := s.Preview(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.Start(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			account, err := s.Organize.account(job.Config.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			if action == "delete" {
				err = s.Organize.accounts.Delete(account.ID, account.Revision)
			} else {
				cookie := ""
				if action == "replace" {
					cookie = "UID=456_other; SEID=other-account"
				}
				err = s.Organize.accounts.Update(account.ID, account.Revision, account.Name, &cookie)
			}
			if err != nil {
				t.Fatal(err)
			}
			p := s.live[job.ID]
			if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "interrupted" {
				t.Fatal("unavailable original account did not stop its task", done, err, p.job.Status)
			}
			if drive.creates != 0 || adder.url != "" || tracker.calls != 0 {
				t.Fatal("unavailable original account reached remote cloud work")
			}
		})
	}
}

func TestDownloadBatchExecutionSlotsAndBackoff(t *testing.T) {
	s, _, jobs := gardenWorkerFixture(t, 10)
	var active, peak, reads atomic.Int32
	release := make(chan struct{})
	s.Reader = gardenReaderFunc(func(ctx context.Context, _, hash string) (model.AnimeOfflineStatus, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		reads.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return model.AnimeOfflineStatus{}, ctx.Err()
		}
		return gardenWorkerStatus(OrganizeJob{Download: &ResourceDownload{Hash: hash}}, "downloading"), nil
	})
	cancel, done, runErr := gardenRunTest(t, s)
	gardenWait(t, func() bool { return active.Load() == 4 })
	if reads.Load() != 4 {
		t.Fatal("more than four steps entered the network", reads.Load())
	}
	close(release)
	// The first four now back off. All peers must get their initial read
	// without waiting for the two-second polling delay.
	deadline := time.Now().Add(time.Second)
	for reads.Load() < int32(len(jobs)) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if reads.Load() != int32(len(jobs)) || peak.Load() > 4 {
		t.Fatal("backoff occupied execution slots", reads.Load(), peak.Load())
	}
	cancel()
	<-done
	if *runErr != nil || len(s.steps) != 0 || len(s.live) != 0 {
		t.Fatal("shutdown leaked execution slots/tasks", *runErr)
	}
}

func TestDownloadBatchSpecsUseEachFileAndSingleTitleTagsAsFallback(t *testing.T) {
	s, _, _, _, _, _ := gardenFixture(t)
	names := []torrentmeta.File{{Path: "[星光字幕组] Show [01][720p][繁體].mkv", Size: 10}}
	s.Resources = downloadReaderFunc(func(_ context.Context, _ DownloadResourceInput) (DownloadFiles, error) {
		return organizeResourceFiles(gardenTestHash, names)
	})
	in := DownloadResourceInput{Title: "[其他字幕组] Show [1080p][简体]"}
	files, err := s.Files(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	spec := files.Specs[files.Files[0].ID]
	if spec.Resolution != "720p" || spec.Fansub != "星光字幕组" || strings.Join(spec.Languages, ",") != "zh-Hant" {
		t.Fatal("title overrode the actual file", spec)
	}
	names = []torrentmeta.File{{Path: "Show [01].mkv", Size: 10}}
	files, err = s.Files(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	spec = files.Specs[files.Files[0].ID]
	if spec.Resolution != "1080p" || spec.Fansub != "其他字幕组" || strings.Join(spec.Languages, ",") != "zh-Hans" {
		t.Fatal("single file did not inherit missing title tags", spec)
	}
	names = append(names, torrentmeta.File{Path: "Show [02].mkv", Size: 10})
	files, err = s.Files(context.Background(), in)
	if err != nil || files.Specs[files.Files[0].ID].Resolution != "" || files.Specs[files.Files[1].ID].Fansub != "" {
		t.Fatal("pack title was copied onto every video", err, files.Specs)
	}
}

func TestDownloadSubtitleNamingScopes(t *testing.T) {
	for _, tc := range []struct {
		name, title, file, languages string
		peer                         bool
	}{
		{"explicit file", "[简繁内封]", "Show/Show.S01E01.[简体].mkv", "简体", false},
		{"directory", "", "Movie[国英多音轨+简繁英字幕]/Movie.mkv", "简繁英", false},
		{"directory before post", "Movie[简繁英内封]", "Movie[CHS]/Movie.mkv", "简体", false},
		{"post title", "Movie[国英多音轨+简繁英字幕]", "Movie/Movie.mkv", "简繁英", false},
		{"size is not GB subtitles", "", "Movie/Movie.[20 GB].mkv", "", false},
		{"mixed directory", "Movie[简繁英字幕]", "Movie[简繁英字幕]/Movie.mkv", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, drive, _, _, _, input := gardenFixture(t)
			reads := 0
			s.Resources = downloadReaderFunc(func(context.Context, DownloadResourceInput) (DownloadFiles, error) {
				reads++
				files := []torrentmeta.File{{Path: tc.file, Size: 100}}
				if tc.peer {
					files = append(files, torrentmeta.File{Path: path.Join(path.Dir(tc.file), "Other.mkv"), Size: 100})
				}
				return organizeResourceFiles(gardenTestHash, files)
			})
			input.DownloadResourceInput = DownloadResourceInput{Source: "gying", Title: tc.title}
			files, err := s.Files(context.Background(), input.DownloadResourceInput)
			if err != nil || len(files.Files) == 0 || reads != 1 || len(drive.listCalls) != 0 {
				t.Fatal("subtitle naming added I/O", files, err, reads, drive.listCalls)
			}
			for _, file := range files.Files {
				if file.NamingValues[naming.Languages] != tc.languages || file.NamingValues[naming.SubtitleMode] != "" {
					t.Fatal("wrong subtitle scope or inferred subtitle mode", file.NamingValues)
				}
			}
			input.Files = nil
			for i, file := range files.Files {
				input.Files = append(input.Files, OrganizeSelection{ID: file.ID, Season: 1, Episode: i + 1})
			}
			input.ManifestID = files.ManifestID
			cached, err := s.Preview(context.Background(), input)
			if err != nil || reads != 1 {
				t.Fatal("cached preview refetched resource", cached, err, reads)
			}
			input.ManifestID = ""
			fresh, err := s.Preview(context.Background(), input)
			if err != nil || len(cached.Items) != len(fresh.Items) || reads != 2 {
				t.Fatal("uncached preview", fresh, err, reads)
			}
			for i, item := range cached.Items {
				if item.Name != fresh.Items[i].Name || item.Directory != fresh.Items[i].Directory {
					t.Fatal("manifest changed subtitle naming", item, fresh.Items[i])
				}
			}
		})
	}
}

func TestDownloadQueuedRecoveryNeverReplaysExternalWriteIntent(t *testing.T) {
	s, _, input, _ := downloadBatchFixture(t, 1)
	preview, err := s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	base := preview.Jobs[0]
	base.Status = "interrupted"
	if !canQueueDownload(base) {
		t.Fatal("unstarted intent should be recoverable")
	}
	for _, mutate := range []func(*OrganizeJob){
		func(j *OrganizeJob) { j.Stats.StartedAt = time.Now() },
		func(j *OrganizeJob) { j.Stats.DirectoryCreates = 1 },
		func(j *OrganizeJob) { j.Items[0].Status = "preparing" },
		func(j *OrganizeJob) { j.Download.DirectoryID = "99" },
		func(j *OrganizeJob) { j.Config.SourceCID = "99" },
	} {
		job := cloneOrganizeJob(base)
		mutate(&job)
		if canQueueDownload(job) {
			t.Fatal("external activity incorrectly allowed resubmission", job)
		}
	}
}

func TestDownloadBatchCompletesThroughSharedWorker(t *testing.T) {
	s, drive, input, reads := downloadBatchFixture(t, 2)
	preview, err := s.PreviewBatch(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := s.StartBatch(preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	tracker := &gardenTrackerFake{}
	s.Reader = tracker
	for i, job := range accepted.Jobs {
		pending := s.live[job.ID]
		parent, video, subtitle := fmt.Sprint(800+i), fmt.Sprint(900+i), fmt.Sprint(950+i)
		tracker.resultID = parent
		tracker.beforeRead = func() {
			drive.dirs[pending.job.Download.DirectoryID] = []p115.ShareFile{{ID: parent, Name: "same", Directory: true}}
			drive.dirs[parent] = []p115.ShareFile{{ID: video, Name: "Show [01].mkv", Size: 1200}, {ID: subtitle, Name: "Show [01].zh-CN.ass", Size: 30}}
		}
		if done, err := s.advance(context.Background(), pending); err != nil || !done || pending.job.Status != "completed" {
			t.Fatal("batch item did not complete", err, pending.job)
		}
		if pending.job.Items[0].File.ID != video || pending.job.Items[1].File.ID != subtitle || pending.job.Download.BatchID != preview.ID {
			t.Fatal("batch lost actual identities", pending.job)
		}
	}
	if reads.Load() != 2 || tracker.calls != 2 || drive.moves != 2 || drive.renames != 2 {
		t.Fatal("batch repeated website/status reads or split subtitle writes", reads.Load(), tracker.calls, drive.moves, drive.renames)
	}
}
