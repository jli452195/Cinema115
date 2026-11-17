package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

const ed2kSTRMTestHash = "0123456789abcdef0123456789abcdef"
const ed2kSTRMTestSHA1 = "0123456789ABCDEF0123456789ABCDEF01234567"
const ed2kSTRMTestSize int64 = 1234

type ed2kSTRMClientStub struct {
	t        *testing.T
	calls    []string
	expected []p115.DrivePlaybackFile
	dirs     map[string]p115.DriveDirectory
	find     func(string, string) (*p115.DrivePlaybackFile, error)
	info     func(string, string) (*p115.DownloadInfo, error)
	listing  func(string, string) (p115.DriveDirectory, []p115.ShareFile, error)
	create   func(string, string, string) (string, error)
}

func (c *ed2kSTRMClientStub) unexpected(method string) {
	c.t.Helper()
	c.t.Fatalf("unexpected 115 request: %s", method)
}
func (c *ed2kSTRMClientStub) CreateDirectory(_ context.Context, parent, name, cookie string) (string, error) {
	c.calls = append(c.calls, "create")
	if c.create != nil {
		return c.create(parent, name, cookie)
	}
	c.unexpected("CreateDirectory")
	return "", nil
}
func (c *ed2kSTRMClientStub) ReadDirectory(_ context.Context, cid, _ string) (p115.DriveDirectory, error) {
	c.calls = append(c.calls, "directory")
	if v, ok := c.dirs[cid]; ok {
		return v, nil
	}
	c.unexpected("ReadDirectory")
	return p115.DriveDirectory{}, nil
}
func (c *ed2kSTRMClientStub) ReadDirectoryFiles(_ context.Context, cid, cookie string) (p115.DriveDirectory, []p115.ShareFile, error) {
	c.calls = append(c.calls, "listing")
	if c.listing != nil {
		return c.listing(cid, cookie)
	}
	c.unexpected("ReadDirectoryFiles")
	return p115.DriveDirectory{}, nil, nil
}
func (c *ed2kSTRMClientStub) DirectoryMissing(context.Context, string, string) (bool, error) {
	c.calls = append(c.calls, "directory-missing")
	c.unexpected("DirectoryMissing")
	return false, nil
}
func (c *ed2kSTRMClientStub) FindPlaybackFileBySHA1Quick(_ context.Context, sha1, cookie string) (*p115.DrivePlaybackFile, error) {
	c.calls = append(c.calls, "find")
	if c.find != nil {
		return c.find(sha1, cookie)
	}
	c.unexpected("FindPlaybackFileBySHA1Quick")
	return nil, nil
}
func (c *ed2kSTRMClientStub) PickCodeInstantInfo(_ context.Context, pickcode, cookie string) (*p115.DownloadInfo, error) {
	c.calls = append(c.calls, "info")
	if c.info != nil {
		return c.info(pickcode, cookie)
	}
	c.unexpected("PickCodeInstantInfo")
	return nil, nil
}
func (c *ed2kSTRMClientStub) PickCodeInstantInfoForFile(ctx context.Context, expected p115.DrivePlaybackFile, cookie string) (*p115.DownloadInfo, error) {
	c.expected = append(c.expected, expected)
	return c.PickCodeInstantInfo(ctx, expected.PickCode, cookie)
}
func (c *ed2kSTRMClientStub) VerifiedPlaybackURL(context.Context, p115.DrivePlaybackFile, string, string) (string, error) {
	c.calls = append(c.calls, "url")
	c.unexpected("VerifiedPlaybackURL")
	return "", nil
}
func (c *ed2kSTRMClientStub) InstantUpload(context.Context, *p115.DownloadInfo, string, string, func(p115.InstantProgress)) (*p115.InstantUploadResult, error) {
	c.calls = append(c.calls, "upload")
	c.unexpected("InstantUpload")
	return nil, nil
}
func (c *ed2kSTRMClientStub) CompletePlaybackUpload(context.Context, *p115.DownloadInfo, string, *p115.InstantUploadResult, string, string) (p115.DrivePlaybackFile, string, error) {
	c.calls = append(c.calls, "complete-upload")
	c.unexpected("CompletePlaybackUpload")
	return p115.DrivePlaybackFile{}, "", nil
}

type ed2kSTRMTaskStub struct {
	t     *testing.T
	calls int
	read  func(string, string) (p115.ED2KTaskObservation, error)
}

func (c *ed2kSTRMTaskStub) InspectED2KTask(_ context.Context, cookie, hash string) (p115.ED2KTaskObservation, error) {
	c.calls++
	if c.read == nil {
		c.t.Fatal("unexpected cloud task inspection")
	}
	return c.read(cookie, hash)
}

type ed2kSTRMFixture struct {
	service  *ED2KSTRMService
	store    *storage.Store
	filename string
	accounts *P115Accounts
	client   *ed2kSTRMClientStub
	cloud    *ed2kSTRMTaskStub
}

func newED2KSTRMFixture(t *testing.T) *ed2kSTRMFixture {
	t.Helper()
	ctx := context.Background()
	f := &ed2kSTRMFixture{filename: filepath.Join(t.TempDir(), "ed2k.db"), client: &ed2kSTRMClientStub{t: t}, cloud: &ed2kSTRMTaskStub{t: t}}
	var err error
	f.store, err = storage.Open(ctx, f.filename)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f.store != nil {
			_ = f.store.Close()
		}
	})
	f.accounts, _ = cloudWorkflowAccounts(t)
	f.service, err = NewED2KSTRMService(ctx, f.store, f.accounts, f.client, f.cloud, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SaveSettings(ctx, model.ED2KSTRMSettings{BaseURL: "https://cinema.example", OutputDir: "ed2k"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *ed2kSTRMFixture) imported(t *testing.T) model.ED2KSTRMResource {
	t.Helper()
	rows, err := f.service.Import(context.Background(), []string{ed2kSTRMTestLink("Film.mkv", ed2kSTRMTestHash, ed2kSTRMTestSize)})
	if err != nil || len(rows) != 1 {
		t.Fatalf("import=%+v err=%v", rows, err)
	}
	return rows[0]
}

func ed2kSTRMTestLink(name, hash string, size int64) string {
	return "ed2k://|file|" + url.PathEscape(name) + "|" + strconv.FormatInt(size, 10) + "|" + hash + "|/"
}

func TestED2KSTRMImportDeduplicatesContentAcrossNames(t *testing.T) {
	f := newED2KSTRMFixture(t)
	original := f.imported(t)
	rows, err := f.service.Import(context.Background(), []string{
		ed2kSTRMTestLink("另一个名字.mkv", strings.ToUpper(ed2kSTRMTestHash), ed2kSTRMTestSize),
		ed2kSTRMTestLink("重复名字.mkv", ed2kSTRMTestHash, ed2kSTRMTestSize),
	})
	if err != nil || len(rows) != 1 || rows[0].Hash != original.Hash || rows[0].Name != original.Name {
		t.Fatalf("duplicate import=%+v err=%v", rows, err)
	}
	if !slices.Contains(rows[0].Aliases, "另一个名字.mkv") || !slices.Contains(rows[0].Aliases, "重复名字.mkv") {
		t.Fatalf("aliases from the same batch not recorded: %+v", rows[0])
	}
	all := f.service.List()
	if len(all) != 1 || len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatalf("dedup/list triggered work: rows=%d calls=%v cloud=%d", len(all), f.client.calls, f.cloud.calls)
	}
	all[0].Aliases[0] = "客户端修改.mkv"
	if slices.Contains(f.service.List()[0].Aliases, "客户端修改.mkv") {
		t.Fatal("List exposed mutable internal aliases")
	}
	stored, err := f.store.ListED2KSTRMResources(context.Background())
	if err != nil || len(stored) != 1 {
		t.Fatalf("durable dedup=%+v err=%v", stored, err)
	}
	if !slices.Contains(stored[0].Aliases, "另一个名字.mkv") || !slices.Contains(stored[0].Aliases, "重复名字.mkv") {
		t.Fatalf("same-batch aliases were not persisted: %+v", stored[0])
	}
	if _, err := f.service.Import(context.Background(), []string{ed2kSTRMTestLink("Different-size.mkv", ed2kSTRMTestHash, ed2kSTRMTestSize+1)}); err != nil {
		t.Fatal(err)
	}
	if len(f.service.List()) != 2 {
		t.Fatal("same hash with a different size was incorrectly reused")
	}
}

func TestED2KSTRMSettingsAndListingDoNotContact115(t *testing.T) {
	f := newED2KSTRMFixture(t)
	v := f.imported(t)
	settings := model.ED2KSTRMSettings{BaseURL: "https://new.example", OutputDir: "new-ed2k"}
	if _, err := f.service.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		rows := f.service.List()
		if len(rows) != 1 || rows[0].Settings != v.Settings {
			t.Fatalf("queued task settings changed: %+v", rows)
		}
	}
	if len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatalf("local operation contacted 115: %v cloud=%d", f.client.calls, f.cloud.calls)
	}
}

func TestED2KSTRMUncertainSubmissionSurvivesRestartWithoutResubmitting(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	v := f.imported(t)
	v.CacheCID = "11"
	if err := f.service.save(ctx, v); err != nil {
		t.Fatal(err)
	}
	cache := model.ED2KSTRMCache{AccountID: v.AccountID, UID: v.UID, CID: "10", Name: "Cinema115-ED2K-test"}
	if err := f.store.SaveED2KSTRMCache(ctx, cache); err != nil {
		t.Fatal(err)
	}
	f.client.dirs = map[string]p115.DriveDirectory{
		"10": {CID: "10", ParentCID: "0", Name: cache.Name},
		"11": {CID: "11", ParentCID: "10", Name: "ed2k-" + v.Hash + "-1234"},
	}
	adds := 0
	manual := &cloudWorkflowStub{
		lookup: func(context.Context, string, p115.CloudResource) (p115.CloudTask, bool, error) {
			return p115.CloudTask{}, false, nil
		},
		remove: func(context.Context, string, string) error { t.Fatal("unexpected old task removal"); return nil },
		add: func(_ context.Context, _, _ string, cid string) (json.RawMessage, error) {
			adds++
			stored, err := f.store.GetED2KSTRMResource(ctx, v.Hash, v.Size)
			if err != nil || stored.Stage != "submit_pending" || cid != "11" {
				t.Fatalf("submit intent was not durable before request: %+v cid=%s err=%v", stored, cid, err)
			}
			return nil, context.DeadlineExceeded
		},
	}
	downloads := &CloudDownloadService{Accounts: f.accounts, Client: manual, Coordinator: &CloudDownloadCoordinator{}}
	f.service.Downloads = downloads
	if err := f.service.advance(ctx, &v); !CloudDownloadUncertain(err) || v.Status != "uncertain" || adds != 1 {
		t.Fatalf("uncertain submission=%+v adds=%d err=%v", v, adds, err)
	}
	// 模拟回执丢失后立即退出，依靠写入前的持久意图恢复。
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, err = storage.Open(ctx, f.filename)
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = NewED2KSTRMService(ctx, f.store, f.accounts, f.client, f.cloud, downloads, f.service.Root)
	if err != nil {
		t.Fatal(err)
	}
	f.cloud.read = func(_ string, hash string) (p115.ED2KTaskObservation, error) {
		if hash != v.Hash {
			t.Fatalf("observed wrong hash: %s", hash)
		}
		return p115.ED2KTaskObservation{}, nil
	}
	restored := f.service.List()[0]
	for range 2 {
		if err := f.service.advance(ctx, &restored); err != nil {
			t.Fatal(err)
		}
	}
	if adds != 1 || f.cloud.calls != 2 || restored.Status != "uncertain" || restored.Stage != "submit_pending" {
		t.Fatalf("unknown write was replayed: adds=%d observed=%d resource=%+v", adds, f.cloud.calls, restored)
	}
}

func TestED2KSTRMCheckingMissingCacheDoesNotSubmitOffline(t *testing.T) {
	f := newED2KSTRMFixture(t)
	v := f.imported(t)
	v.SHA1, v.FileID, v.PickCode = ed2kSTRMTestSHA1, "file", "pickcode"
	v.Status, v.Stage = "ready", "ready"
	if err := f.service.save(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	f.client.info = func(string, string) (*p115.DownloadInfo, error) { return nil, p115.ErrDriveFileMissing }
	f.client.find = func(sha1, _ string) (*p115.DrivePlaybackFile, error) {
		if sha1 != ed2kSTRMTestSHA1 {
			t.Fatalf("wrong content lookup: %s", sha1)
		}
		return nil, nil
	}
	queued, err := f.service.Request(context.Background(), v.Hash, v.Size, true)
	if err != nil || queued.Stage != "check" {
		t.Fatalf("check request=%+v err=%v", queued, err)
	}
	if len(f.client.calls) != 0 {
		t.Fatalf("queueing a check performed network I/O: %v", f.client.calls)
	}
	if err := f.service.advance(context.Background(), &queued); err != nil {
		t.Fatal(err)
	}
	if queued.CacheState != "missing" || queued.SHA1 != ed2kSTRMTestSHA1 || queued.Status != "ready" || !slices.Equal(f.client.calls, []string{"info", "find"}) || f.cloud.calls != 0 {
		t.Fatalf("check unexpectedly restored resource: %+v calls=%v cloud=%d", queued, f.client.calls, f.cloud.calls)
	}
	if len(f.client.expected) != 1 {
		t.Fatalf("missing-file confirmation did not receive a fixed file identity: %+v", f.client.expected)
	}
	expected := f.client.expected[0]
	if expected.ID != v.FileID || expected.PickCode != v.PickCode || expected.SHA1 != v.SHA1 || expected.Size != v.Size {
		t.Fatalf("missing-file confirmation changed its expected identity: %+v", expected)
	}
}

func TestED2KSTRMCheckingWithoutRecordedSHA1DoesNotCreateOrSubmit(t *testing.T) {
	f := newED2KSTRMFixture(t)
	f.cloud.read = func(string, string) (p115.ED2KTaskObservation, error) {
		return p115.ED2KTaskObservation{}, nil
	}
	v := f.imported(t)
	v.Status, v.Stage = "failed", "locate"
	if err := f.service.save(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	queued, err := f.service.Request(context.Background(), v.Hash, v.Size, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.advance(context.Background(), &queued); err != nil && !errors.Is(err, ErrGenerationInput) {
		t.Fatal(err)
	}
	if len(f.client.calls) != 0 || f.cloud.calls != 1 {
		t.Fatalf("checking an unverified resource did more than observe its task: calls=%v cloud=%d", f.client.calls, f.cloud.calls)
	}
}

func TestED2KSTRMLookupFailuresDoNotBecomeCacheMisses(t *testing.T) {
	for _, mode := range []string{"network", "identity-conflict"} {
		t.Run(mode, func(t *testing.T) {
			f := newED2KSTRMFixture(t)
			v := f.imported(t)
			v.SHA1, v.FileID, v.PickCode = ed2kSTRMTestSHA1, "file", "pickcode"
			upstreamErr := errors.New("temporary upstream failure")
			f.client.info = func(string, string) (*p115.DownloadInfo, error) {
				if mode == "network" {
					return nil, upstreamErr
				}
				return &p115.DownloadInfo{FileID: v.FileID, Size: v.Size, SHA1: strings.Repeat("F", 40)}, nil
			}
			err := f.service.advance(context.Background(), &v)
			if mode == "network" && !errors.Is(err, upstreamErr) || mode == "identity-conflict" && !errors.Is(err, ErrGenerationInput) {
				t.Fatalf("lookup error=%v", err)
			}
			if mode == "identity-conflict" && v.Status != "blocked" {
				t.Fatalf("identity conflict was not blocked: %+v", v)
			}
			if v.SHA1 != ed2kSTRMTestSHA1 || !slices.Equal(f.client.calls, []string{"info"}) || f.cloud.calls != 0 {
				t.Fatalf("lookup failure fell through to restoration: resource=%+v calls=%v cloud=%d", v, f.client.calls, f.cloud.calls)
			}
		})
	}
}

func TestED2KSTRMSHA1ConflictCannotReplaceRecordedIdentity(t *testing.T) {
	f := newED2KSTRMFixture(t)
	v := f.imported(t)
	v.SHA1 = ed2kSTRMTestSHA1
	a, err := f.accounts.Primary(v.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	fresh := p115.DrivePlaybackFile{ID: "file", PickCode: "pickcode", Size: v.Size, SHA1: strings.Repeat("F", 40)}
	if err := f.service.ready(context.Background(), &v, a, fresh); !errors.Is(err, ErrGenerationInput) {
		t.Fatalf("SHA1 conflict accepted: %+v err=%v", v, err)
	}
	if v.SHA1 != ed2kSTRMTestSHA1 || v.Status != "blocked" || v.STRMPath != "" {
		t.Fatalf("conflicting restoration replaced identity: %+v", v)
	}
	files, err := os.ReadDir(f.service.Root)
	if err != nil || len(files) != 0 {
		t.Fatalf("conflicting resource created output: %v err=%v", files, err)
	}
}

func TestED2KSTRMAccountChangesStopQueuedTasksBeforeNetwork(t *testing.T) {
	for _, action := range []string{"refresh", "replace", "clear", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newED2KSTRMFixture(t)
			v := f.imported(t)
			a, err := f.accounts.Primary(v.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			if action == "delete" {
				err = f.accounts.Delete(a.ID, a.Revision)
			} else {
				cookie := "UID=123_B; CID=refreshed"
				if action == "replace" {
					cookie = "UID=999_A; CID=replaced"
				} else if action == "clear" {
					cookie = ""
				}
				err = f.accounts.Update(a.ID, a.Revision, a.Name, &cookie)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.service.advance(context.Background(), &v); err == nil {
				t.Fatal("task continued after account/credential change")
			}
			if v.Status != "blocked" {
				t.Fatalf("changed or missing credentials did not block the task: %+v", v)
			}
			if len(f.client.calls) != 0 || f.cloud.calls != 0 {
				t.Fatalf("account change triggered network: %v cloud=%d", f.client.calls, f.cloud.calls)
			}
		})
	}
}

func TestED2KSTRMGenerationPreservesUnrelatedFiles(t *testing.T) {
	f := newED2KSTRMFixture(t)
	v := f.imported(t)
	v.SHA1 = ed2kSTRMTestSHA1
	destination := filepath.Join(f.service.Root, "ed2k", v.Hash+"-1234", "Film.strm")
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	const unrelated = "https://unrelated.example/original.mkv\n"
	if err := os.WriteFile(destination, []byte(unrelated), 0644); err != nil {
		t.Fatal(err)
	}
	if err := f.service.write(context.Background(), &v); !errors.Is(err, ErrGenerationInput) {
		t.Fatalf("unrelated output was overwritten: %v", err)
	}
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != unrelated || v.STRMPath != "" {
		t.Fatalf("output changed: %q resource=%+v err=%v", got, v, err)
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := f.service.write(context.Background(), &v); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(destination)
	want, linkErr := strm.ED2KURL(v.Settings.BaseURL, strm.ED2K{Hash: v.Hash, SHA1: v.SHA1, Size: v.Size, Name: v.Name})
	if err != nil || linkErr != nil || string(got) != want+"\n" || v.STRMPath != "ed2k/"+v.Hash+"-1234/Film.strm" {
		t.Fatalf("output=%q path=%s err=%v linkErr=%v", got, v.STRMPath, err, linkErr)
	}
}

func TestED2KSTRMOutputsCannotBeCoveredByDirectorySTRM(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	if _, err := f.service.SaveSettings(ctx, model.ED2KSTRMSettings{BaseURL: "https://cinema.example", OutputDir: "exports/ed2k-history"}); err != nil {
		t.Fatal(err)
	}
	_ = f.imported(t)
	if _, err := f.service.SaveSettings(ctx, model.ED2KSTRMSettings{BaseURL: "https://cinema.example", OutputDir: "exports/ed2k-current"}); err != nil {
		t.Fatal(err)
	}
	client := &driveSTRMTestClient{paths: map[string]string{"100": "/library"}}
	drive := NewDriveSTRMService(f.store, f.accounts, client, f.service.Root)
	settings := model.DefaultDriveSTRMSettings()
	settings.BaseURL = "https://cinema.example"
	settings.VideoExtensions = "mkv"
	if _, err := drive.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	a, err := f.accounts.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"exports/ed2k-history", "exports/ed2k-history/child", "exports/ed2k-current", "exports/ed2k-current/child", "exports", "."} {
		t.Run(directory, func(t *testing.T) {
			mapping := model.DriveSTRM{Name: "library", AccountID: a.ID, SourceCID: "100", OutputDir: directory}
			if _, err := drive.SaveWithSchedule(ctx, mapping, nil); !errors.Is(err, ErrGenerationInput) {
				t.Fatalf("overlapping directory accepted: %q err=%v", directory, err)
			}
		})
	}
	if len(client.calls) != 0 {
		t.Fatalf("overlap validation contacted 115: %v", client.calls)
	}
	rows, err := f.store.ListDriveSTRM(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected mappings were persisted: %+v err=%v", rows, err)
	}
	allowed := model.DriveSTRM{Name: "independent", AccountID: a.ID, SourceCID: "100", OutputDir: "exports/ed2k-current-sibling"}
	if _, err := drive.SaveWithSchedule(ctx, allowed, nil); err != nil {
		t.Fatalf("sibling directory incorrectly rejected: %v", err)
	}
}

func TestED2KSTRMObservationUsesExactResultSubtreeWithoutTargetCID(t *testing.T) {
	for _, mode := range []string{"nested-result-directory", "result-file", "same-size-outside-result", "result-not-present"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			v := f.imported(t)
			v.Stage, v.Status, v.CacheCID = "waiting", "downloading", "11"
			good := p115.ShareFile{ID: "15", Name: "Real-result.mkv", PickCode: "actual-pick", Size: v.Size, SHA1: ed2kSTRMTestSHA1}
			outside := p115.ShareFile{ID: "16", Name: "Unrelated.mkv", PickCode: "outside-pick", Size: v.Size, SHA1: strings.Repeat("F", 40)}
			task := p115.ED2KTaskObservation{Present: true, Hash: v.Hash, ResultID: "12", State: "completed"}
			listings := map[string][]p115.ShareFile{
				"11": {{ID: "12", Name: "offline-result", Directory: true}, outside},
				"12": {{ID: "14", Name: "nested", Directory: true}},
				"14": {good},
			}
			success := mode == "nested-result-directory" || mode == "result-file"
			switch mode {
			case "result-file":
				task.ResultID = good.ID
				listings = map[string][]p115.ShareFile{"11": {outside, good}}
			case "same-size-outside-result":
				listings["14"] = []p115.ShareFile{{ID: "17", Name: "Wrong-size.mkv", Size: v.Size + 1, SHA1: ed2kSTRMTestSHA1}}
			case "result-not-present":
				task.ResultID = "99"
				listings = map[string][]p115.ShareFile{"11": {good}}
			}
			f.cloud.read = func(cookie, hash string) (p115.ED2KTaskObservation, error) {
				a, err := f.accounts.Primary(v.AccountID)
				if err != nil || cookie != a.Cookie || hash != v.Hash {
					t.Fatalf("observation changed its credentials or identity: hash=%s err=%v", hash, err)
				}
				return task, nil
			}
			f.client.listing = func(cid, _ string) (p115.DriveDirectory, []p115.ShareFile, error) {
				files, ok := listings[cid]
				if !ok {
					t.Fatalf("read unexpected result directory: %s", cid)
				}
				return p115.DriveDirectory{CID: cid}, files, nil
			}
			if success {
				f.client.info = func(pickcode, _ string) (*p115.DownloadInfo, error) {
					if pickcode != good.PickCode {
						t.Fatalf("selected a file outside the task result: %s", pickcode)
					}
					return &p115.DownloadInfo{FileID: good.ID, Name: good.Name, Size: good.Size, SHA1: good.SHA1}, nil
				}
			}
			err := f.service.advance(ctx, &v)
			if success {
				if err != nil || v.Status != "ready" || v.FileID != good.ID || v.SHA1 != good.SHA1 || v.STRMPath == "" {
					t.Fatalf("valid task result rejected: resource=%+v err=%v", v, err)
				}
				content, err := os.ReadFile(filepath.Join(f.service.Root, filepath.FromSlash(v.STRMPath)))
				if err != nil || !strings.Contains(string(content), "/"+good.SHA1+"/") {
					t.Fatalf("generated STRM=%q err=%v", content, err)
				}
				return
			}
			if err == nil || v.FileID != "" || v.SHA1 != "" || v.STRMPath != "" || slices.Contains(f.client.calls, "info") {
				t.Fatalf("outside/missing result accepted: resource=%+v calls=%v err=%v", v, f.client.calls, err)
			}
			files, readErr := os.ReadDir(f.service.Root)
			if readErr != nil || len(files) != 0 {
				t.Fatalf("unverified result created output: %v err=%v", files, readErr)
			}
		})
	}
}
