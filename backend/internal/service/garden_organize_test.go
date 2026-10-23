package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

const gardenTestHash = "af53b74dfee5ab0562da0e21c1381ffe934ac07a"

type gardenDetailFake struct {
	paths      []string
	names      []string
	incomplete bool
}

func (f *gardenDetailFake) Fetch(_ context.Context, p string, _ int64) ([]byte, error) {
	f.paths = append(f.paths, p)
	d := garden.ResourceDetail{Status: "OK", Resource: garden.Resource{Provider: "dmhy", ProviderID: "688777", Magnet: "magnet:?xt=urn:btih:" + gardenTestHash}}
	d.Detail.Files = []garden.DetailFile{}
	d.Detail.HasMoreFiles = f.incomplete
	for _, name := range f.names {
		d.Detail.Files = append(d.Detail.Files, garden.DetailFile{Name: name, Size: "1.2GB"})
	}
	return json.Marshal(d)
}

type gardenTrackerFake struct {
	cid, cookie string
	resultID    string
	calls       int
	missing     bool
	downloading bool
	err         error
	beforeRead  func()
}

func (f *gardenTrackerFake) ReadAnimeTaskStatus(_ context.Context, cookie, hash string) (model.AnimeOfflineStatus, error) {
	f.calls++
	f.cookie = cookie
	if f.beforeRead != nil {
		f.beforeRead()
	}
	if f.err != nil {
		return model.AnimeOfflineStatus{}, f.err
	}
	if f.missing {
		return model.AnimeOfflineStatus{}, nil
	}
	status := int64(1)
	progress := 100.0
	out := model.AnimeOfflineStatus{SchemaVersion: 1, Source: model.AnimeOfflineExact, Present: true, InfoHash: hash, TargetCID: f.cid, ResultID: f.resultID, Status: &status, Progress: &progress, DisplayStatus: "completed", State: "completed"}
	if f.downloading {
		progress, out.DisplayStatus, out.State = 50, "downloading", "downloading"
	}
	return out, nil
}

func TestGardenInstantCompletionWithoutTargetCID(t *testing.T) {
	for _, result := range []string{"801", "901"} {
		t.Run(result, func(t *testing.T) {
			s, drive, _, tracker, _, in := gardenFixture(t)
			plan, err := s.Preview(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.Start(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := s.live[job.ID]
			tracker.resultID = result
			tracker.beforeRead = func() {
				drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
				drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200}}
			}
			// Submission, exact completion, identity proof and organization all
			// finish in one worker step, with no timer or extra status request.
			if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" {
				t.Fatal("instant completion failed", done, err, p.job)
			}
			if tracker.calls != 1 || drive.renames != 1 || drive.moves != 1 {
				t.Fatal("unnecessary polls or writes", tracker.calls, drive.renames, drive.moves)
			}
			// Two necessary source reads (validate, reconcile rename), no
			// whole-plan reread. The earlier binding scan adds exactly one.
			if drive.listCalls["801"] != 3 {
				t.Fatal("redundant source listing", drive.listCalls)
			}
			reads := 0
			for _, count := range drive.listCalls {
				reads += count
			}
			// Preview used one read; execution statistics include staging and
			// binding, and survive entry into the shared batch executor.
			if p.job.Stats.DirectoryCreates != drive.creates || p.job.Stats.DirectoryRequests != reads-1 {
				t.Fatal("incomplete chain statistics", p.job.Stats, reads, drive.creates)
			}
		})
	}
}

func TestGardenCompletionNeedsDirectoryEvidence(t *testing.T) {
	for _, mode := range []string{"foreign-result", "no-evidence", "wrong-target"} {
		t.Run(mode, func(t *testing.T) {
			s, drive, _, tracker, _, in := gardenFixture(t)
			plan, err := s.Preview(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.Start(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := s.live[job.ID]
			tracker.beforeRead = func() {
				drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
				drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200}}
			}
			if mode == "foreign-result" {
				tracker.resultID = "999"
			}
			if mode == "wrong-target" {
				tracker.cid, tracker.resultID = "999", "801"
			}
			for i := 0; i < 3; i++ {
				done, err := s.advance(context.Background(), p)
				if err != nil {
					t.Fatal(err)
				}
				if done {
					break
				}
			}
			if drive.renames != 0 || drive.moves != 0 || drive.creates != 1 {
				t.Fatal("mutated without directory proof", drive)
			}
			if mode == "no-evidence" {
				if p.job.Status != "downloading" || !p.next.After(time.Now()) {
					t.Fatal("must keep waiting for directory evidence", p.job.Status)
				}
			} else if p.job.Status != "blocked" {
				t.Fatal("unsafe result was accepted", p.job.Status)
			}
			if mode == "foreign-result" && tracker.calls != 1 {
				t.Fatal("repolled a completed task", tracker.calls)
			}
		})
	}
}

func TestGardenSingleFileTorrentWrapper(t *testing.T) {
	for _, resultID := range []string{"801", "901"} {
		t.Run(resultID, func(t *testing.T) {
			s, drive, _, tracker, transport, in := gardenFixture(t)
			const name = "[Group] Show [01].mkv"
			transport.names = []string{name}
			in.Files[0].ID = name
			plan, err := s.Preview(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.Start(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := s.live[job.ID]
			tracker.resultID = resultID
			tracker.beforeRead = func() {
				drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: name, Directory: true}}
				drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: name, Size: 1200}}
			}
			if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" {
				t.Fatal("verified single-file wrapper did not organize", done, err, p.job)
			}
			if tracker.calls != 1 || drive.renames != 1 || drive.moves != 1 || !strings.HasSuffix(p.job.Items[0].File.SourcePath, "/"+name+"/"+name) {
				t.Fatal("wrapper added requests or lost actual source path", p.job)
			}
		})
	}
}

func TestGardenSingleFileWrapperNeverGuessesOtherPaths(t *testing.T) {
	for _, mode := range []string{"no-result", "foreign-result", "other-wrapper", "nested-wrapper", "other-file", "multiple-files"} {
		t.Run(mode, func(t *testing.T) {
			const name = "show.mkv"
			job := OrganizeJob{Items: []OrganizeItem{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: name}, SourcePath: "/" + name}}}}
			files := []OrganizeFile{{ShareFile: p115.ShareFile{ID: "901", Name: name}, ParentID: "801", SourcePath: name + "/" + name, MediaType: "video"}}
			resultID := "801"
			switch mode {
			case "no-result":
				resultID = ""
			case "foreign-result":
				resultID = "999"
			case "other-wrapper":
				files[0].SourcePath = "other/" + name
			case "nested-wrapper":
				files[0].SourcePath = "nested/" + name + "/" + name
			case "other-file":
				files[0].Name = "other.mkv"
			case "multiple-files":
				files = append(files, files[0])
			}
			if err := bindGardenFiles(&job, files, resultID); err == nil || job.Items[0].File.ID != name {
				t.Fatal("unverified wrapper guessed a file", mode, err, job)
			}
		})
	}
}

func TestGardenCompletedTaskDoesNotRepollWhileOrganizerBusy(t *testing.T) {
	s, drive, _, tracker, _, in := gardenFixture(t)
	plan, err := s.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Start(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := s.live[job.ID]
	tracker.resultID = "801"
	tracker.beforeRead = func() {
		drive.dirs[p.job.Download.DirectoryID] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
		drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200}}
	}
	release, err := s.Organize.ReserveDirectoryWork()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if done, err := s.advance(context.Background(), p); err != nil || done || !p.next.After(time.Now()) {
			t.Fatal(done, err)
		}
	}
	release()
	if done, err := s.advance(context.Background(), p); err != nil || !done || p.job.Status != "completed" || tracker.calls != 1 {
		t.Fatal("completion lost while busy", done, err, tracker.calls, p.job.Status)
	}
}

func gardenFixture(t *testing.T) (*GardenOrganize, *organizeFakeDrive, *cloudAdderStub, *gardenTrackerFake, *gardenDetailFake, GardenOrganizeInput) {
	o, drive, account, _ := newOrganizeTest(t)
	transport := &gardenDetailFake{names: []string{"show/[Group] Show [01].mkv", "show/[Group] Show [02].mkv", "show/[Group] Show [SP01].mkv", "show/subtitles.zip", "show/disc.iso"}}
	g := NewGardenService(transport)
	g.spacing = 0
	adder := &cloudAdderStub{}
	settings := &CloudDownloadSettings{value: cloudDownloadDocument{CloudDownloadConfig: CloudDownloadConfig{AccountID: account, TargetCID: "10", TargetPath: "/来源"}}}
	tracker := &gardenTrackerFake{}
	s := NewGardenOrganize(o, g, &CloudDownloadService{Accounts: o.accounts, Client: adder, Coordinator: &CloudDownloadCoordinator{}}, settings, tracker)
	in := GardenOrganizeInput{GardenResourceInput: GardenResourceInput{Provider: "dmhy", ProviderID: "688777", Magnet: "magnet:?xt=urn:btih:" + gardenTestHash}, Kind: "tv", TMDBID: 42, AccountID: account, TargetCID: "20", TargetPath: "/目标", Files: []OrganizeSelection{{ID: transport.names[0], Season: 1, Episode: 1, Suffix: "Group"}}}
	return s, drive, adder, tracker, transport, in
}

func TestGardenFilesStandardSeasonEpisodes(t *testing.T) {
	s, _, _, _, transport, in := gardenFixture(t)
	transport.names = make([]string, 150)
	for i := range transport.names {
		transport.names[i] = fmt.Sprintf("Kiniro.No.Gasshuberu.S01.1080p.AMZN.WEB-DL.DDP2.0.H.264-Yatogam1/Kiniro.No.Gasshuberu.S01E%02d.1080p.AMZN.WEB-DL.DDP2.0.H.264-Yatogam1.mkv", i+1)
	}
	files, err := s.Garden.Files(context.Background(), in.GardenResourceInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != len(transport.names) || len(files.EndEpisodes) != 0 {
		t.Fatal("single-episode files changed", len(files.Files), files.EndEpisodes)
	}
	for i, file := range files.Files {
		if file.ID != transport.names[i] || file.Season != 1 || file.Episode != i+1 {
			t.Fatalf("file %d lost its standard episode: %+v", i+1, file)
		}
	}
}

func TestGardenFilesCacheAliasesAndConservativeSuggestions(t *testing.T) {
	s, _, _, _, transport, in := gardenFixture(t)
	transport.names = []string{
		"show/百变之星.2003.E027-E28.1080p.BDrip.mkv",
		"show/百变之星.2006.E53.OVA02.V2.1080p.mkv",
		"show/[Group] Show [SP01].mkv",
		"show/[Group] Show [10].mkv", "show/[Group] Show [2].mkv",
		"show/カレイドスター 新たなる翼 第01話.mp4", "show/NCOP.E27-E28.mkv",
		"show/no-number.mkv", "show/disc.iso", "show/subtitles.zip",
	}
	files, err := s.Garden.Files(context.Background(), in.GardenResourceInput)
	if err != nil || len(files.Files) != 8 {
		t.Fatal(files, err)
	}
	byName := make(map[string]OrganizeFile)
	for _, f := range files.Files {
		byName[f.Name] = f
	}
	if byName["百变之星.2003.E027-E28.1080p.BDrip.mkv"].Episode != 27 || files.EndEpisodes[transport.names[0]] != 28 {
		t.Fatal("range lost", files)
	}
	for _, name := range []string{"百变之星.2006.E53.OVA02.V2.1080p.mkv", "[Group] Show [SP01].mkv", "NCOP.E27-E28.mkv", "no-number.mkv"} {
		if byName[name].Episode != 0 {
			t.Fatal("special guessed", name)
		}
	}
	if files.Files[0].Name != "[Group] Show [2].mkv" || byName["カレイドスター 新たなる翼 第01話.mp4"].Episode != 1 {
		t.Fatal("natural order/publisher numbers", files)
	}
	alias := in.GardenResourceInput
	alias.Provider = "mikan"
	alias.ProviderID = "different"
	alias.Magnet = "magnet:?xt=urn:btih:V5J3OTP64WVQKYW2BYQ4COA772JUVQD2"
	if _, err = s.Garden.Files(context.Background(), alias); err != nil || len(transport.paths) != 1 {
		t.Fatal("BTIH cache alias missed", err, transport.paths)
	}
	wrong := in.GardenResourceInput
	wrong.Magnet = "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"
	if _, err = s.Garden.Files(context.Background(), wrong); err == nil {
		t.Fatal("resource identity mismatch accepted")
	}
}

func TestGardenDownloadOrganizePinsAccountAndPreservesUnselected(t *testing.T) {
	s, drive, adder, tracker, _, in := gardenFixture(t)
	ctx := context.Background()
	preview, err := s.Preview(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if adder.url != "" || drive.creates != 0 {
		t.Fatal("preparation submitted or created a folder")
	}
	if _, err = s.Organize.Start(in.AccountID, preview.ID); err == nil {
		t.Fatal("ordinary organizer accepted a website plan")
	}
	job, err := s.Start(preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(preview.ID); err != nil || len(s.live) != 1 || len(s.Organize.History()) != 1 {
		t.Fatal("start not idempotent", err)
	}
	p := s.live[job.ID]
	oldCookie := p.account.Cookie
	account, _ := s.Organize.accounts.Get(in.AccountID)
	replacement := "UID=456_other; SEID=replacement"
	if err = s.Organize.accounts.Update(account.ID, account.Revision, account.Name, &replacement); err != nil {
		t.Fatal(err)
	}
	if done, err := s.advance(ctx, p); err != nil || done || adder.cookie != oldCookie || adder.cid != p.job.Download.DirectoryID {
		t.Fatal("submit did not pin", done, err, adder)
	}
	if release, err := s.Organize.ReserveDirectoryWork(); err != nil {
		t.Fatal("download wait held organize lock", err)
	} else {
		release()
	}
	dir := p.job.Download.DirectoryID
	// Cloud results include the original torrent paths, plus untouched leftovers.
	drive.dirs[dir] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
	drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200}, {ID: "902", Name: "[Group] Show [02].mkv", Size: 1300}, {ID: "903", Name: "subtitles.zip", Size: 10}}
	tracker.cid = dir
	if done, err := s.advance(ctx, p); err != nil || !done || p.job.Status != "completed" {
		t.Fatal("organize failed", done, err, p.job)
	}
	if tracker.cookie != oldCookie || len(drive.moveBatches) != 1 || len(drive.moveBatches[0]) != 1 || drive.moveBatches[0][0] != "901" {
		t.Fatal("wrong selection/account", drive.moveBatches, tracker.cookie)
	}
	if len(drive.dirs["801"]) != 2 || drive.dirs["801"][0].ID != "902" {
		t.Fatal("leftovers lost", drive.dirs["801"])
	}
	if strings.Contains(s.Organize.store.Value(), "secret-cookie") || strings.Contains(s.Organize.store.Value(), "replacement") {
		t.Fatal("credential persisted")
	}
	for _, cookie := range drive.cookies {
		if cookie != oldCookie {
			t.Fatal("drive rebound account")
		}
	}
	if _, err = s.Start(preview.ID); err != nil || drive.creates != 3 {
		t.Fatal("lost-response retry replayed work", drive.creates, err)
	}
}

func TestGardenBindingRequiresExactPathsAndNeverPartiallyChangesPlan(t *testing.T) {
	job := OrganizeJob{Items: []OrganizeItem{{File: OrganizeFile{ShareFile: p115.ShareFile{ID: "show/a.mkv"}, SourcePath: "/show/a.mkv"}}, {File: OrganizeFile{ShareFile: p115.ShareFile{ID: "show/b.mkv"}, SourcePath: "/show/b.mkv"}}}}
	files := []OrganizeFile{{ShareFile: p115.ShareFile{ID: "1", Name: "a.mkv"}, SourcePath: "show/a.mkv", MediaType: "video"}, {ShareFile: p115.ShareFile{ID: "2", Name: "b.mkv"}, SourcePath: "other/b.mkv", MediaType: "video"}}
	if err := bindGardenFiles(&job, files, ""); err == nil || job.Items[0].File.ID != "show/a.mkv" {
		t.Fatal("guessed basename or partially rebound", err, job)
	}
	files[1].SourcePath = "show/b.mkv"
	if err := bindGardenFiles(&job, files, ""); err != nil || job.Items[0].File.ID != "1" {
		t.Fatal(err, job)
	}
}

func TestGardenFailuresDoNotResubmitOrRename(t *testing.T) {
	for _, mode := range []string{"missing-file", "wrong-target", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			s, drive, adder, tracker, _, in := gardenFixture(t)
			plan, err := s.Preview(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.Start(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := s.live[job.ID]
			if _, err = s.advance(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			tracker.cid = p.job.Download.DirectoryID
			if mode == "wrong-target" {
				tracker.cid = "999"
			}
			if mode == "read-error" {
				tracker.err = errors.New("offline")
			}
			for i := 0; i < 3; i++ {
				done, e := s.advance(context.Background(), p)
				if e != nil {
					t.Fatal(e)
				}
				if done {
					break
				}
			}
			if drive.renames != 0 || drive.moves != 0 || drive.creates != 1 || adder.cid != p.job.Download.DirectoryID {
				t.Fatal("failure mutated/replayed", drive)
			}
			if mode != "read-error" && p.job.Status != "blocked" {
				t.Fatal(p.job.Status)
			}
			if mode == "read-error" && (p.job.Status != "downloading" || !p.next.After(time.Now())) {
				t.Fatal("read failure not backed off")
			}
		})
	}
}

func TestGardenPreviewConflictsUnsupportedAndRestart(t *testing.T) {
	s, _, _, _, transport, in := gardenFixture(t)
	in.Files = append(in.Files, OrganizeSelection{ID: transport.names[1], Season: 1, Episode: 1, Suffix: "Group"})
	preview, err := s.Preview(context.Background(), in)
	if err != nil || preview.Items[0].Conflict == "" || preview.Items[1].Conflict == "" {
		t.Fatal("duplicate target missed", preview, err)
	}
	if _, err = s.Start(preview.ID); err == nil {
		t.Fatal("conflicting task started")
	}
	in.Files = in.Files[:1]
	preview, err = s.Preview(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(preview.ID); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewManualOrganize(s.Organize.store, s.Organize.accounts, s.Organize.drive, s.Organize.metadata)
	if err != nil || restarted.History()[0].Status != "interrupted" {
		t.Fatal("restart silently rebound work", err)
	}
	transport.incomplete = true
	s.Garden = NewGardenService(transport)
	s.Garden.spacing = 0
	if files, err := s.Garden.Files(context.Background(), in.GardenResourceInput); err != nil || len(files.Files) != 0 || files.Reason == "" {
		t.Fatal("partial list accepted", files, err)
	}
}

func TestGardenCleanupOnlyEmptyDirectoriesAndNotifiesAfterRelease(t *testing.T) {
	for _, leftover := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "leftover"}[leftover], func(t *testing.T) {
			s, base, _, tracker, _, in := gardenFixture(t)
			drive := &cleanupFakeDrive{organizeFakeDrive: base}
			s.Organize.drive = drive
			plan, err := s.Preview(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			job, err := s.Start(plan.ID)
			if err != nil {
				t.Fatal(err)
			}
			p := s.live[job.ID]
			if _, err = s.advance(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			dir := p.job.Download.DirectoryID
			tracker.cid = dir
			drive.dirs[dir] = []p115.ShareFile{{ID: "801", Name: "show", Directory: true}}
			drive.dirs["801"] = []p115.ShareFile{{ID: "901", Name: "[Group] Show [01].mkv", Size: 1200}}
			if leftover {
				drive.dirs["801"] = append(drive.dirs["801"], p115.ShareFile{ID: "902", Name: "subtitle.zip", Size: 10})
			}
			notified := 0
			s.Organize.OnCompleted = func(ctx context.Context, c OrganizeConfig) {
				notified++
				release, e := s.Organize.ReserveDirectoryWork()
				if e != nil {
					t.Error("notified while locked", e)
				} else {
					release()
				}
			}
			if done, err := s.advance(context.Background(), p); err != nil || !done || notified != 1 {
				t.Fatal(done, err, notified)
			}
			if leftover && len(drive.deleted) != 0 {
				t.Fatal("deleted leftover folder", drive.deleted)
			}
			if !leftover && (len(drive.deleted) != 2 || drive.deleted[0] != "801" || drive.deleted[1] != dir) {
				t.Fatal("empty cleanup order", drive.deleted)
			}
		})
	}
}
