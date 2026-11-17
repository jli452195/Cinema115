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
	t         *testing.T
	calls     []string
	expected  []p115.DrivePlaybackFile
	dirs      map[string]p115.DriveDirectory
	directory func(string, string) (p115.DriveDirectory, error)
	missing   func(string, string) (bool, error)
	find      func(string, string) (*p115.DrivePlaybackFile, error)
	info      func(string, string) (*p115.DownloadInfo, error)
	listing   func(string, string) (p115.DriveDirectory, []p115.ShareFile, error)
	create    func(string, string, string) (string, error)
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
func (c *ed2kSTRMClientStub) ReadDirectory(_ context.Context, cid, cookie string) (p115.DriveDirectory, error) {
	c.calls = append(c.calls, "directory")
	if c.directory != nil {
		return c.directory(cid, cookie)
	}
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
func (c *ed2kSTRMClientStub) DirectoryMissing(_ context.Context, cid, cookie string) (bool, error) {
	c.calls = append(c.calls, "directory-missing")
	if c.missing != nil {
		return c.missing(cid, cookie)
	}
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
	target   model.ED2KSTRMTarget
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
	if _, err := f.service.SaveSettings(ctx, model.ED2KSTRMSettings{BaseURL: "https://cinema.example"}); err != nil {
		t.Fatal(err)
	}
	f.target, err = f.service.SaveTarget(ctx, model.ED2KSTRMTarget{Name: "默认输出", OutputDir: "ed2k"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *ed2kSTRMFixture) imported(t *testing.T) model.ED2KSTRMResource {
	t.Helper()
	_, err := f.service.ImportTo(context.Background(), f.target.ID, []string{ed2kSTRMTestLink("Film.mkv", ed2kSTRMTestHash, ed2kSTRMTestSize)}, "initial-import")
	if err != nil {
		t.Fatal(err)
	}
	rows := f.service.List()
	if len(rows) != 1 {
		t.Fatalf("import=%+v", rows)
	}
	return rows[0]
}

func ed2kSTRMTestLink(name, hash string, size int64) string {
	return "ed2k://|file|" + url.PathEscape(name) + "|" + strconv.FormatInt(size, 10) + "|" + hash + "|/"
}

func TestED2KSTRMImportDeduplicatesContentAcrossNames(t *testing.T) {
	f := newED2KSTRMFixture(t)
	original := f.imported(t)
	_, err := f.service.ImportTo(context.Background(), f.target.ID, []string{
		ed2kSTRMTestLink("另一个名字.mkv", strings.ToUpper(ed2kSTRMTestHash), ed2kSTRMTestSize),
		ed2kSTRMTestLink("重复名字.mkv", ed2kSTRMTestHash, ed2kSTRMTestSize),
	}, "alias-import")
	rows := f.service.List()
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
	if _, err := f.service.ImportTo(context.Background(), f.target.ID, []string{ed2kSTRMTestLink("Different-size.mkv", ed2kSTRMTestHash, ed2kSTRMTestSize+1)}, "different-size"); err != nil {
		t.Fatal(err)
	}
	if len(f.service.List()) != 2 {
		t.Fatal("same hash with a different size was incorrectly reused")
	}
}

func TestED2KSTRMSettingsAndListingDoNotContact115(t *testing.T) {
	f := newED2KSTRMFixture(t)
	_ = f.imported(t)
	settings := model.ED2KSTRMSettings{BaseURL: "https://new.example"}
	if _, err := f.service.SaveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		rows := f.service.List()
		if len(rows) != 1 {
			t.Fatalf("resource listing changed: %+v", rows)
		}
	}
	for _, task := range f.service.tasks {
		if task.BaseURL != "https://cinema.example" || task.OutputDir != "ed2k" {
			t.Fatalf("accepted export snapshot followed current settings: %+v", task)
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
	v.CacheCID, v.CacheParentCID = "11", "10"
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
	if v.SHA1 != ed2kSTRMTestSHA1 || v.Status != "blocked" {
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
	v.SHA1, v.Status, v.CacheState = ed2kSTRMTestSHA1, "ready", "missing"
	if err := f.service.save(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(f.service.Root, "ed2k", "Film.strm")
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	const unrelated = "https://unrelated.example/original.mkv\n"
	if err := os.WriteFile(destination, []byte(unrelated), 0644); err != nil {
		t.Fatal(err)
	}
	f.finishExports(t)
	got, err := os.ReadFile(destination)
	if err != nil || string(got) != unrelated {
		t.Fatalf("unrelated output changed: %q err=%v", got, err)
	}
	if len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatal("known identity export contacted cloud")
	}
	want, err := strm.ED2KURL("https://cinema.example", strm.ED2K{Hash: v.Hash, SHA1: v.SHA1, Size: v.Size, Name: v.Name})
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(f.service.Tasks()) != 1 || f.service.Tasks()[0].Status != "failed" {
		t.Fatalf("collision should preserve original file and report failed output: files=%v tasks=%+v", files, f.service.Tasks())
	}
	for _, file := range files {
		if file.Name() == "Film.strm" {
			continue
		}
		got, err := os.ReadFile(filepath.Join(filepath.Dir(destination), file.Name()))
		if err != nil || string(got) != want+"\n" {
			t.Fatalf("collision output invalid: name=%s content=%q err=%v", file.Name(), got, err)
		}
	}
}

func (f *ed2kSTRMFixture) finishExports(t *testing.T) {
	t.Helper()
	for range 100 {
		progress, err := f.service.advanceExport(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !progress {
			return
		}
	}
	t.Fatal("export worker kept reporting progress without reaching idle")
}

func (f *ed2kSTRMFixture) knownResource(t *testing.T) model.ED2KSTRMResource {
	t.Helper()
	v := f.imported(t)
	v.SHA1, v.Status, v.CacheState, v.Stage = ed2kSTRMTestSHA1, "ready", "missing", "ready"
	if err := f.service.save(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *ed2kSTRMFixture) restart(t *testing.T) {
	t.Helper()
	root, downloads := f.service.Root, f.service.Downloads
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, err = storage.Open(context.Background(), f.filename)
	if err != nil {
		t.Fatal(err)
	}
	f.service, err = NewED2KSTRMService(context.Background(), f.store, f.accounts, f.client, f.cloud, downloads, root)
	if err != nil {
		t.Fatal(err)
	}
}

func TestED2KSTRMMovedOutputIsNeverMaintainedByCacheOrRestart(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	v := f.knownResource(t)
	f.finishExports(t)
	if len(f.service.Tasks()) != 0 {
		t.Fatalf("completed task remained visible: %+v", f.service.Tasks())
	}
	original := filepath.Join(f.service.Root, "ed2k", "Film.strm")
	moved := filepath.Join(t.TempDir(), "organized.strm")
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(moved)
	if err != nil {
		t.Fatal(err)
	}
	f.client.find = func(string, string) (*p115.DrivePlaybackFile, error) {
		return &p115.DrivePlaybackFile{ID: "123", PickCode: "pick123", SHA1: v.SHA1, Size: v.Size, Name: v.Name}, nil
	}
	f.client.info = func(string, string) (*p115.DownloadInfo, error) {
		return &p115.DownloadInfo{FileID: "123", SHA1: v.SHA1, Size: v.Size, Name: v.Name}, nil
	}
	for _, check := range []bool{true, false} {
		queued, err := f.service.Request(ctx, v.Hash, v.Size, check)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.service.advance(ctx, &queued); err != nil {
			t.Fatal(err)
		}
		f.finishExports(t)
		if _, err := os.Stat(original); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cache check/restore recreated moved STRM: %v", err)
		}
	}
	f.restart(t)
	f.finishExports(t)
	if len(f.service.Tasks()) != 0 {
		t.Fatal("restart returned completed outputs to pending state")
	}
	if _, err := os.Stat(original); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restart recreated moved STRM: %v", err)
	}
	after, err := os.ReadFile(moved)
	if err != nil || string(after) != string(content) {
		t.Fatalf("organized file changed: %q err=%v", after, err)
	}
	stored, err := f.store.ListED2KSTRMTasks(ctx)
	if err != nil || len(stored) != 1 {
		t.Fatalf("missing replay receipt: %+v err=%v", stored, err)
	}
	for _, task := range stored {
		if task.Status != "completed" || task.OutputDir != "" || task.BaseURL != "" || task.Name != "" || task.TargetID != "" || task.TargetName != "" {
			t.Fatalf("completed receipt retained output location: %+v", task)
		}
	}
}

func TestED2KSTRMRequestReplayDoesNotReExportButNewRequestDoes(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	v := f.knownResource(t)
	links := []string{ed2kSTRMTestLink(v.Name, v.Hash, v.Size)}
	pending, err := f.service.ImportTo(ctx, f.target.ID, links, "initial-import")
	if err != nil || len(pending.Tasks) != 1 || len(f.service.Tasks()) != 1 {
		t.Fatalf("pending replay created tasks: %+v err=%v", pending, err)
	}
	f.finishExports(t)
	filename := filepath.Join(f.service.Root, "ed2k", "Film.strm")
	if err := os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	replay, err := f.service.ImportTo(ctx, f.target.ID, links, "initial-import")
	if err != nil || len(replay.Tasks) != 1 || replay.Tasks[0].ID != pending.Tasks[0].ID {
		t.Fatalf("completed replay lost its receipt: %+v err=%v", replay, err)
	}
	f.finishExports(t)
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("same request recreated removed output: %v", err)
	}
	other, err := f.service.SaveTarget(ctx, model.ED2KSTRMTarget{Name: "另一个目录", OutputDir: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ImportTo(ctx, other.ID, links, "initial-import"); !errors.Is(err, ErrGenerationInput) {
		t.Fatalf("same request ID accepted a different target: %v", err)
	}
	explicit, err := f.service.ImportTo(ctx, f.target.ID, links, "explicit-new-request")
	if err != nil || len(explicit.Tasks) != 1 || explicit.Tasks[0].ID == pending.Tasks[0].ID {
		t.Fatalf("explicit new request did not create output work: %+v err=%v", explicit, err)
	}
	f.finishExports(t)
	if _, err := os.Stat(filename); err != nil {
		t.Fatalf("explicit request did not re-export: %v", err)
	}
	if len(f.service.List()) != 1 || len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatal("re-export added resources or contacted 115")
	}
}

func TestED2KSTRMOneResourceExportsToMultipleTargetsWithoutCloudReads(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	v := f.knownResource(t)
	other, err := f.service.SaveTarget(ctx, model.ED2KSTRMTarget{Name: "第二目录", OutputDir: "second"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.service.ImportTo(ctx, other.ID, []string{ed2kSTRMTestLink("另一名称.mkv", v.Hash, v.Size)}, "other-target")
	if err != nil || len(result.Resources) != 1 || len(result.Tasks) != 1 || len(f.service.List()) != 1 || len(f.service.Tasks()) != 2 {
		t.Fatalf("multi-target import duplicated content: %+v err=%v", result, err)
	}
	f.finishExports(t)
	for directory, name := range map[string]string{"ed2k": "Film.strm", "second": "另一名称.strm"} {
		data, err := os.ReadFile(filepath.Join(f.service.Root, directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "/"+v.SHA1+"/") {
			t.Fatalf("wrong exported content: %q", data)
		}
		files, err := os.ReadDir(filepath.Join(f.service.Root, directory))
		if err != nil || len(files) != 1 || files[0].IsDir() {
			t.Fatalf("output was not flat: %v err=%v", files, err)
		}
	}
	if len(f.service.Tasks()) != 0 || len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatal("known SHA1 export retained tasks or accessed 115")
	}
}

func TestED2KSTRMAcceptedOutputKeepsTargetAndAddressSnapshot(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	_ = f.knownResource(t)
	f.target.Name, f.target.OutputDir = "修改后目录", "new-ed2k"
	if _, err := f.service.SaveTarget(ctx, f.target); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.SaveSettings(ctx, model.ED2KSTRMSettings{BaseURL: "https://changed.example"}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteTarget(ctx, f.target.ID); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	f.finishExports(t)
	data, err := os.ReadFile(filepath.Join(f.service.Root, "ed2k", "Film.strm"))
	if err != nil || !strings.HasPrefix(string(data), "https://cinema.example/api/ed2k-strm/") {
		t.Fatalf("accepted output followed edited or deleted target: %q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(f.service.Root, "new-ed2k")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("accepted output wrote current target: %v", err)
	}
	if len(f.service.Targets()) != 0 || len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatal("target deletion did not remain independent from accepted local output")
	}
}

func TestED2KSTRMInterruptedOutputNeverRecreatesMissingFileOnRestart(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	_ = f.knownResource(t)
	tasks := f.service.Tasks()
	if len(tasks) != 1 {
		t.Fatal(tasks)
	}
	task := tasks[0]
	task.Status = "writing"
	if err := f.service.saveExport(ctx, task); err != nil {
		t.Fatal(err)
	}
	f.restart(t)
	f.finishExports(t)
	if _, err := os.Stat(filepath.Join(f.service.Root, "ed2k", "Film.strm")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uncertain output was silently repeated: %v", err)
	}
	tasks = f.service.Tasks()
	if len(tasks) != 1 || tasks[0].Status != "failed" {
		t.Fatalf("uncertain output did not become reviewable failure: %+v", tasks)
	}
	if err := f.service.RetryTask(ctx, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	f.finishExports(t)
	if _, err := os.Stat(filepath.Join(f.service.Root, "ed2k", "Film.strm")); err != nil {
		t.Fatalf("explicit retry did not output STRM: %v", err)
	}
	if len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatal("local retry accessed 115")
	}
}

func TestED2KSTRMSameNameDifferentContentDoesNotOverwrite(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	first := f.knownResource(t)
	otherHash := strings.Repeat("b", 32)
	if _, err := f.service.ImportTo(ctx, f.target.ID, []string{ed2kSTRMTestLink("Film.mkv", otherHash, ed2kSTRMTestSize)}, "different-content"); err != nil {
		t.Fatal(err)
	}
	other := f.service.resources[ed2kResourceKey(otherHash, ed2kSTRMTestSize)]
	other.SHA1, other.Status, other.Stage = strings.Repeat("F", 40), "ready", "ready"
	if err := f.service.save(ctx, other); err != nil {
		t.Fatal(err)
	}
	f.finishExports(t)
	data, err := os.ReadFile(filepath.Join(f.service.Root, "ed2k", "Film.strm"))
	if err != nil || !strings.Contains(string(data), "/"+first.Hash+"/"+first.SHA1+"/") {
		t.Fatalf("later same-name content overwrote earlier output: %q err=%v", data, err)
	}
	tasks := f.service.Tasks()
	if len(tasks) != 1 || tasks[0].Hash != otherHash || tasks[0].Status != "failed" {
		t.Fatalf("collision did not expose failed task: %+v", tasks)
	}
	if len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatal("collision handling accessed 115")
	}
}

func TestED2KSTRMTargetRejectsExistingDirectorySTRMOverlapLocally(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	client := &driveSTRMTestClient{paths: map[string]string{"100": "/library"}}
	drive := NewDriveSTRMService(f.store, f.accounts, client, f.service.Root)
	settings := model.DefaultDriveSTRMSettings()
	settings.BaseURL, settings.VideoExtensions = "https://cinema.example", "mkv"
	if _, err := drive.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	a, err := f.accounts.Primary("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drive.Save(ctx, model.DriveSTRM{Name: "existing", AccountID: a.ID, SourceCID: "100", OutputDir: "library/movies"}); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"library/movies", "library/movies/child", "library", "."} {
		if _, err := f.service.SaveTarget(ctx, model.ED2KSTRMTarget{Name: "重叠目录", OutputDir: directory}); !errors.Is(err, ErrGenerationInput) {
			t.Fatalf("overlapping ED2K target accepted: %s err=%v", directory, err)
		}
	}
	if len(f.service.Targets()) != 1 || len(f.client.calls) != 0 || f.cloud.calls != 0 {
		t.Fatal("rejected targets changed state or accessed 115")
	}
}

func TestED2KSTRMImportRejectsResourceOwnedByPreviousPrimary(t *testing.T) {
	for _, knownSHA1 := range []bool{false, true} {
		t.Run(strconv.FormatBool(knownSHA1), func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			v := f.imported(t)
			if knownSHA1 {
				v.SHA1, v.Status, v.Stage = ed2kSTRMTestSHA1, "ready", "ready"
				if err := f.service.save(ctx, v); err != nil {
					t.Fatal(err)
				}
			}
			a, err := f.accounts.Primary(v.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			cookie := "UID=999_A; CID=new-primary"
			if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
				t.Fatal(err)
			}
			links := []string{ed2kSTRMTestLink("Film.mkv", v.Hash, v.Size)}
			if _, err := f.service.ImportTo(ctx, f.target.ID, links, "new-primary-output"); !errors.Is(err, ErrGenerationInput) {
				t.Fatalf("old-primary content accepted as current-primary output: %v", err)
			}
			resources := f.service.List()
			if len(resources) != 1 || resources[0].AccountID != v.AccountID || resources[0].UID != v.UID || resources[0].CredentialScope != v.CredentialScope || resources[0].SHA1 != v.SHA1 || len(f.service.Tasks()) != 1 {
				t.Fatalf("rejected import changed resource or output tasks: resources=%+v tasks=%+v", resources, f.service.Tasks())
			}
			if _, err := f.service.ImportTo(ctx, f.target.ID, links, "initial-import"); err != nil {
				t.Fatalf("safe old-request replay did not return original receipt: %v", err)
			}
			if len(f.client.calls) != 0 || f.cloud.calls != 0 {
				t.Fatal("primary ownership rejection or request replay contacted 115")
			}
		})
	}
}

func TestED2KSTRMRetryAndDeleteTaskCannotBothSucceed(t *testing.T) {
	for _, knownSHA1 := range []bool{false, true} {
		t.Run(strconv.FormatBool(knownSHA1), func(t *testing.T) {
			ctx := context.Background()
			f := newED2KSTRMFixture(t)
			v := f.imported(t)
			if knownSHA1 {
				v.SHA1 = ed2kSTRMTestSHA1
			}
			v.Status, v.Stage = "failed", "locate"
			task := f.service.Tasks()[0]
			task.Status, task.Message = "failed", "首次输出失败"
			for range 48 {
				if err := f.service.save(ctx, v); err != nil {
					t.Fatal(err)
				}
				if err := f.service.saveExport(ctx, task); err != nil {
					t.Fatal(err)
				}
				start := make(chan struct{})
				retried, deleted := make(chan error, 1), make(chan error, 1)
				go func() { <-start; retried <- f.service.RetryTask(ctx, task.ID) }()
				go func() { <-start; deleted <- f.service.DeleteTask(ctx, task.ID) }()
				close(start)
				retryErr, deleteErr := <-retried, <-deleted
				if (retryErr == nil) == (deleteErr == nil) {
					t.Fatalf("retry and cleanup must serialize to one accepted operation: retry=%v delete=%v", retryErr, deleteErr)
				}
				current := f.service.tasks[task.ID]
				if deleteErr == nil {
					if current.Status != "completed" || current.OutputDir != "" || current.Name != "" || current.TargetID != "" || current.BaseURL != "" {
						t.Fatalf("successful cleanup was revived by concurrent retry: %+v", current)
					}
					if f.service.List()[0].Status != "failed" {
						t.Fatal("successful cleanup allowed delayed cloud recovery to queue")
					}
				} else if current.Status != "queued" || current.OutputDir != task.OutputDir || current.Name != task.Name {
					t.Fatalf("successful retry lost its accepted output snapshot: %+v", current)
				}
			}
			if len(f.client.calls) != 0 || f.cloud.calls != 0 {
				t.Fatal("task controls performed network I/O")
			}
		})
	}
}

func TestED2KSTRMOutputsCannotBeCoveredByDirectorySTRM(t *testing.T) {
	ctx := context.Background()
	f := newED2KSTRMFixture(t)
	f.target.OutputDir = "exports/ed2k-history"
	var err error
	if f.target, err = f.service.SaveTarget(ctx, f.target); err != nil {
		t.Fatal(err)
	}
	_ = f.imported(t)
	f.target.OutputDir = "exports/ed2k-current"
	if f.target, err = f.service.SaveTarget(ctx, f.target); err != nil {
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
				if err != nil || v.Status != "ready" || v.FileID != good.ID || v.SHA1 != good.SHA1 {
					t.Fatalf("valid task result rejected: resource=%+v err=%v", v, err)
				}
				files, err := os.ReadDir(f.service.Root)
				if err != nil || len(files) != 0 {
					t.Fatalf("cache completion wrote STRM without processing its output task: %v err=%v", files, err)
				}
				return
			}
			if err == nil || v.FileID != "" || v.SHA1 != "" || slices.Contains(f.client.calls, "info") {
				t.Fatalf("outside/missing result accepted: resource=%+v calls=%v err=%v", v, f.client.calls, err)
			}
			files, readErr := os.ReadDir(f.service.Root)
			if readErr != nil || len(files) != 0 {
				t.Fatalf("unverified result created output: %v err=%v", files, readErr)
			}
		})
	}
}
