package service

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/subshare"
)

type downloadSubtitleShareDrive struct {
	*downloadShareFake
	uploads int
}

func (d *downloadSubtitleShareDrive) UploadSubtitle(_ context.Context, cid, name string, data []byte, _ string) error {
	d.uploads++
	d.dirs[cid] = append(d.dirs[cid], p115.ShareFile{ID: "98765", Name: name, Size: int64(len(data))})
	return nil
}

func TestDownloadExternalSubtitlesShareManifestAndIdempotency(t *testing.T) {
	s, tasks, db, client, in := downloadShareFixture(t)
	tasks.Generator = generateFunc(func(_ context.Context, in GenerateInput) (*GenerationResult, error) {
		if !strings.Contains(in.MetadataExtensions, "vtt") {
			t.Fatal("explicit subtitle extension omitted", in.MetadataExtensions)
		}
		return &GenerationResult{Written: 1, Files: make([]GeneratedSTRM, 1)}, nil
	})
	drive := &downloadSubtitleShareDrive{downloadShareFake: client}
	s.Organize.drive = drive
	library := &subtitleLibraryFake{file: subshare.File{Path: "animation/Show/01.chs.vtt", Name: "01.chs.vtt", SHA: strings.Repeat("a", 40), Size: 6}}
	s.Subtitles = library
	in.Subtitles = []DownloadSubtitleSelection{{VersionID: "v", Path: library.file.Path, SHA: library.file.SHA, VideoID: in.Files[0].ID, Language: "zh-CN"}}
	job := completeDownloadShare(t, s, in)
	if drive.uploads != 1 || !job.Download.Share.HandedOff || job.Download.Subtitles[0].FileID != "98765" {
		t.Fatal("external subtitles not handed off", job)
	}
	record, err := db.GetShareTask(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var req shareTaskRequest
	if err = json.Unmarshal(record.Request, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Download.Files) != 2 || !strings.Contains(strings.Join(req.Download.Manifest, "\n"), job.Download.Subtitles[0].Name) {
		t.Fatal("share manifest lost subtitle", req.Download)
	}
	if err = tasks.retryDue(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	record, err = db.GetShareTask(context.Background(), job.ID)
	if err != nil || record.Status != "success" || client.sends != 1 {
		t.Fatal("strict share verification rejected external subtitle", record, err)
	}
	if err = tasks.EnqueueDownloadShare(context.Background(), job, tasks.AccountCookie(in.AccountID)); err != nil {
		t.Fatal(err)
	}
	if err = tasks.retryDue(context.Background(), time.Now()); err != nil || client.sends != 1 || drive.uploads != 1 {
		t.Fatal("duplicate subtitle/share", err)
	}
}

type subtitleLibraryFake struct {
	file  subshare.File
	err   error
	reads int
}

func (f *subtitleLibraryFake) Files(context.Context, string) ([]subshare.File, error) {
	return []subshare.File{f.file}, nil
}
func (f *subtitleLibraryFake) Read(context.Context, subshare.File) ([]byte, error) {
	f.reads++
	return []byte("字幕"), f.err
}

func TestDownloadExternalSubtitlesPlanUploadAndLostReceipt(t *testing.T) {
	s, drive, _, _, transport, in := gardenFixture(t)
	library := &subtitleLibraryFake{file: subshare.File{Path: "subs_list/animation/2000/Show/BD/Group/Show [01].chs.ass", Name: "Show [01].chs.ass", SHA: strings.Repeat("a", 40), Revision: strings.Repeat("b", 40), Size: 6}}
	s.Subtitles = library
	in.Subtitles = []DownloadSubtitleSelection{{VersionID: "v", Path: library.file.Path, SHA: library.file.SHA, VideoID: transport.names[0], Language: "zh-CN"}}
	plan, err := s.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Download.Subtitles) != 1 || library.reads != 0 {
		t.Fatal("preview downloaded subtitle or lost selection", plan)
	}
	bad := in
	bad.Subtitles = append([]DownloadSubtitleSelection(nil), in.Subtitles...)
	bad.Subtitles[0].SHA = "changed"
	if _, err = s.Preview(context.Background(), bad); err == nil {
		t.Fatal("changed subtitle accepted")
	}
	bad = in
	bad.Subtitles = append(in.Subtitles, in.Subtitles...)
	if _, err = s.Preview(context.Background(), bad); err == nil {
		t.Fatal("duplicate subtitles accepted")
	}
	job, err := s.Start(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Use the shared engine fixture to emulate the final verified video result.
	job.Items[0].File.ID = "101"
	job.Items[0].File.ParentID = "30"
	job.Items[0].File.Size = drive.dirs["30"][0].Size
	job.Items[0].Status = "done"
	job.Items[0].TargetCID = "30"
	job.Items[0].Name = drive.dirs["30"][0].Name
	job.Status = "completed"
	upload := &subtitleFakeDrive{organizeFakeDrive: drive, uploadErr: errors.New("lost receipt")}
	s.Organize.drive = upload
	if err = s.uploadSubtitles(context.Background(), &job, s.live[plan.ID].account.Cookie); err != nil {
		t.Fatal(err)
	}
	if job.Status != "partial" || job.Download.Subtitles[0].Status != "uncertain" || upload.uploads != 1 {
		t.Fatal("upload failure lost intent", job)
	}
	engine, err := NewOrganizeEngine(s.Organize.store, s.Organize.accounts, upload, s.Organize.metadata, s.Organize.templates)
	if err != nil {
		t.Fatal(err)
	}
	s = NewDownloadOrganize(engine, s.Resources, s.Downloads, s.Settings, s.Reader)
	s.Subtitles = library
	if _, err = s.Resume(plan.ID); err != nil {
		t.Fatal("subtitle-only retry not resumable", err)
	}
	upload.uploadErr = nil
	pending := s.live[plan.ID]
	if done, err := s.advance(context.Background(), pending); err != nil || !done || pending.job.Status != "completed" {
		t.Fatal(done, err, pending.job)
	}
	job = pending.job
	if upload.uploads != 1 || job.Download.Subtitles[0].Status != "done" {
		t.Fatal("lost receipt duplicated upload", job.Download.Subtitles, upload.uploads)
	}
	cloned := cloneOrganizeJob(job)
	cloned.Download.Subtitles[0].Status = "changed"
	if job.Download.Subtitles[0].Status == "changed" {
		t.Fatal("clone aliases subtitles")
	}
}

func TestDownloadExternalSubtitleTitleBindingAndExistingPair(t *testing.T) {
	for _, mode := range []string{"title-only", "incomplete-pair"} {
		t.Run(mode, func(t *testing.T) {
			s, drive, _, tracker, _, in := gardenFixture(t)
			library := &subtitleLibraryFake{file: subshare.File{Path: "animation/Show.chs.ass", Name: "Show.chs.ass", SHA: strings.Repeat("a", 40), Size: 6}}
			s.Subtitles = library
			if mode == "title-only" {
				in.DownloadResourceInput = titleResource("garden", "[Group] Show [01][1080P]")
				in.Files[0].ID = "title"
			}
			in.Subtitles = []DownloadSubtitleSelection{{VersionID: "v", Path: library.file.Path, SHA: library.file.SHA, VideoID: in.Files[0].ID, Language: "zh-CN"}}
			plan, err := s.Preview(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "title-only" && !strings.HasPrefix(plan.Download.Subtitles[0].Name, plan.Items[0].Stem+".") {
				t.Fatal("title stem was truncated", plan)
			}
			if _, err = s.Start(plan.ID); err != nil {
				t.Fatal(err)
			}
			p := s.live[plan.ID]
			upload := &subtitleFakeDrive{organizeFakeDrive: drive}
			s.Organize.drive = upload
			tracker.resultID = "801"
			tracker.beforeRead = func() {
				drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
				drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "[Group] Show [01].mp4", Size: 1200}}
			}
			if mode == "incomplete-pair" {
				p.job.Items[0].File.ID, p.job.Items[0].Status, p.job.Items[0].TargetCID = "101", "done", "30"
				p.job.Items[0].Name = drive.dirs["30"][0].Name
				p.job.Items[0].File.Size = drive.dirs["30"][0].Size
				p.job.Status = "completed"
				idx := p.job.Download.Subtitles[0]
				idx.File.Name, idx.Name = "Show.idx", "Show.zh-CN.idx"
				sub := idx
				sub.File.Name, sub.Name = "Show.sub", "Show.zh-CN.sub"
				p.job.Items[0].Name = "Show.mkv"
				drive.dirs["30"][0].Name = "Show.mkv"
				p.job.Download.Subtitles = []DownloadSubtitle{idx, sub}
				drive.dirs["30"] = append(drive.dirs["30"], p115.ShareFile{ID: "999", Name: idx.Name, Size: idx.File.Size})
				if err = s.uploadSubtitles(context.Background(), &p.job, p.account.Cookie); err != nil || p.job.Status != "partial" || upload.uploads != 0 {
					t.Fatal("mixed existing/new pair", err, p.job)
				}
				return
			}
			if done, err := s.advance(context.Background(), p); !done || err != nil || p.job.Status != "completed" || upload.uploads != 1 {
				t.Fatal(done, err, p.job)
			}
			video, sub := p.job.Items[0], p.job.Download.Subtitles[0]
			if !strings.HasSuffix(video.Name, ".mp4") || sub.Name != strings.TrimSuffix(video.Name, ".mp4")+".zh-CN.ass" || sub.FileID == "" || sub.TargetPath != path.Join(path.Dir(video.TargetPath), sub.Name) {
				t.Fatal(video, sub)
			}
		})
	}
}

func TestShareCollectionRetainsExternalSubtitleOwners(t *testing.T) {
	f := newDownloadCollectionFixture(t, 2)
	for i := range f.jobs {
		p := f.s.live[f.jobs[i].ID]
		p.job.Download.Subtitles = []DownloadSubtitle{{File: subshare.File{Name: "Show.ass", Path: "same"}, VideoIndex: 0, Language: "zh-TW", Status: "pending"}}
		f.ready(t, i)
	}
	if err := f.s.sealShareCollections(time.Now().Add(3 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	p := f.leader(t)
	if len(p.job.Download.Subtitles) != 2 {
		t.Fatal("collection lost subtitles")
	}
	owners := make(map[int]bool)
	for _, sub := range p.job.Download.Subtitles {
		if sub.VideoIndex >= len(p.job.Items) || owners[sub.VideoIndex] {
			t.Fatal("collection lost video identity", p.job.Download.Subtitles)
		}
		owners[sub.VideoIndex] = true
	}
	for i := range p.job.Download.Subtitles {
		p.job.Download.Subtitles[i].Status = "done"
	}
	if err := f.s.Organize.checkpoint(&p.job); err != nil {
		t.Fatal(err)
	}
	for _, member := range f.s.History() {
		if member.Download.Subtitles[0].Status != "done" {
			t.Fatal("member subtitle state not propagated", member.ID)
		}
	}
}
