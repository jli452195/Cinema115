package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type cleanupFakeDrive struct {
	*organizeFakeDrive
	deleted      []string
	deleteErr    error
	applyOnError bool
	beforeDelete func()
	noDelete     bool
}

func (d *cleanupFakeDrive) DeleteEntries(ctx context.Context, ids []string, cookie string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.cookies = append(d.cookies, cookie)
	d.deleted = append(d.deleted, ids...)
	if d.beforeDelete != nil {
		d.beforeDelete()
	}
	if !d.noDelete && (d.deleteErr == nil || d.applyOnError) {
		for _, id := range ids {
			for parent, files := range d.dirs {
				for i, file := range files {
					if file.ID == id {
						d.dirs[parent] = append(files[:i], files[i+1:]...)
						break
					}
				}
			}
			delete(d.dirs, id)
		}
	}
	return d.deleteErr
}

func newCleanupTest(t *testing.T) (*ManualOrganize, *cleanupFakeDrive, string, *failingAccountStore) {
	t.Helper()
	s, drive, id, store := newOrganizeTest(t)
	d := &cleanupFakeDrive{organizeFakeDrive: drive}
	s.drive = d
	return s, d, id, store
}

func cleanupPreview(t *testing.T, s *ManualOrganize, id, mode, parent string, ids ...string) OrganizeCleanup {
	t.Helper()
	plan, err := s.PreviewCleanup(context.Background(), OrganizeCleanupInput{AccountID: id, ParentID: parent, ParentPath: "/来源", Mode: mode, IDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestManualOrganizeCleanupSelectedAndReplay(t *testing.T) {
	s, d, id, store := newCleanupTest(t)
	d.pageSize = 1
	plan := cleanupPreview(t, s, id, "selected", "30", "101", "104")
	if len(d.deleted) != 0 {
		t.Fatal("preview mutated drive")
	}
	result, err := s.ExecuteCleanup(context.Background(), id, plan.ID)
	if err != nil || result.Status != "completed" || len(d.deleted) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(d.dirs["30"]) != 2 || d.dirs["30"][0].ID != "102" {
		t.Fatal("unselected entries changed")
	}
	_, err = s.ExecuteCleanup(context.Background(), id, plan.ID)
	if err != nil || len(d.deleted) != 2 {
		t.Fatalf("replay issued delete: %v", err)
	}
	if strings.Contains(store.data, "secret-cookie") || strings.Contains(store.data, "UID=") {
		t.Fatal("credentials persisted")
	}
	restarted, err := newManualOrganizeTest(store, s.accounts, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = restarted.ExecuteCleanup(context.Background(), id, plan.ID)
	if err != nil || len(d.deleted) != 2 {
		t.Fatalf("restart replay: %v", err)
	}
	// Folder deletion includes its contents without consulting metadata.
	folder := cleanupPreview(t, s, id, "selected", "10", "30")
	result, err = s.ExecuteCleanup(context.Background(), id, folder.ID)
	if err != nil || result.Status != "completed" || len(d.dirs["10"]) != 0 {
		t.Fatalf("folder result=%+v err=%v", result, err)
	}
}

func TestManualOrganizeCleanupEmptyBottomUp(t *testing.T) {
	s, d, id, _ := newCleanupTest(t)
	d.dirs["30"] = []p115.ShareFile{{ID: "40", Name: "nested", Directory: true}, {ID: "101", Name: "keep.txt", Size: 1}}
	d.dirs["40"] = []p115.ShareFile{{ID: "41", Name: "leaf", Directory: true}}
	d.dirs["41"] = nil
	d.pageSize = 1
	plan := cleanupPreview(t, s, id, "empty", "10")
	if len(plan.Items) != 2 || plan.Items[0].ID != "41" || plan.Items[1].ID != "40" {
		t.Fatalf("plan=%+v", plan)
	}
	result, err := s.ExecuteCleanup(context.Background(), id, plan.ID)
	if err != nil || result.Status != "completed" || !reflect.DeepEqual(d.deleted, []string{"41", "40"}) {
		t.Fatalf("result=%+v deletes=%v err=%v", result, d.deleted, err)
	}
	if len(d.dirs["30"]) != 1 || len(d.dirs["10"]) != 1 {
		t.Fatal("nonempty folder or root deleted")
	}
}

func TestManualOrganizeCleanupRechecksChanges(t *testing.T) {
	for _, change := range []string{"new-content", "moved-ancestor", "renamed", "resized"} {
		t.Run(change, func(t *testing.T) {
			s, d, id, _ := newCleanupTest(t)
			d.dirs["30"] = []p115.ShareFile{{ID: "40", Name: "empty", Directory: true}}
			d.dirs["40"] = nil
			mode, parent, ids := "empty", "10", []string{}
			if change == "resized" {
				mode, parent, ids = "selected", "30", []string{"101"}
				d.dirs["30"] = []p115.ShareFile{{ID: "101", Name: "file", Size: 1}}
			}
			plan := cleanupPreview(t, s, id, mode, parent, ids...)
			switch change {
			case "new-content":
				d.dirs["40"] = []p115.ShareFile{{ID: "999", Name: "new.txt"}}
			case "moved-ancestor":
				d.dirs["20"], d.dirs["10"] = d.dirs["10"], nil
			case "renamed":
				d.dirs["10"][0].Name = "changed"
			case "resized":
				d.dirs["30"][0].Size = 2
			}
			result, err := s.ExecuteCleanup(context.Background(), id, plan.ID)
			if err != nil || result.Status != "partial" || len(d.deleted) != 0 {
				t.Fatalf("result=%+v deletes=%v err=%v", result, d.deleted, err)
			}
		})
	}
}

func TestManualOrganizeCleanupRejectsInvalidSelections(t *testing.T) {
	s, d, id, _ := newCleanupTest(t)
	for _, ids := range [][]string{nil, {"0"}, {"10"}, {"30", "30"}, {"101"}, {"999"}, {"-1"}} {
		_, err := s.PreviewCleanup(context.Background(), OrganizeCleanupInput{AccountID: id, ParentID: "10", ParentPath: "/来源", Mode: "selected", IDs: ids})
		if err == nil {
			t.Fatalf("accepted ids %v", ids)
		}
	}
	for _, account := range []string{"", "missing"} {
		_, err := s.PreviewCleanup(context.Background(), OrganizeCleanupInput{AccountID: account, ParentID: "10", ParentPath: "/来源", Mode: "selected", IDs: []string{"30"}})
		if err == nil {
			t.Fatalf("accepted account %q", account)
		}
	}
	plan := cleanupPreview(t, s, id, "selected", "10", "30")
	if _, err := s.ExecuteCleanup(context.Background(), id, "forged"); err == nil {
		t.Fatal("accepted forged plan")
	}
	if _, err := s.ExecuteCleanup(context.Background(), "", plan.ID); err == nil {
		t.Fatal("account fallback")
	}
	s.cleanupPlans[plan.ID] = func() OrganizeCleanup {
		p := s.cleanupPlans[plan.ID]
		p.CreatedAt = time.Now().Add(-11 * time.Minute)
		return p
	}()
	if _, err := s.ExecuteCleanup(context.Background(), id, plan.ID); err == nil {
		t.Fatal("accepted expired plan")
	}
	if len(d.deleted) != 0 {
		t.Fatal("invalid input mutated drive")
	}
}

func TestManualOrganizeCleanupAccountSnapshotAndRevision(t *testing.T) {
	for _, changeBefore := range []bool{false, true} {
		t.Run(map[bool]string{true: "before", false: "during"}[changeBefore], func(t *testing.T) {
			s, d, id, _ := newCleanupTest(t)
			plan := cleanupPreview(t, s, id, "selected", "30", "101", "104")
			original, _ := s.accounts.Get(id)
			change := func() {
				cookie := "UID=new-account-cookie"
				if err := s.accounts.Update(id, original.Revision, "changed", &cookie); err != nil {
					t.Fatal(err)
				}
				d.beforeDelete = nil
			}
			if changeBefore {
				change()
			} else {
				d.beforeDelete = change
			}
			result, err := s.ExecuteCleanup(context.Background(), id, plan.ID)
			if changeBefore {
				if err == nil || len(d.deleted) != 0 {
					t.Fatal("accepted changed credentials")
				}
				return
			}
			if err != nil || result.Status != "completed" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			for _, cookie := range d.cookies {
				if cookie != original.Cookie {
					t.Fatal("credential snapshot changed")
				}
			}
		})
	}
}

func TestManualOrganizeCleanupStopsOnUncertainWrite(t *testing.T) {
	for _, mode := range []string{"timeout", "false-success", "readback-failed"} {
		t.Run(mode, func(t *testing.T) {
			s, d, id, store := newCleanupTest(t)
			plan := cleanupPreview(t, s, id, "selected", "30", "101", "104")
			switch mode {
			case "timeout":
				d.deleteErr, d.applyOnError = errors.New("timeout UID=123_snapshot; SEID=secret-cookie"), true
			case "false-success":
				d.noDelete = true
			case "readback-failed":
				d.beforeDelete = func() { d.beforeRead = func(string, int) error { return errors.New("read failed") } }
			}
			result, err := s.ExecuteCleanup(context.Background(), id, plan.ID)
			if err != nil || result.Status != "partial" || result.Items[0].Status != "uncertain" || result.Items[1].Status != "pending" || len(d.deleted) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw)+store.data, "secret-cookie") {
				t.Fatal("secret in result")
			}
			_, err = s.ExecuteCleanup(context.Background(), id, plan.ID)
			if err != nil || len(d.deleted) != 1 {
				t.Fatal("uncertain request replayed")
			}
		})
	}
}

func TestManualOrganizeCleanupMutualExclusion(t *testing.T) {
	s, d, id, _ := newCleanupTest(t)
	organizePlan := organizeTestPreview(t, s, id, []OrganizeSelection{{ID: "101", Season: 1, Episode: 1}})
	plan := cleanupPreview(t, s, id, "selected", "30", "104")
	d.beforeDelete = func() {
		if _, err := s.Start(id, organizePlan.ID); !errors.Is(err, ErrOrganizeConflict) {
			t.Fatalf("organize started while deleting: %v", err)
		}
		if _, err := s.ExecuteCleanup(context.Background(), id, plan.ID); err != nil {
			t.Fatalf("in-flight replay: %v", err)
		}
	}
	if _, err := s.ExecuteCleanup(context.Background(), id, plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(id, organizePlan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewCleanup(context.Background(), OrganizeCleanupInput{AccountID: id, ParentID: "10", ParentPath: "/来源", Mode: "empty"}); !errors.Is(err, ErrOrganizeConflict) {
		t.Fatalf("cleanup during organize: %v", err)
	}
}

func TestManualOrganizeCleanupReadOrPersistenceFailurePreventsDelete(t *testing.T) {
	for _, mode := range []string{"read", "save", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, d, id, store := newCleanupTest(t)
			plan := cleanupPreview(t, s, id, "selected", "30", "101")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "read":
				d.beforeRead = func(string, int) error { return errors.New("read failed") }
			case "save":
				store.err = errors.New("disk full")
			case "cancel":
				cancel()
			}
			_, _ = s.ExecuteCleanup(ctx, id, plan.ID)
			if len(d.deleted) != 0 {
				t.Fatal("delete despite failed preflight")
			}
			if s.cleaning {
				t.Fatal("reservation leaked")
			}
		})
	}
}

func TestManualOrganizeCleanupRestartInterruptsWithoutReplaying(t *testing.T) {
	s, d, id, store := newCleanupTest(t)
	plan := cleanupPreview(t, s, id, "selected", "10", "30")
	plan.Status, plan.Items[0].Status = "running", "uncertain"
	if err := s.cleanupCheckpoint(&plan); err != nil {
		t.Fatal(err)
	}
	restarted, err := newManualOrganizeTest(store, s.accounts, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := restarted.ExecuteCleanup(context.Background(), id, plan.ID)
	if err != nil || result.Status != "interrupted" || result.Items[0].Status != "uncertain" || len(d.deleted) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
