package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localDownloadSubtitle(video, name string, data []byte) DownloadSubtitleSelection {
	digest := sha256.Sum256(data)
	return DownloadSubtitleSelection{LocalID: strings.Repeat("a", 32), OriginalName: name, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)), VideoID: video, Language: "zh-CN", Version: "Group"}
}

func TestDownloadLocalSubtitlesPersistRestartAndLostUploadReceipt(t *testing.T) {
	s, drive, _, _, transport, in := gardenFixture(t)
	s.SubtitleAttachments = t.TempDir()
	data := []byte("[Script Info]\nTitle: local subtitle\n")
	in.Subtitles = []DownloadSubtitleSelection{localDownloadSubtitle(transport.names[0], "字幕组/Show [01].chs.ass", data)}
	ctx := context.Background()
	plan, err := s.Preview(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(plan.ID); !errors.Is(err, ErrOrganizeConflict) || len(s.History()) != 0 {
		t.Fatal("started before local bytes were saved", err)
	}
	if err = s.SaveSubtitleAttachments(ctx, plan.ID, map[string][]byte{in.Subtitles[0].LocalID: []byte("changed")}); !errors.Is(err, ErrOrganizeInput) {
		t.Fatal("accepted changed bytes", err)
	}
	files := map[string][]byte{in.Subtitles[0].LocalID: data}
	for i := 0; i < 2; i++ {
		if err = s.SaveSubtitleAttachments(ctx, plan.ID, files); err != nil {
			t.Fatal("save/replay", err)
		}
	}
	// A damaged local cache can be repaired before submitting the preview,
	// including on Windows where replacing an existing file must work.
	filename, err := s.attachmentPath(plan.Download.Subtitles[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filename, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveSubtitleAttachments(ctx, plan.ID, files); err != nil {
		t.Fatal("repair existing attachment", err)
	}
	if _, err = s.readDownloadSubtitle(ctx, plan.Download.Subtitles[0]); err != nil {
		t.Fatal("repair did not restore payload", err)
	}
	job, err := s.Start(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	job.Items[0].File.ID, job.Items[0].File.ParentID = "101", "30"
	job.Items[0].File.Size = drive.dirs["30"][0].Size
	job.Items[0].Status, job.Items[0].TargetCID = "done", "30"
	job.Items[0].Name = drive.dirs["30"][0].Name
	job.Status = "completed"
	upload := &subtitleFakeDrive{organizeFakeDrive: drive, uploadErr: errors.New("lost receipt")}
	s.Organize.drive = upload
	if err = s.uploadSubtitles(ctx, &job, s.live[plan.ID].account.Cookie); err != nil || job.Status != "partial" || upload.uploads != 1 {
		t.Fatal("upload intent not retained", job, err)
	}
	if err = s.pruneSubtitleAttachments(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.readDownloadSubtitle(ctx, job.Download.Subtitles[0]); err != nil {
		t.Fatal("cleanup removed uncertain upload", err)
	}
	engine, err := NewOrganizeEngine(s.Organize.store, s.Organize.accounts, upload, s.Organize.metadata, s.Organize.templates)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewDownloadOrganize(engine, s.Resources, s.Downloads, s.Settings, s.Reader)
	restarted.SubtitleAttachments = s.SubtitleAttachments
	upload.uploadErr = nil
	if _, err = restarted.Resume(plan.ID); err != nil {
		t.Fatal("local attachment did not survive restart", err)
	}
	p := restarted.live[plan.ID]
	if done, err := restarted.advance(ctx, p); err != nil || !done || p.job.Status != "completed" || upload.uploads != 1 {
		t.Fatal("retry uploaded twice or did not finish", p.job, err)
	}
	if !strings.HasSuffix(p.job.Download.Subtitles[0].Name, ".Group.zh-CN.ass") {
		t.Fatal("version lost", p.job.Download.Subtitles[0])
	}
	if err = restarted.pruneSubtitleAttachments(time.Now()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.SubtitleAttachments)
	if err != nil || len(entries) != 0 {
		t.Fatal("completed local payload not cleaned", entries, err)
	}
}

func TestDownloadLocalSubtitlePairAndSelectionValidation(t *testing.T) {
	s, _, _, _, transport, in := gardenFixture(t)
	s.SubtitleAttachments = t.TempDir()
	idx := localDownloadSubtitle(transport.names[0], "Group/Show [01].chs.idx", []byte("index"))
	sub := localDownloadSubtitle(transport.names[0], "Group/Show [01].chs.sub", []byte("bitmap"))
	sub.LocalID = strings.Repeat("b", 32)
	ctx := context.Background()
	in.Subtitles = []DownloadSubtitleSelection{idx}
	if _, err := s.Preview(ctx, in); !errors.Is(err, ErrOrganizeInput) {
		t.Fatal("unpaired IDX accepted", err)
	}
	in.Subtitles = []DownloadSubtitleSelection{idx, sub}
	plan, err := s.Preview(ctx, in)
	if err != nil {
		t.Fatal("valid local pair rejected", err)
	}
	files := map[string][]byte{idx.LocalID: []byte("index"), sub.LocalID: []byte("bitmap")}
	for _, invalid := range []map[string][]byte{
		{idx.LocalID: []byte("index")},
		{idx.LocalID: []byte("index"), sub.LocalID: []byte("bitmap"), "extra": []byte("extra")},
	} {
		if err = s.SaveSubtitleAttachments(ctx, plan.ID, invalid); !errors.Is(err, ErrOrganizeInput) {
			t.Fatal("invalid attachment set accepted", err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = s.SaveSubtitleAttachments(cancelled, plan.ID, files); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled upload accepted", err)
	}
	if err = s.SaveSubtitleAttachments(ctx, plan.ID, files); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(plan.ID); err != nil {
		t.Fatal("local IDX/SUB task rejected", err)
	}
}

func TestDownloadLocalSubtitlesIncludedInShareSTRM(t *testing.T) {
	s, tasks, db, client, in := downloadShareFixture(t)
	s.SubtitleAttachments = t.TempDir()
	drive := &downloadSubtitleShareDrive{downloadShareFake: client}
	s.Organize.drive = drive
	data := []byte("WEBVTT\n\nlocal subtitle\n")
	in.Subtitles = []DownloadSubtitleSelection{localDownloadSubtitle(in.Files[0].ID, "Show [01].chs.vtt", data)}
	ctx := context.Background()
	plan, err := s.Preview(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveSubtitleAttachments(ctx, plan.ID, map[string][]byte{in.Subtitles[0].LocalID: data}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(plan.ID); err != nil {
		t.Fatal(err)
	}
	p := s.live[plan.ID]
	if done, err := s.advance(ctx, p); !done || err != nil || p.job.Status != "completed" || drive.uploads != 1 {
		t.Fatal("local subtitle blocked share handoff", p.job, err)
	}
	if err = s.pruneSubtitleAttachments(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = tasks.retryDue(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	record, err := db.GetShareTask(ctx, plan.ID)
	if err != nil || record.Status != "success" || client.sends != 1 {
		t.Fatal("share failed after local attachment cleanup", record, err)
	}
}

func TestDownloadLocalSubtitleBatchValidationAndCleanup(t *testing.T) {
	s, _, input, _ := downloadBatchFixture(t, 2)
	s.SubtitleAttachments = t.TempDir()
	data := []byte("subtitle")
	for i := range input.Resources {
		input.Resources[i].Subtitles = []DownloadSubtitleSelection{localDownloadSubtitle(input.Resources[i].Files[0].ID, "Show [01].ass", data)}
	}
	ctx := context.Background()
	plan, err := s.PreviewBatch(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{strings.Repeat("a", 32): data}
	if err = s.SaveSubtitleAttachments(ctx, plan.ID, files); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.StartBatch(plan.ID)
	if err != nil || len(accepted.Jobs) != 2 {
		t.Fatal(accepted, err)
	}
	if _, err = s.StartBatch(plan.ID); err != nil {
		t.Fatal("batch replay", err)
	}
	for _, job := range accepted.Jobs {
		if body, err := s.readDownloadSubtitle(ctx, job.Download.Subtitles[0]); err != nil || string(body) != string(data) {
			t.Fatal("batch attachment owner lost", err)
		}
	}
	sub := accepted.Jobs[0].Download.Subtitles[0]
	filename, _ := s.attachmentPath(sub)
	if err := os.WriteFile(filename, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.readDownloadSubtitle(ctx, sub); err == nil {
		t.Fatal("accepted corrupted local attachment")
	}
	sub.AttachmentID = "../escape"
	if _, err = s.attachmentPath(sub); !errors.Is(err, ErrOrganizeInput) {
		t.Fatal("accepted client path", err)
	}
	orphan := filepath.Join(s.SubtitleAttachments, strings.Repeat("f", 32))
	if err = os.WriteFile(orphan, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.pruneSubtitleAttachments(time.Now().Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("abandoned attachment retained", err)
	}
	if _, err = os.Stat(filename); err != nil {
		t.Fatal("pending task attachment removed", err)
	}
}
