package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type driveSTRMTreeTestClient struct {
	*driveSTRMTestClient
	leaves      []string
	exportCalls []string
	pending     bool
	onStart     func()
}

func (c *driveSTRMTreeTestClient) StartDriveTreeExport(ctx context.Context, cid, cookie string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.exportCalls = append(c.exportCalls, "start:"+cid+":"+cookie)
	if c.onStart != nil {
		c.onStart()
	}
	return "12345", nil
}

func (c *driveSTRMTreeTestClient) DriveTreeExportStatus(ctx context.Context, id, cookie string) (p115.DriveTreeExport, error) {
	c.exportCalls = append(c.exportCalls, "status:"+id+":"+cookie)
	if err := ctx.Err(); err != nil {
		return p115.DriveTreeExport{}, err
	}
	job := p115.DriveTreeExport{ID: id}
	if !c.pending {
		job.FileID, job.PickCode, job.Name = "999", "abc123", "library.txt"
	}
	return job, nil
}

func (c *driveSTRMTreeTestClient) ReadDriveTreeExport(ctx context.Context, job p115.DriveTreeExport, root, cookie string, visit func(string) error) error {
	c.exportCalls = append(c.exportCalls, "read:"+job.ID+":"+root+":"+cookie)
	for _, relative := range c.leaves {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(relative); err != nil {
			return err
		}
	}
	return nil
}

func (c *driveSTRMTreeTestClient) DeleteDriveTreeExport(ctx context.Context, id, cookie string) error {
	c.exportCalls = append(c.exportCalls, "delete:"+id+":"+cookie)
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.pending {
		return p115.ErrDriveTreeExportPending
	}
	return nil
}

func driveSTRMTreeSetup(t *testing.T) (*DriveSTRMService, *storage.Store, *driveSTRMTreeTestClient, model.DriveSTRM, string) {
	t.Helper()
	s, db, base, value, root := driveSTRMTestSetup(t)
	base.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv"), {ID: "200", Name: "Season 01", Directory: true}, {ID: "10", Name: "poster.jpg", Size: 5, SHA1: strings.Repeat("B", 40)}}
	base.files["200"] = []p115.ShareFile{driveSTRMTestVideo("2", "E01.mkv")}
	base.metadata["10"] = []byte("image")
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	client := &driveSTRMTreeTestClient{driveSTRMTestClient: base, leaves: []string{"movie.mkv", "Season 01/E01.mkv", "poster.jpg", "ignored.zip"}}
	s.Client = client
	base.calls = nil
	return s, db, client, value, root
}

func driveSTRMTreeDue(t *testing.T, db *storage.Store, id string) {
	t.Helper()
	value, err := db.GetDriveSTRM(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	value.Queued, value.FullScan, value.PendingDirectories = false, false, nil
	value.NextScanAt = time.Now().Add(-time.Second)
	// A daily tree check is due while the weekly metadata scan is still fresh.
	value.LastFullScanAt = time.Now()
	if err := db.SaveDriveSTRM(context.Background(), value); err != nil {
		t.Fatal(err)
	}
}

func TestDriveSTRMTreeExistingOutputsAvoidAllFileLists(t *testing.T) {
	s, db, client, value, _ := driveSTRMTreeSetup(t)
	driveSTRMTreeDue(t, db, value.ID)
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Videos != 2 || stats.Unchanged != 3 || stats.Written != 0 || stats.MetadataWritten != 0 {
		t.Fatalf("tree stats=%+v err=%v", stats, err)
	}
	for _, call := range client.calls {
		if strings.HasPrefix(call, "list:") || strings.HasPrefix(call, "metadata:") {
			t.Fatalf("unchanged tree read source files: %v", client.calls)
		}
	}
	if client.metadataCalls != 1 {
		t.Fatalf("unchanged attachments downloaded again: %d", client.metadataCalls)
	}
	foundRead := false
	for _, call := range client.exportCalls {
		if !strings.HasSuffix(call, ":UID=42_A") {
			t.Fatalf("export changed credentials: %v", client.exportCalls)
		}
		foundRead = foundRead || call == "read:12345:library:UID=42_A"
	}
	if !foundRead {
		t.Fatalf("tree root was not checked: %v", client.exportCalls)
	}
	jobs, err := db.ListDriveSTRMExports(context.Background())
	if err != nil || len(jobs) != 0 {
		t.Fatalf("completed export leaked cleanup record: %v %v", jobs, err)
	}
}

func TestDriveSTRMTreeMissingOutputScansOnlyKnownDirectory(t *testing.T) {
	s, db, client, value, root := driveSTRMTreeSetup(t)
	filename := filepath.Join(root, "strm", "Season 01", "E01.strm")
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	driveSTRMTreeDue(t, db, value.ID)
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Written != 1 {
		t.Fatalf("repair stats=%+v err=%v", stats, err)
	}
	listed := make([]string, 0, 1)
	for _, call := range client.calls {
		if strings.HasPrefix(call, "list:") {
			listed = append(listed, call)
		}
	}
	if len(listed) != 1 || listed[0] != "list:200:UID=42_A" {
		t.Fatalf("missing child triggered unrelated scans: %v", client.calls)
	}
	if _, err := os.Stat(filename); err != nil {
		t.Fatalf("missing child not repaired: %v", err)
	}
}

func TestDriveSTRMTreeNewDirectoryFallsBackToNearestKnownAncestor(t *testing.T) {
	s, db, client, value, root := driveSTRMTreeSetup(t)
	client.leaves = append(client.leaves, "Season 01/Specials/new.mkv")
	client.files["200"] = append(client.files["200"], p115.ShareFile{ID: "400", Name: "Specials", Directory: true})
	client.files["400"] = []p115.ShareFile{driveSTRMTestVideo("3", "new.mkv")}
	client.paths["400"] = "/library/Season 01/Specials"
	driveSTRMTreeDue(t, db, value.ID)
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Written != 1 {
		t.Fatalf("new child stats=%+v err=%v", stats, err)
	}
	wanted := map[string]bool{"list:200:UID=42_A": false, "list:400:UID=42_A": false}
	for _, call := range client.calls {
		if !strings.HasPrefix(call, "list:") {
			continue
		}
		if _, ok := wanted[call]; !ok {
			t.Fatalf("new directory caused an unrelated scan: %v", client.calls)
		}
		wanted[call] = true
	}
	for call, found := range wanted {
		if !found {
			t.Fatalf("missing ancestor/child list %s: %v", call, client.calls)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "strm", "Season 01", "Specials", "new.strm")); err != nil {
		t.Fatalf("new nested output missing: %v", err)
	}
}

func TestDriveSTRMTreeCanceledExportCleanupSurvivesRestartAndMappingDeletion(t *testing.T) {
	for _, removeMapping := range []bool{false, true} {
		name := "restart"
		if removeMapping {
			name = "mapping-removed"
		}
		t.Run(name, func(t *testing.T) {
			s, db, client, value, root := driveSTRMTreeSetup(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client.pending, client.onStart = true, cancel
			value.Mode = "tree"
			if _, err := s.scan(ctx, value); !errors.Is(err, context.Canceled) {
				t.Fatalf("scan lost cancellation: %v", err)
			}
			jobs, err := db.ListDriveSTRMExports(context.Background())
			if err != nil || len(jobs) != 1 || jobs["12345"] != value.AccountID {
				t.Fatalf("canceled accepted export was not persisted: %v %v", jobs, err)
			}
			before := len(client.exportCalls)
			if _, err := s.scan(context.Background(), value); err == nil {
				t.Fatal("a second export started while the first still awaits cleanup")
			}
			if len(client.exportCalls) != before {
				t.Fatal("pending export caused another cloud request")
			}
			if removeMapping {
				if err := s.Delete(context.Background(), value.ID); err != nil {
					t.Fatal(err)
				}
				jobs, err = db.ListDriveSTRMExports(context.Background())
				if err != nil || jobs["12345"] != value.AccountID {
					t.Fatalf("mapping deletion lost cleanup identity: %v %v", jobs, err)
				}
			}
			// Resume only from the durable queue, without the original scan state.
			s = NewDriveSTRMService(db, s.Accounts, client, root)
			if err := s.cleanDriveSTRMExports(context.Background()); err != nil {
				t.Fatalf("pending cleanup should defer: %v", err)
			}
			jobs, _ = db.ListDriveSTRMExports(context.Background())
			if jobs["12345"] != value.AccountID {
				t.Fatal("pending upstream job discarded")
			}
			client.pending = false
			if err := s.cleanDriveSTRMExports(context.Background()); err != nil {
				t.Fatal(err)
			}
			jobs, err = db.ListDriveSTRMExports(context.Background())
			if err != nil || len(jobs) != 0 {
				t.Fatalf("ready export cleanup did not complete: %v %v", jobs, err)
			}
		})
	}
}

func TestDriveSTRMTreeCleanupNeverUsesReplacementPrimary(t *testing.T) {
	for _, mode := range []string{"switch-uid", "delete-and-recreate"} {
		t.Run(mode, func(t *testing.T) {
			s, db, client, value, _ := driveSTRMTreeSetup(t)
			ctx := context.Background()
			if err := db.SaveDriveSTRMExport(ctx, value.AccountID, "12345"); err != nil {
				t.Fatal(err)
			}
			account, _ := s.Accounts.Get(value.AccountID)
			if mode == "switch-uid" {
				cookie := "UID=99_A"
				if err := s.Accounts.Update(account.ID, account.Revision, account.Name, &cookie); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.Accounts.Delete(account.ID, account.Revision); err != nil {
					t.Fatal(err)
				}
				id, err := s.Accounts.AddPrimary("replacement")
				if err != nil {
					t.Fatal(err)
				}
				replacement, _ := s.Accounts.Get(id)
				cookie := "UID=42_B"
				if err := s.Accounts.Update(id, replacement.Revision, replacement.Name, &cookie); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.cleanDriveSTRMExports(ctx); err != nil {
				t.Fatal(err)
			}
			if len(client.exportCalls) != 0 {
				t.Fatalf("old export accessed with replacement primary: %v", client.exportCalls)
			}
			jobs, err := db.ListDriveSTRMExports(ctx)
			if err != nil || len(jobs) != 1 || jobs["12345"] != value.AccountID {
				t.Fatalf("old export ownership changed or disappeared: %v %v", jobs, err)
			}
		})
	}
}

func TestDriveSTRMTreeDuplicateExportIDPreservesOtherAccountCleanup(t *testing.T) {
	s, db, _, value, _ := driveSTRMTreeSetup(t)
	if err := db.SaveDriveSTRMExport(context.Background(), "previous-primary", "12345"); err != nil {
		t.Fatal(err)
	}
	value.Mode = "tree"
	if _, err := s.scan(context.Background(), value); err == nil {
		t.Fatal("export was reassigned across account configurations")
	}
	jobs, err := db.ListDriveSTRMExports(context.Background())
	if err != nil || len(jobs) != 1 || jobs["12345"] != "previous-primary" {
		t.Fatalf("deferred cleanup removed another account's job: %v %v", jobs, err)
	}
}
