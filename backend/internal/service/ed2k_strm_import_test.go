package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/model"
)

func TestED2KSTRMTargetSnapshotRejectsChangesBeforeAdmission(t *testing.T) {
	for _, change := range []string{"name", "directory", "assignment", "deleted"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			if err := f.service.AssignTarget(ctx, f.target.ID, "bot-one"); err != nil {
				t.Fatal(err)
			}
			snapshot, _ := f.service.Target(f.target.ID)
			changed := snapshot
			var err error
			switch change {
			case "name":
				changed.Name = "新目录名称"
				_, err = f.service.SaveTarget(ctx, changed)
			case "directory":
				changed.OutputDir = "another-output"
				_, err = f.service.SaveTarget(ctx, changed)
			case "assignment":
				err = f.service.AssignTarget(ctx, snapshot.ID, "bot-two")
			case "deleted":
				err = f.service.DeleteTarget(ctx, snapshot.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			links := []string{ed2kSTRMTestLink("Film.mkv", ed2kSTRMTestHash, ed2kSTRMTestSize)}
			if _, err := f.service.ImportToTarget(ctx, snapshot, links, "snapshot-request"); !errors.Is(err, ErrGenerationInput) {
				t.Fatalf("stale target accepted: %v", err)
			}
			if len(f.service.List()) != 0 || len(f.service.Tasks()) != 0 || len(f.client.calls) != 0 || f.cloud.calls != 0 {
				t.Fatal("rejected snapshot created resources, tasks or cloud operations")
			}
			stored, err := f.store.ListED2KSTRMTasks(ctx)
			if err != nil || len(stored) != 0 {
				t.Fatalf("rejected snapshot persisted output tasks: %+v err=%v", stored, err)
			}
			if change == "deleted" {
				return
			}
			current, _ := f.service.Target(snapshot.ID)
			accepted, err := f.service.ImportToTarget(ctx, current, links, "snapshot-request")
			if err != nil || len(accepted.Tasks) != 1 {
				t.Fatalf("current target rejected: %+v err=%v", accepted, err)
			}
			task := accepted.Tasks[0]
			if task.TargetID != current.ID || task.TargetName != current.Name || task.OutputDir != current.OutputDir {
				t.Fatalf("accepted output did not pin the selected target: %+v expected=%+v", task, current)
			}
			if _, err := f.service.ImportToTarget(ctx, model.ED2KSTRMTarget{ID: current.ID}, links, "snapshot-request"); !errors.Is(err, ErrGenerationInput) {
				t.Fatalf("replayed request bypassed target authorization: %v", err)
			}
		})
	}
}
