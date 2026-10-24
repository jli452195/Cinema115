package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	medianaming "strm115/backend/internal/naming"
	"strm115/backend/internal/storage"
)

type animeFileGatewayStub struct {
	create func(context.Context, string, string, string) (string, error)
	page   func(context.Context, string, int, string) (p115.AnimeDrivePage, error)
}

func (f *animeFileGatewayStub) CreateDirectory(ctx context.Context, parent, name, cookie string) (string, error) {
	return f.create(ctx, parent, name, cookie)
}
func (f *animeFileGatewayStub) ReadAnimeDrivePage(ctx context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
	return f.page(ctx, cid, offset, cookie)
}

func animeFileRoot(job model.AnimeDownloadJob) model.AnimeDriveEntry {
	return model.AnimeDriveEntry{ID: "200", ParentCID: job.ParentCID, Name: job.DirectoryName, Kind: "directory"}
}
func animeFileVideo() model.AnimeDriveEntry {
	return model.AnimeDriveEntry{ID: "400", ParentCID: "300", Name: "01.mkv", Kind: "file", Size: 1234}
}

func TestAnimeFilesDirectoryCreateOnceAndIntegratedSubmit(t *testing.T) {
	f, db, session, job := animeSubmitFixture(t)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid=NULL WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	creates, submits := 0, 0
	session.directories = animeDownloadDirectoryFunc(func(context.Context, string, string) (p115.DriveDirectory, error) {
		t.Fatal("successful create receipt triggered another directory read")
		return p115.DriveDirectory{}, nil
	})
	f.service.FileGateway = &animeFileGatewayStub{
		create: func(_ context.Context, parent, name, cookie string) (string, error) {
			creates++
			if parent != "100" || name != job.DirectoryName || cookie != animeDownloadTestCookie {
				t.Fatal("unfixed create")
			}
			var dispatched string
			if err := db.QueryRow(`SELECT json_extract(directory_operation_json,'$.state') FROM anime_download_jobs WHERE id=?`, job.ID).Scan(&dispatched); err != nil || dispatched != "dispatched" {
				t.Fatal("create before committed intent", dispatched, err)
			}
			return "200", nil
		},
		page: func(context.Context, string, int, string) (p115.AnimeDrivePage, error) {
			t.Fatal("normal create scanned parent")
			return p115.AnimeDrivePage{}, nil
		},
	}
	f.service.SubmitGateway = &animeSubmitGatewayStub{
		inspect: func(context.Context, string, string) (p115.AnimeTaskObservation, error) {
			if submits == 0 {
				return p115.AnimeTaskObservation{}, nil
			}
			return matchedAnimeTask(job), nil
		},
		submit: func(context.Context, string, string, string) error { submits++; return nil },
	}
	for i := 0; i < 3; i++ {
		out, err := f.service.EnsureDownload(context.Background(), session, job.ID)
		if err != nil || out.Job.DirectoryCID == nil || *out.Job.DirectoryCID != "200" || out.Job.DirectoryOperation.State != "confirmed" || out.Job.DownloadState != "accepted" {
			t.Fatal(out, err)
		}
	}
	if creates != 1 || submits != 1 {
		t.Fatal("needless writes", creates, submits)
	}
}

func TestAnimeFilesDirectoryUnknownRecoversWithoutAnotherCreate(t *testing.T) {
	for _, mode := range []string{"lost receipt", "not found", "duplicate name", "wrong parent", "partial listing", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, job := animeSubmitFixture(t)
			if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid=NULL WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			creates := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.service.FileGateway = &animeFileGatewayStub{
				create: func(context.Context, string, string, string) (string, error) {
					creates++
					if mode == "cancelled" {
						cancel()
					}
					return "", errors.New("private " + animeDownloadTestCookie)
				},
				page: func(_ context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
					if cid != "100" || offset != 0 || cookie != animeDownloadTestCookie {
						t.Fatal("recovery escaped saved parent")
					}
					entries := []model.AnimeDriveEntry{animeFileRoot(job)}
					if mode == "wrong parent" {
						entries[0].ParentCID = "999"
					}
					if mode == "not found" {
						entries = nil
					}
					if mode == "duplicate name" {
						extra := animeFileRoot(job)
						extra.ID = "201"
						entries = append(entries, extra)
					}
					count := len(entries)
					if mode == "partial listing" {
						count = 2
						entries = nil
					}
					return p115.AnimeDrivePage{CID: cid, Count: count, Entries: entries}, nil
				},
			}
			first, err := f.service.EnsureDownloadDirectory(ctx, session, job.ID)
			if !errors.Is(err, ErrAnimeDirectoryPending) || first.DirectoryCID != nil || first.DirectoryOperation.State != "unknown" {
				t.Fatal("lost create was not preserved", first, err)
			}
			out, err := f.service.EnsureDownloadDirectory(context.Background(), session, job.ID)
			shouldBind := mode == "lost receipt" || mode == "cancelled"
			if creates != 1 || shouldBind && (err != nil || out.DirectoryCID == nil) || !shouldBind && (!errors.Is(err, ErrAnimeDirectoryPending) || out.DirectoryCID != nil) {
				t.Fatal("unsafe recovery", creates, out, err)
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "SEID=") {
				t.Fatal("credential in directory operation")
			}
		})
	}
}

func completedAnimeFilesFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, *AnimeDownloadSession, model.AnimeDownloadJob) {
	t.Helper()
	f, db, job, _, _ := animeStatusFixture(t)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET download_state='completed',result_object_id='300',result_object_kind='unknown' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.restoreAnimeStatusSession(context.Background(), job.Snapshot.Target)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f, db, session, job
}

func normalAnimeFileGateway(t *testing.T, pages *int) *animeFileGatewayStub {
	t.Helper()
	return &animeFileGatewayStub{
		create: func(context.Context, string, string, string) (string, error) {
			t.Fatal("file verification created directory")
			return "", nil
		},
		page: func(_ context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
			*pages++
			if cid != "200" && cid != "300" || offset != 0 || cookie != animeDownloadTestCookie {
				t.Fatal("unscoped list", cid)
			}
			if cid == "200" {
				return p115.AnimeDrivePage{CID: cid, Count: 1, Entries: []model.AnimeDriveEntry{{ID: "300", ParentCID: cid, Kind: "directory", Name: "release"}}}, nil
			}
			return p115.AnimeDrivePage{CID: cid, Count: 1, Entries: []model.AnimeDriveEntry{animeFileVideo()}}, nil
		},
	}
}

func TestAnimeFilesCompletionSavesManifestAndStopsAllOfflinePolling(t *testing.T) {
	for _, naming := range []bool{false, true} {
		t.Run(map[bool]string{false: "save original", true: "await organize"}[naming], func(t *testing.T) {
			f, db, job, runner, now := animeStatusFixture(t)
			if naming {
				job.Snapshot.Naming = &model.AnimeNamingPlan{SchemaVersion: 1}
				template, _ := medianaming.Default(medianaming.TV)
				job.Snapshot.Organization = &model.AnimeOrganizationSnapshot{Naming: medianaming.Snapshot{Template: template}, SchemaVersion: 1, PolicyVersion: 1, PlanRevision: 1, Target: job.Snapshot.Target}
				job.Snapshot.Organization.Target.ParentCID = "900"
				job.Snapshot.Organization.Target.ParentPath = "/整理"
				raw, _ := json.Marshal(job.Snapshot)
				if _, err := db.Exec(`UPDATE anime_download_jobs SET snapshot_json=? WHERE id=?`, string(raw), job.ID); err != nil {
					t.Fatal(err)
				}
			}
			pages, statusReads := 0, 0
			f.service.FileGateway = normalAnimeFileGateway(t, &pages)
			f.service.StatusGateway = animeStatusReaderFunc(func(context.Context, string, string) (model.AnimeOfflineStatus, error) {
				statusReads++
				return serviceAnimeStatus(job, "finished", 100), nil
			})
			if err := runner.PollOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			out, err := f.service.Store.GetAnimeDownloadJob(context.Background(), job.ID)
			workflow := "original_saved"
			if naming {
				workflow = "active"
			}
			if err != nil || out.WorkflowState != workflow || out.FileState != "verified" || out.Files == nil || out.Files.Validate(out) != nil || len(out.Files.Entries) != 3 || *out.ResultObjectKind != "directory" || out.NextRunAt != nil || pages != 2 || statusReads != 1 {
				t.Fatal(out, pages, statusReads, err)
			}
			*now += 86400000
			if err := runner.PollOnce(context.Background()); err != nil || statusReads != 1 || pages != 2 {
				t.Fatal("finished task rechecked", err)
			}
			// The saved snapshot is an operation record, not a live file index.
			// A later manual move must not cause another 115 listing or download.
			f.service.FileGateway = &animeFileGatewayStub{page: func(context.Context, string, int, string) (p115.AnimeDrivePage, error) {
				t.Fatal("completed files were scanned again after manual organization")
				return p115.AnimeDrivePage{}, nil
			}}
			session, err := f.service.restoreAnimeStatusSession(context.Background(), out.Snapshot.Target)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.CollectDownloadedFiles(context.Background(), session, out.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnimeFilesSupportsDirectFileAndRootResultWithoutExtraQueries(t *testing.T) {
	for _, kind := range []string{"file", "root"} {
		t.Run(kind, func(t *testing.T) {
			f, db, session, job := completedAnimeFilesFixture(t)
			resultID := "300"
			if kind == "root" {
				resultID = "200"
			}
			if _, err := db.Exec(`UPDATE anime_download_jobs SET result_object_id=? WHERE id=?`, resultID, job.ID); err != nil {
				t.Fatal(err)
			}
			pages := 0
			gateway := normalAnimeFileGateway(t, &pages)

			gateway.page = func(_ context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
				pages++
				if cid != "200" {
					t.Fatal("unnecessary directory read")
				}
				file := animeFileVideo()
				file.ParentCID = "200"
				if kind == "file" {
					file.ID = "300"
				}
				return p115.AnimeDrivePage{CID: cid, Count: 1, Entries: []model.AnimeDriveEntry{file}}, nil
			}
			f.service.FileGateway = gateway
			out, err := f.service.CollectDownloadedFiles(context.Background(), session, job.ID)
			if err != nil || out.FileState != "verified" || len(out.Files.Entries) != 2 || pages != 1 {
				t.Fatal(out, pages, err)
			}
		})
	}
}

func TestAnimeFilesIncompleteOrForeignTreeNeverBecomesVerified(t *testing.T) {
	for _, mode := range []string{"outside root", "wrong result", "empty", "partial page", "duplicate page", "changed count", "nested", "too deep", "account changed", "read failed", "status conflict"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, job := completedAnimeFilesFixture(t)
			pages := 0
			gateway := normalAnimeFileGateway(t, &pages)
			original := gateway.page
			gateway.page = func(_ context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
				if cid == "200" {
					page, err := original(context.Background(), cid, offset, cookie)
					if mode == "outside root" {
						page.CID = "999"
					}
					if mode == "wrong result" {
						page.Entries[0].ID = "999"
					}
					return page, err
				}
				pages++
				if mode == "read failed" {
					return p115.AnimeDrivePage{}, errors.New(animeDownloadTestCookie)
				}
				file := animeFileVideo()
				count := 1
				entries := []model.AnimeDriveEntry{file}
				switch mode {
				case "empty":
					count = 0
					entries = nil
				case "partial page":
					entries = nil
				case "duplicate page":
					count = 2 // A second page repeats the first ID.
				case "changed count":
					count = 2
					if offset > 0 {
						count = 3
						file.ID = "401"
						entries = []model.AnimeDriveEntry{file}
					}
				case "nested":
					if cid == "300" {
						entries = []model.AnimeDriveEntry{{ID: "301", ParentCID: "300", Name: "nested", Kind: "directory"}}
					} else {
						file.ParentCID = "301"
						entries = []model.AnimeDriveEntry{file}
					}
				case "too deep":
					n := 299 + pages
					entries = []model.AnimeDriveEntry{{ID: strconv.Itoa(n), ParentCID: cid, Name: "nested", Kind: "directory"}}
				case "account changed":
					a, err := f.accounts.Primary(job.AccountID)
					if err != nil {
						t.Fatal(err)
					}
					replacement := "UID=123_B; SEID=replaced"
					if err := f.accounts.Update(a.ID, a.Revision, a.Name, &replacement); err != nil {
						t.Fatal(err)
					}
				}
				return p115.AnimeDrivePage{CID: cid, Offset: offset, Count: count, Entries: entries}, nil
			}
			f.service.FileGateway = gateway
			if mode == "status conflict" {
				if _, err := db.Exec(`UPDATE anime_download_jobs SET block_code='TASK_STATUS_CONFLICT' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
			}
			out, err := f.service.CollectDownloadedFiles(context.Background(), session, job.ID)
			if mode == "nested" {
				if err != nil || out.FileState != "verified" || pages != 3 {
					t.Fatal(out, err)
				}
				return
			}
			if mode == "status conflict" {
				if !errors.Is(err, storage.ErrAnimeConflict) || pages != 0 {
					t.Fatal("foreign status advanced", out, err)
				}
				return
			}
			if !errors.Is(err, ErrAnimeFilesIncomplete) || out.FileState != "needs_review" || out.Files != nil || out.WorkflowState != "active" || out.NextRunAt != nil {
				t.Fatal("bad file facts accepted", out, err)
			}
		})
	}
}

func TestAnimeFilesReadCancellationAndOldGenerationCannotSave(t *testing.T) {
	for _, mode := range []string{"cancel", "generation"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, job := completedAnimeFilesFixture(t)
			pages := 0
			gateway := normalAnimeFileGateway(t, &pages)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := gateway.page
			gateway.page = func(ctx context.Context, cid string, offset int, cookie string) (p115.AnimeDrivePage, error) {
				if cid == "200" {
					return original(ctx, cid, offset, cookie)
				}
				if mode == "cancel" {
					cancel()
					return p115.AnimeDrivePage{}, ctx.Err()
				}
				if _, err := db.Exec(`UPDATE anime_download_jobs SET generation=generation+1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				return p115.AnimeDrivePage{CID: cid, Count: 1, Entries: []model.AnimeDriveEntry{animeFileVideo()}}, nil
			}
			f.service.FileGateway = gateway
			out, err := f.service.CollectDownloadedFiles(ctx, session, job.ID)
			wantErr := context.Canceled
			if mode == "generation" {
				wantErr = storage.ErrAnimeConflict
			}
			if !errors.Is(err, wantErr) || out.Files != nil || out.FileState == "verified" {
				t.Fatal("old or cancelled read saved", out, err)
			}
		})
	}
}

func TestAnimeFilesSharedGatePreventsConcurrentDirectoryCreate(t *testing.T) {
	f, db, session, job := animeSubmitFixture(t)
	if _, err := db.Exec(`UPDATE anime_download_jobs SET directory_cid=NULL WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	var writes atomic.Int32
	f.service.FileGateway = &animeFileGatewayStub{create: func(context.Context, string, string, string) (string, error) { writes.Add(1); return "200", nil }}
	release, err := f.service.DownloadCoordinator.Acquire(context.Background(), job.UID, "magnet", job.Snapshot.InfoHash)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := f.service.EnsureDownloadDirectory(context.Background(), session, job.ID); !errors.Is(err, ErrCloudTaskBusy) || writes.Load() != 0 {
		t.Fatal(err, writes.Load())
	}
}
