package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func TestDriveSTRMSettingsDoNotQueueAndFreezeRunningScan(t *testing.T) {
	s, db, client, mapping, root := driveSTRMTestSetup(t)
	ctx := context.Background()
	client.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "movie.mkv")}
	value, scanCtx, found, err := s.next(ctx)
	if err != nil || !found {
		t.Fatalf("next: found=%v err=%v", found, err)
	}
	before, _ := db.GetDriveSTRM(ctx, mapping.ID)
	<-s.wake // The original mapping creation woke the worker.
	calls := len(client.calls)
	settings := value.Settings
	settings.BaseURL = "https://new.example"
	settings.EventIntervalSeconds, settings.ReconcileIntervalHours, settings.FullScanIntervalHours = 0, 6, 24
	if _, err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetDriveSTRM(ctx, mapping.ID)
	if !reflect.DeepEqual(before, after) || len(s.wake) != 0 || len(client.calls) != calls {
		t.Fatal("saving settings changed, queued or requested the active mapping")
	}
	if value.Settings.EventIntervalSeconds != 20 || value.Settings.ReconcileIntervalHours != 24 || value.Settings.FullScanIntervalHours != 168 {
		t.Fatalf("running attempt's settings changed: %+v", value.Settings)
	}
	stats, scanErr := s.scan(scanCtx, value)
	if scanErr != nil || stats.Written != 1 {
		t.Fatalf("running scan=%+v err=%v", stats, scanErr)
	}
	if err := s.finish(ctx, mapping.ID, stats, scanErr); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(root, "strm", "movie.strm")
	data, _ := os.ReadFile(filename)
	if !strings.HasPrefix(string(data), "https://cinema.example/") {
		t.Fatalf("running scan used updated settings: %s", data)
	}
	before, _ = db.GetDriveSTRM(ctx, mapping.ID)
	if _, err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	after, _ = db.GetDriveSTRM(ctx, mapping.ID)
	if !reflect.DeepEqual(before, after) || len(s.wake) != 0 {
		t.Fatal("saving settings queued or changed an idle mapping")
	}
	// A new service reads the saved settings; only an explicit full scan
	// changes already-generated files to use the new service address.
	s = NewDriveSTRMService(db, s.Accounts, client, root)
	if err := s.Queue(ctx, mapping.ID, true); err != nil {
		t.Fatal(err)
	}
	if stats, err := driveSTRMTestAttempt(t, s); err != nil || stats.Written != 1 {
		t.Fatalf("next scan=%+v err=%v", stats, err)
	}
	data, _ = os.ReadFile(filename)
	if !strings.HasPrefix(string(data), "https://new.example/") {
		t.Fatalf("next scan ignored saved settings: %s", data)
	}
}

type driveSTRMUnsavedSettings struct{ DriveSTRMStore }

func (driveSTRMUnsavedSettings) GetDriveSTRMSettings(context.Context) (model.DriveSTRMSettings, error) {
	return model.DriveSTRMSettings{}, storage.ErrNotFound
}

func TestDriveSTRMUnsavedSettingsIdleWithoutCloudRequests(t *testing.T) {
	s, db, client, mapping, _ := driveSTRMTestSetup(t)
	s.Store = driveSTRMUnsavedSettings{s.Store}
	client.calls = nil
	ctx := context.Background()
	if _, err := s.Save(ctx, mapping); !errors.Is(err, ErrGenerationInput) {
		t.Fatalf("save without settings: %v", err)
	}
	if err := s.Queue(ctx, mapping.ID, true); !errors.Is(err, ErrGenerationInput) {
		t.Fatalf("scan without settings: %v", err)
	}
	before, _ := db.GetDriveSTRM(ctx, mapping.ID)
	if _, _, found, err := s.next(ctx); err != nil || found {
		t.Fatalf("unconfigured worker did not idle: found=%v err=%v", found, err)
	}
	events := NewDriveSTRMEvents(s, driveSTRMLifeFake(func(context.Context, int, int, string) (p115.LifeEventPage, error) {
		t.Fatal("unconfigured events requested 115")
		return p115.LifeEventPage{}, nil
	}))
	if err := events.Check(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetDriveSTRM(ctx, mapping.ID)
	if len(client.calls) != 0 || !reflect.DeepEqual(before, after) {
		t.Fatal("unconfigured worker requested 115 or changed the mapping")
	}
	mapping.Enabled = false
	if err := db.SaveDriveSTRM(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMonitoring(ctx, mapping.ID, true); !errors.Is(err, ErrGenerationInput) {
		t.Fatalf("monitor enabled without settings: %v", err)
	}
}

func TestDriveSTRMSettingsValidationAndNormalization(t *testing.T) {
	s, _, client, _, _ := driveSTRMTestSetup(t)
	ctx := context.Background()
	valid := model.DriveSTRMSettings{BaseURL: " https://cinema.example/ ", VideoExtensions: ".MP4,mkv,MP4", MetadataExtensions: ".SRT,ass,SRT", MinSizeMB: 8}
	got, err := s.SaveSettings(ctx, valid)
	if err != nil || got.BaseURL != "https://cinema.example" || got.VideoExtensions != "mkv,mp4" || got.MetadataExtensions != "ass,srt" {
		t.Fatalf("normalize=%+v err=%v", got, err)
	}
	for name, change := range map[string]func(*model.DriveSTRMSettings){
		"blank URL":        func(v *model.DriveSTRMSettings) { v.BaseURL = "" },
		"URL path":         func(v *model.DriveSTRMSettings) { v.BaseURL = "https://cinema.example/path" },
		"blank videos":     func(v *model.DriveSTRMSettings) { v.VideoExtensions = "" },
		"invalid videos":   func(v *model.DriveSTRMSettings) { v.VideoExtensions = "mk/v" },
		"overlap":          func(v *model.DriveSTRMSettings) { v.MetadataExtensions = "mkv" },
		"strm":             func(v *model.DriveSTRMSettings) { v.MetadataExtensions = "strm" },
		"negative size":    func(v *model.DriveSTRMSettings) { v.MinSizeMB = -1 },
		"excessive size":   func(v *model.DriveSTRMSettings) { v.MinSizeMB = 1<<30 + 1 },
		"event period":     func(v *model.DriveSTRMSettings) { v.EventIntervalSeconds = 1 },
		"reconcile period": func(v *model.DriveSTRMSettings) { v.ReconcileIntervalHours = -1 },
		"full period":      func(v *model.DriveSTRMSettings) { v.FullScanIntervalHours = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := valid
			change(&bad)
			if _, err := s.SaveSettings(ctx, bad); !errors.Is(err, ErrGenerationInput) {
				t.Fatalf("invalid settings accepted: %v", err)
			}
			current, err := s.Settings(ctx)
			if err != nil || current != got {
				t.Fatalf("invalid settings replaced saved settings: %+v %v", current, err)
			}
		})
	}
	if len(client.calls) != 1 { // Only the original mapping creation read 115.
		t.Fatalf("settings caused cloud requests: %v", client.calls)
	}
}

func TestDriveSTRMSettingsCanBeSavedWithoutPrimaryAccount(t *testing.T) {
	_, db, _, _, root := driveSTRMTestSetup(t)
	s := NewDriveSTRMService(db, nil, nil, root)
	settings := model.DefaultDriveSTRMSettings()
	settings.BaseURL, settings.VideoExtensions = "https://cinema.example", "mkv"
	if _, err := s.SaveSettings(context.Background(), settings); err != nil {
		t.Fatalf("settings required a configured account: %v", err)
	}
}
