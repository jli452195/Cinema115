package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type ed2kReadinessStore struct {
	ED2KSTRMStore
	read func(context.Context) (model.ED2KSTRMSettings, error)
}

func (s ed2kReadinessStore) GetED2KSTRMSettings(ctx context.Context) (model.ED2KSTRMSettings, error) {
	return s.read(ctx)
}

func TestED2KSTRMImportReadinessIsLocalAndDoesNotCreateWork(t *testing.T) {
	for _, mode := range []string{"ready", "missing-settings", "invalid-address", "missing-primary", "empty-cookie", "canceled", "storage-error"} {
		t.Run(mode, func(t *testing.T) {
			f := newED2KSTRMFixture(t)
			ctx := context.Background()
			var want error
			switch mode {
			case "missing-settings":
				f.service.Store = ed2kReadinessStore{ED2KSTRMStore: f.store, read: func(context.Context) (model.ED2KSTRMSettings, error) {
					return model.ED2KSTRMSettings{}, storage.ErrNotFound
				}}
				want = ErrGenerationInput
			case "invalid-address":
				if err := f.store.SaveED2KSTRMSettings(ctx, model.ED2KSTRMSettings{}); err != nil {
					t.Fatal(err)
				}
				want = ErrGenerationInput
			case "missing-primary", "empty-cookie":
				a, err := f.accounts.Primary("")
				if err != nil {
					t.Fatal(err)
				}
				if mode == "missing-primary" {
					if err := f.accounts.Delete(a.ID, a.Revision); err != nil {
						t.Fatal(err)
					}
					// 即使存在已登录小号，普通导入也必须等待主账号配置。
					id, err := f.accounts.Add("小号")
					if err != nil {
						t.Fatal(err)
					}
					cookie := "UID=456_A; CID=secondary"
					if err := f.accounts.Update(id, 1, "小号", &cookie); err != nil {
						t.Fatal(err)
					}
				} else {
					cookie := ""
					if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
						t.Fatal(err)
					}
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "storage-error":
				want = errors.New("read unavailable")
				f.service.Store = ed2kReadinessStore{ED2KSTRMStore: f.store, read: func(context.Context) (model.ED2KSTRMSettings, error) {
					return model.ED2KSTRMSettings{}, want
				}}
			}
			err := f.service.CheckImportReady(ctx)
			if mode == "ready" && err != nil || mode != "ready" && err == nil || want != nil && !errors.Is(err, want) {
				t.Fatalf("readiness=%v want=%v", err, want)
			}
			resources, readErr := f.store.ListED2KSTRMResources(context.Background())
			if readErr != nil || len(resources) != 0 || len(f.service.List()) != 0 || len(f.service.Tasks()) != 0 || len(f.client.calls) != 0 || f.cloud.calls != 0 {
				t.Fatalf("本地预检触发业务写入或上游访问：resources=%v err=%v", resources, readErr)
			}
		})
	}
}
