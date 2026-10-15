package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

type subtitleSourceFakeDrive struct {
	*subtitleFakeDrive
	reads       int
	readCookies []string
	readHook    func()
}

func (d *subtitleSourceFakeDrive) ReadSubtitleSource(ctx context.Context, parent string, file p115.ShareFile, cookie string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.reads++
	d.readCookies = append(d.readCookies, cookie)
	if d.readHook != nil {
		d.readHook()
	}
	return []byte("subtitle"), nil
}

func newSubtitleSourceTest(t *testing.T) (*ManualOrganize, *subtitleSourceFakeDrive, SubtitleSourcesInput, *failingAccountStore) {
	t.Helper()
	s, drive, id, job, store := newSubtitleTest(t)
	d := &subtitleSourceFakeDrive{subtitleFakeDrive: drive}
	s.drive = d
	file := p115.ShareFile{ID: "600", Name: "episode01.zip", Size: 8}
	d.dirs["30"] = append(d.dirs["30"], file)
	input := SubtitleSourcesInput{AccountID: id, JobID: job.ID, Files: []SubtitleSourceFile{{ShareFile: file, ParentID: "30"}}}
	return s, d, input, store
}

func TestSubtitleSourcesPinCredentialsAndKeepPayloadOutOfStorage(t *testing.T) {
	s, drive, input, store := newSubtitleSourceTest(t)
	plan, err := s.PrepareSubtitleSources(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	drive.readHook = func() {
		a, _ := s.accounts.Get(input.AccountID)
		cookie := "UID=123_snapshot; SEID=new-credential"
		if err := s.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
			t.Fatal(err)
		}
	}
	content, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600")
	if err != nil || string(content.Data) != "subtitle" || !strings.Contains(drive.readCookies[0], "secret-cookie") {
		t.Fatal("snapshot changed", err)
	}
	content.Close()
	if _, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600"); err == nil || drive.reads != 1 {
		t.Fatal("credential change did not invalidate source")
	}
	serialized, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "cookie") || strings.Contains(store.data, "secret-cookie") || strings.Contains(store.data, plan.ID) {
		t.Fatal("source secret persisted or returned")
	}
}

func TestSubtitleSourcesRejectStaleSelectionSplitAndInvalidScope(t *testing.T) {
	for _, mode := range []string{"missing-account", "job", "parent", "name", "size", "split", "duplicate", "unsupported", "oversize", "raw-oversize"} {
		t.Run(mode, func(t *testing.T) {
			s, drive, input, _ := newSubtitleSourceTest(t)
			switch mode {
			case "missing-account":
				input.AccountID = ""
			case "job":
				input.JobID = "foreign"
			case "parent":
				input.Files[0].ParentID = "20"
			case "name":
				input.Files[0].Name = "changed.zip"
			case "size":
				input.Files[0].Size++
			case "split":
				input.Files[0].Name = "subs.part1.rar"
			case "duplicate":
				input.Files = append(input.Files, input.Files[0])
			case "unsupported":
				input.Files[0].Name = "video.mkv"
			case "oversize":
				input.Files[0].Size = p115.MaxSubtitleSourceBytes + 1
			case "raw-oversize":
				input.Files[0].Name = "big.srt"
				input.Files[0].Size = p115.MaxSubtitleBytes + 1
			}
			if _, err := s.PrepareSubtitleSources(context.Background(), input); err == nil {
				t.Fatal("invalid selection accepted")
			}
			if drive.reads != 0 {
				t.Fatal("invalid selection downloaded")
			}
		})
	}
}

func TestSubtitleSourcesExpirationCancellationAndRelease(t *testing.T) {
	s, drive, input, _ := newSubtitleSourceTest(t)
	plan, err := s.PrepareSubtitleSources(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "601"); err == nil {
		t.Fatal("unselected file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ReadSubtitleSource(ctx, input.AccountID, plan.ID, "600"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel lost", err)
	}
	if s.subtitleSourceReads != 0 {
		t.Fatal("download slot leaked")
	}
	s.ReleaseSubtitleSources("foreign", plan.ID)
	content, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600")
	if err != nil {
		t.Fatal("foreign release removed source", err)
	}
	content.Close()
	s.ReleaseSubtitleSources(input.AccountID, plan.ID)
	if _, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600"); err == nil {
		t.Fatal("released source accepted")
	}
	plan, err = s.PrepareSubtitleSources(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	expired := s.subtitleSources[plan.ID]
	expired.ExpiresAt = time.Now().Add(-time.Second)
	s.subtitleSources[plan.ID] = expired
	reads := drive.reads
	if _, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600"); err == nil || reads != drive.reads {
		t.Fatal("expired source downloaded")
	}
	if _, ok := s.subtitleSources[plan.ID]; ok {
		t.Fatal("expired credential snapshot retained")
	}
}

func TestSubtitleSourceDownloadReservationLastsUntilResponseCloses(t *testing.T) {
	s, drive, input, _ := newSubtitleSourceTest(t)
	plan, err := s.PrepareSubtitleSources(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600"); err == nil || drive.reads != 2 {
		t.Fatal("download buffers unbounded")
	}
	first.Close()
	first.Close()
	third, err := s.ReadSubtitleSource(context.Background(), input.AccountID, plan.ID, "600")
	if err != nil {
		t.Fatal("response did not release slot", err)
	}
	third.Close()
	if s.subtitleSourceReads != 1 {
		t.Fatal("double close released another response")
	}
}
