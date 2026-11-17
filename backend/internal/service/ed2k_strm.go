package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

type ED2KSTRMStore interface {
	GetED2KSTRMSettings(context.Context) (model.ED2KSTRMSettings, error)
	SaveED2KSTRMSettings(context.Context, model.ED2KSTRMSettings) error
	GetED2KSTRMCache(context.Context, string) (model.ED2KSTRMCache, error)
	SaveED2KSTRMCache(context.Context, model.ED2KSTRMCache) error
	SaveED2KSTRMResource(context.Context, model.ED2KSTRMResource) error
	ListED2KSTRMResources(context.Context) ([]model.ED2KSTRMResource, error)
	ListED2KSTRMTargets(context.Context) ([]model.ED2KSTRMTarget, error)
	SaveED2KSTRMTarget(context.Context, model.ED2KSTRMTarget) error
	DeleteED2KSTRMTarget(context.Context, string) error
	ListED2KSTRMTasks(context.Context) ([]model.ED2KSTRMTask, error)
	SaveED2KSTRMTask(context.Context, model.ED2KSTRMTask) error
	DeleteED2KSTRMTask(context.Context, string) error
	SaveED2KSTRMImport(context.Context, []model.ED2KSTRMResource, []model.ED2KSTRMTask) error
}

type ED2KSTRMClient interface {
	CreateDirectory(context.Context, string, string, string) (string, error)
	ReadDirectory(context.Context, string, string) (p115.DriveDirectory, error)
	ReadDirectoryFiles(context.Context, string, string) (p115.DriveDirectory, []p115.ShareFile, error)
	DirectoryMissing(context.Context, string, string) (bool, error)
	FindPlaybackFileBySHA1Quick(context.Context, string, string) (*p115.DrivePlaybackFile, error)
	PickCodeInstantInfo(context.Context, string, string) (*p115.DownloadInfo, error)
	PickCodeInstantInfoForFile(context.Context, p115.DrivePlaybackFile, string) (*p115.DownloadInfo, error)
	VerifiedPlaybackURL(context.Context, p115.DrivePlaybackFile, string, string) (string, error)
	InstantUpload(context.Context, *p115.DownloadInfo, string, string, func(p115.InstantProgress)) (*p115.InstantUploadResult, error)
	CompletePlaybackUpload(context.Context, *p115.DownloadInfo, string, *p115.InstantUploadResult, string, string) (p115.DrivePlaybackFile, string, error)
}

type ED2KTaskReader interface {
	InspectED2KTask(context.Context, string, string) (p115.ED2KTaskObservation, error)
}

// ED2KSTRMService 独立管理资源与可恢复缓存；读接口不提交离线任务。
type ED2KSTRMService struct {
	Store     ED2KSTRMStore
	Accounts  *P115Accounts
	Client    ED2KSTRMClient
	Cloud     ED2KTaskReader
	Downloads *CloudDownloadService
	Playback  *PlaybackCache
	Root      string
	Logs      *GenerationLogs
	mu        sync.Mutex
	resources map[string]model.ED2KSTRMResource
	targets   map[string]model.ED2KSTRMTarget
	tasks     map[string]model.ED2KSTRMTask
	pinned    map[string]P115Account
	active    string
	wake      chan struct{}
	links     DirectLinkCache
}

func NewED2KSTRMService(ctx context.Context, store ED2KSTRMStore, accounts *P115Accounts, client ED2KSTRMClient, cloud ED2KTaskReader, downloads *CloudDownloadService, root string) (*ED2KSTRMService, error) {
	rows, err := store.ListED2KSTRMResources(ctx)
	if err != nil {
		return nil, err
	}
	targets, err := store.ListED2KSTRMTargets(ctx)
	if err != nil {
		return nil, err
	}
	tasks, err := store.ListED2KSTRMTasks(ctx)
	if err != nil {
		return nil, err
	}
	s := &ED2KSTRMService{Store: store, Accounts: accounts, Client: client, Cloud: cloud, Downloads: downloads, Root: root, resources: make(map[string]model.ED2KSTRMResource, len(rows)), pinned: make(map[string]P115Account), wake: make(chan struct{}, 1)}
	for _, v := range rows {
		s.resources[ed2kResourceKey(v.Hash, v.Size)] = v
	}
	s.targets = make(map[string]model.ED2KSTRMTarget, len(targets))
	for _, v := range targets {
		s.targets[v.ID] = v
	}
	s.tasks = make(map[string]model.ED2KSTRMTask, len(tasks))
	for _, v := range tasks {
		s.tasks[v.ID] = v
	}
	return s, nil
}

func ed2kResourceKey(hash string, size int64) string {
	return strings.ToLower(hash) + ":" + strconv.FormatInt(size, 10)
}
func ed2kCredentialScope(a P115Account) string {
	return DirectLinkKey("ed2k-strm", a.ID, a.UID, strconv.FormatUint(a.Revision, 10), a.Cookie)
}

func parseED2KImport(raw string) (strm.ED2K, error) {
	r, err := p115.ParseCloudResource(raw)
	if err != nil || r.Protocol != "ed2k" {
		return strm.ED2K{}, generationInputError("请填写完整 ED2K 文件链接")
	}
	p := strings.Split(strings.TrimSpace(raw), "|")
	size, err := strconv.ParseInt(p[3], 10, 64)
	if err != nil || size <= 0 {
		return strm.ED2K{}, generationInputError("ED2K 文件大小必须是正整数字节数")
	}
	name, err := url.PathUnescape(p[2])
	if err != nil || !utf8.ValidString(name) || strings.TrimSpace(name) == "" || len([]rune(name)) > 255 || strings.ContainsAny(name, "/\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return strm.ED2K{}, generationInputError("ED2K 文件名无效")
	}
	video := false
	for _, ext := range strings.Split(DefaultVideoExtensions, ",") {
		if strings.EqualFold(path.Ext(name), "."+strings.TrimSpace(strings.TrimPrefix(ext, "."))) {
			video = true
			break
		}
	}
	if !video {
		return strm.ED2K{}, generationInputError("ED2K STRM 仅接收支持的视频文件")
	}
	return strm.ED2K{Hash: r.Hash, Size: size, Name: name}, nil
}

func (s *ED2KSTRMService) SaveSettings(ctx context.Context, v model.ED2KSTRMSettings) (model.ED2KSTRMSettings, error) {
	base, err := ValidateBaseURL(v.BaseURL)
	if err != nil {
		return v, err
	}
	v.BaseURL = base
	v.CacheCID = strings.TrimSpace(v.CacheCID)
	if v.CacheCID == "" {
		v.CacheAccountID, v.CachePath = "", ""
	} else {
		a, err := s.account()
		if err != nil {
			return v, err
		}
		if err := validateED2KCacheSettings(v, a); err != nil {
			return v, err
		}
		if s.Client == nil {
			return v, generationInputError("主网盘目录服务尚未就绪")
		}
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		dir, err := s.Client.ReadDirectory(checkCtx, v.CacheCID, a.Cookie)
		cancel()
		if err != nil {
			return v, fmt.Errorf("核验主网盘缓存目录: %w", err)
		}
		if dir.CID != v.CacheCID || validateP115TargetDirectory(dir.CID, dir.Path) != nil {
			return v, generationInputError("无法确认所选缓存目录，请刷新目录后重新选择")
		}
		v.CachePath = dir.Path
		current, err := s.account()
		if err != nil {
			return v, err
		}
		if ed2kCredentialScope(current) != ed2kCredentialScope(a) {
			return v, generationInputError("主账号或登录凭据已变化，请重新选择缓存目录")
		}
	}
	return v, s.Store.SaveED2KSTRMSettings(ctx, v)
}

// CheckCacheReady 只检查本地配置，不因刷新管理页面访问网盘。
func (s *ED2KSTRMService) CheckCacheReady(settings model.ED2KSTRMSettings) error {
	a, err := s.account()
	if err != nil {
		return err
	}
	return validateED2KCacheSettings(settings, a)
}

func validateED2KCacheSettings(settings model.ED2KSTRMSettings, a P115Account) error {
	if settings.CacheCID == "" {
		return nil
	}
	if settings.CacheAccountID != a.ID {
		return generationInputError("主账号已变化，请重新选择 ED2K 缓存目录")
	}
	if err := validateP115TargetDirectory(settings.CacheCID, settings.CachePath); err != nil {
		return generationInputError("ED2K 缓存目录无效，请重新选择")
	}
	return nil
}
func (s *ED2KSTRMService) List() []model.ED2KSTRMResource {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.ED2KSTRMResource, 0, len(s.resources))
	for _, v := range s.resources {
		v.Aliases = slices.Clone(v.Aliases)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *ED2KSTRMService) account() (P115Account, error) {
	a, err := s.Accounts.Primary("")
	if err != nil {
		return a, err
	}
	if a.Cookie == "" || a.UID == "" || p115.CookieUserID(a.Cookie) != a.UID {
		return a, generationInputError("请先登录明确配置的主账号")
	}
	return a, nil
}

func (s *ED2KSTRMService) sourceAccount(ctx context.Context) (P115Account, error) {
	if a, ok := ed2kSourceAccount(ctx); ok {
		if a.ID == "" || a.Cookie == "" || a.UID == "" || p115.CookieUserID(a.Cookie) != a.UID {
			return a, generationInputError("本次请求固定的主来源凭据不可用")
		}
		return a, nil
	}
	return s.account()
}

func (s *ED2KSTRMService) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *ED2KSTRMService) Request(ctx context.Context, hash string, size int64, check bool) (model.ED2KSTRMResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestLocked(ctx, hash, size, check)
}

func (s *ED2KSTRMService) requestLocked(ctx context.Context, hash string, size int64, check bool) (model.ED2KSTRMResource, error) {
	key := ed2kResourceKey(hash, size)
	v, ok := s.resources[key]
	if !ok {
		return v, storage.ErrNotFound
	}
	if s.active == key || v.Status == "queued" || v.Status == "recovering" || v.Status == "downloading" {
		return v, nil
	}
	a, err := s.account()
	if err != nil {
		return v, err
	}
	if a.ID != v.AccountID || a.UID != v.UID {
		return v, generationInputError("主账号已变化，原资源不能自动转移到新账号")
	}
	// 仅显式操作允许接受同一账号的新凭据；已经执行的未知写入仍只核对原结果。
	v.AccountRevision, v.CredentialScope = a.Revision, ed2kCredentialScope(a)
	if v.Stage != "submit_pending" && v.Stage != "upload_pending" && v.Stage != "waiting" {
		if check {
			v.Stage = "check"
		} else {
			pending, err := s.cacheDirectoryPending(ctx, v)
			if err != nil {
				return v, err
			}
			if !pending {
				settings, err := s.Store.GetED2KSTRMSettings(ctx)
				if err != nil {
					return v, err
				}
				if err := validateED2KCacheSettings(settings, a); err != nil {
					return v, err
				}
				v.CacheTargetCID, v.CacheTargetPath, v.CacheParentCID = settings.CacheCID, settings.CachePath, ""
			}
			v.Stage = "locate"
		}
	}
	v.Status, v.Message, v.Attempts = "queued", "已安排检查缓存", 0
	s.links.Clear()
	v.NextRunAt = time.Time{}
	v.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveED2KSTRMResource(ctx, v); err != nil {
		return v, err
	}
	s.resources[key] = v
	s.pinned[key] = a
	s.notify()
	return v, nil
}

func (s *ED2KSTRMService) save(ctx context.Context, v model.ED2KSTRMResource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ed2kResourceKey(v.Hash, v.Size)
	if old, ok := s.resources[key]; ok {
		v.Aliases = slices.Clone(old.Aliases)
	}
	v.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveED2KSTRMResource(ctx, v); err != nil {
		return err
	}
	s.resources[key] = v
	return nil
}

func (s *ED2KSTRMService) Run(ctx context.Context) error {
	timer := time.NewTicker(3 * time.Second)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if progress, err := s.advanceExport(ctx); err != nil {
			return err
		} else if progress {
			continue
		}
		v, ok := s.pick()
		if ok {
			stepCtx, cancel := context.WithTimeout(ctx, 100*time.Second)
			err := s.advance(stepCtx, &v)
			cancel()
			if err != nil && ctx.Err() == nil {
				v.Attempts++
				v.Message = ed2kPublicError(err)
				v.NextRunAt = time.Now().UTC().Add(time.Duration(min(300, 5*(1<<min(v.Attempts, 5)))) * time.Second)
				if v.Status != "blocked" && v.Status != "uncertain" {
					v.Status = "recovering"
				}
				if v.Attempts >= 8 && v.Status != "blocked" {
					v.Status = "failed"
				}
				if saveErr := s.save(ctx, v); saveErr != nil {
					return saveErr
				}
			}
			s.mu.Lock()
			s.active = ""
			if v.Status == "ready" || v.Status == "failed" || v.Status == "blocked" {
				delete(s.pinned, ed2kResourceKey(v.Hash, v.Size))
			}
			s.mu.Unlock()
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		case <-timer.C:
		}
	}
}

func (s *ED2KSTRMService) pick() (model.ED2KSTRMResource, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var chosen model.ED2KSTRMResource
	key := ""
	now := time.Now()
	for k, v := range s.resources {
		if v.Status != "queued" && v.Status != "recovering" && v.Status != "downloading" && v.Status != "uncertain" {
			continue
		}
		if now.Before(v.NextRunAt) {
			continue
		}
		if key == "" || v.NextRunAt.Before(chosen.NextRunAt) {
			key, chosen = k, v
		}
	}
	s.active = key
	return chosen, key != ""
}

func ed2kPublicError(err error) string {
	if errors.Is(err, p115.ErrInstantUncertain) || CloudDownloadUncertain(err) {
		return "115 处理结果尚未确认，将先核对原操作，请勿重复提交"
	}
	if errors.Is(err, p115.ErrDriveFileMissing) {
		return "缓存文件已不存在"
	}
	var h *p115.HTTPError
	if errors.As(err, &h) {
		return fmt.Sprintf("115 请求暂不可用（HTTP %d），稍后重试", h.Status)
	}
	if errors.Is(err, ErrGenerationInput) {
		return err.Error()
	}
	// 上游错误文本可能包含签名，管理列表仅记录本地安全说明。
	return "缓存处理暂未完成，请稍后重试或检查账号连接"
}

func (s *ED2KSTRMService) taskAccount(v *model.ED2KSTRMResource) (P115Account, error) {
	key := ed2kResourceKey(v.Hash, v.Size)
	s.mu.Lock()
	a, ok := s.pinned[key]
	s.mu.Unlock()
	current, err := s.account()
	if err != nil {
		v.Status = "blocked"
		return current, err
	}
	if current.ID != v.AccountID || current.UID != v.UID || current.Revision != v.AccountRevision || ed2kCredentialScope(current) != v.CredentialScope {
		v.Status = "blocked"
		return current, generationInputError("账号或凭据已变化，任务已暂停，请明确重新检查原账号资源")
	}
	if !ok {
		a = current
		s.mu.Lock()
		s.pinned[key] = a
		s.mu.Unlock()
	}
	return a, nil
}

func (s *ED2KSTRMService) advance(ctx context.Context, v *model.ED2KSTRMResource) error {
	a, err := s.taskAccount(v)
	if err != nil {
		return err
	}
	if v.Stage == "waiting" || v.Stage == "submit_pending" {
		return s.observe(ctx, v, a)
	}
	if v.Stage == "check" && v.SHA1 == "" {
		task, err := s.Cloud.InspectED2KTask(ctx, a.Cookie, v.Hash)
		if err != nil {
			return err
		}
		if task.Present && v.CacheCID != "" {
			return s.observe(ctx, v, a)
		}
		v.Status, v.Message = "failed", "尚无可核验的落盘资料，检查不会重新提交云下载；需要时请手动恢复"
		v.NextRunAt = time.Time{}
		return s.save(ctx, *v)
	}
	if v.SHA1 != "" {
		file, err := s.findCurrent(ctx, *v, a)
		if err != nil {
			if errors.Is(err, ErrGenerationInput) {
				v.Status = "blocked"
			}
			return err
		}
		if file != nil {
			return s.ready(ctx, v, a, *file)
		}
		v.CacheState = "missing"
		v.FileID, v.PickCode = "", ""
		if v.Stage == "check" {
			v.Status, v.Message = "ready", "已确认缓存缺失，可手动恢复或播放时按需恢复"
			v.NextRunAt = time.Time{}
			return s.save(ctx, *v)
		}
		if v.Stage == "upload_pending" {
			v.Status = "uncertain"
			v.NextRunAt = time.Now().UTC().Add(time.Minute)
			v.Message = "秒传结果尚未确认，继续按 SHA1 核对；不会自动重复写入"
			return s.save(ctx, *v)
		}
	}
	if err := s.ensureDirectory(ctx, v, a); err != nil {
		if errors.Is(err, ErrGenerationInput) {
			v.Status = "blocked"
		}
		return err
	}
	if v.SHA1 != "" && s.Playback != nil {
		if handled, err := s.restoreInstant(ctx, v, a); handled || err != nil {
			return err
		}
	}
	// 提交意图先落盘，重启或回执丢失后只查询原任务，不再次调用手动重提入口。
	v.Stage, v.Status, v.Message = "submit_pending", "downloading", "正在提交 ED2K 云下载"
	if err := s.save(ctx, *v); err != nil {
		return err
	}
	targetPath := "/ED2K缓存"
	if v.CacheTargetPath != "" {
		targetPath = path.Join(v.CacheTargetPath, "ed2k-"+v.Hash+"-"+strconv.FormatInt(v.Size, 10))
	}
	_, err = s.Downloads.submitSnapshot(ctx, CloudDownloadConfig{AccountID: a.ID, TargetCID: v.CacheCID, TargetPath: targetPath}, a, ed2kLink(*v))
	if err != nil {
		if CloudDownloadUncertain(err) {
			v.Status = "uncertain"
			return err
		}
		v.Status = "failed"
		v.Stage = "locate"
		v.Message = ed2kPublicError(err)
		return s.save(ctx, *v)
	}
	v.Stage = "waiting"
	v.Message = "115 云下载中，等待核验文件资料"
	v.NextRunAt = time.Now().UTC().Add(5 * time.Second)
	return s.save(ctx, *v)
}

// restoreInstant 将主账号写入锁限定在秒传与结果核验期间，回退离线前释放名额。
func (s *ED2KSTRMService) restoreInstant(ctx context.Context, v *model.ED2KSTRMResource, a P115Account) (bool, error) {
	info, commit, err := s.Playback.PrepareED2KRestore(ctx, strm.ED2K{Hash: v.Hash, SHA1: v.SHA1, Size: v.Size, Name: v.Name})
	if err != nil {
		return false, err
	}
	release, err := s.Playback.acquirePlayback(ctx, a.UID)
	if err != nil {
		return false, err
	}
	defer release()
	if err := s.Accounts.ValidatePrimary(a.ID); err != nil {
		return false, err
	}
	v.Stage, v.Status, v.Message = "upload_pending", "recovering", "正在使用已保存资料恢复主缓存"
	if err := s.save(ctx, *v); err != nil {
		return false, err
	}
	result, uploadErr := s.Client.InstantUpload(ctx, info, v.CacheCID, a.Cookie, nil)
	if uploadErr == nil {
		file, _, err := s.Client.CompletePlaybackUpload(ctx, info, v.CacheCID, result, a.Cookie, p115.PlaybackUserAgent)
		if err != nil {
			v.Status = "uncertain"
			return true, err
		}
		if err := commit(); err != nil {
			return true, err
		}
		return true, s.ready(ctx, v, a, file)
	}
	if errors.Is(uploadErr, p115.ErrInstantUncertain) || CloudDownloadUncertain(uploadErr) {
		v.Status = "uncertain"
		return true, uploadErr
	}
	if errors.Is(uploadErr, p115.ErrInstantUnsupported) || errors.Is(uploadErr, p115.ErrPlaybackRangeUnavailable) {
		return false, nil
	}
	v.Status, v.Stage = "failed", "locate"
	v.Message = "秒传未完成，未自动提交离线，请检查后重新恢复"
	return true, s.save(ctx, *v)
}

func ed2kLink(v model.ED2KSTRMResource) string {
	return "ed2k://|file|" + url.PathEscape(v.Name) + "|" + strconv.FormatInt(v.Size, 10) + "|" + v.Hash + "|/"
}

func (s *ED2KSTRMService) findCurrent(ctx context.Context, v model.ED2KSTRMResource, a P115Account) (*p115.DrivePlaybackFile, error) {
	if v.FileID != "" && v.PickCode != "" {
		info, err := s.Client.PickCodeInstantInfoForFile(ctx, p115.DrivePlaybackFile{ID: v.FileID, PickCode: v.PickCode, SHA1: v.SHA1, Size: v.Size, Name: v.Name}, a.Cookie)
		if err == nil {
			if info.FileID != v.FileID || info.Size != v.Size || !strings.EqualFold(info.SHA1, v.SHA1) {
				return nil, generationInputError("缓存身份与首次记录冲突")
			}
			return &p115.DrivePlaybackFile{ID: v.FileID, PickCode: v.PickCode, SHA1: info.SHA1, Size: info.Size, Name: info.Name, CID: v.CacheCID}, nil
		}
		if !errors.Is(err, p115.ErrDriveFileMissing) {
			return nil, err
		}
	}
	f, err := s.Client.FindPlaybackFileBySHA1Quick(ctx, v.SHA1, a.Cookie)
	if err != nil {
		return nil, err
	}
	if f != nil && (f.Size != v.Size || !strings.EqualFold(f.SHA1, v.SHA1)) {
		return nil, generationInputError("查询文件与首次内容身份冲突")
	}
	return f, nil
}

func (s *ED2KSTRMService) ready(ctx context.Context, v *model.ED2KSTRMResource, a P115Account, f p115.DrivePlaybackFile) error {
	if f.ID == "" || f.PickCode == "" || f.Size != v.Size || !validMaterialHash(f.SHA1) {
		v.Status = "blocked"
		return generationInputError("落盘文件缺少完整身份")
	}
	if v.SHA1 != "" && !strings.EqualFold(v.SHA1, f.SHA1) {
		v.Status = "blocked"
		return generationInputError("恢复文件 SHA1 与首次记录冲突，已停止")
	}
	v.SHA1, v.FileID, v.PickCode = strings.ToUpper(f.SHA1), f.ID, f.PickCode
	v.CacheState = "ready"
	if s.Playback != nil {
		m, ok := s.Playback.materialByID(playbackMaterialID(v.SHA1, v.Size))
		if !ok {
			m = PlaybackMaterial{ID: playbackMaterialID(v.SHA1, v.Size), Name: v.Name, SHA1: v.SHA1, Size: v.Size, CreatedAt: time.Now().UTC()}
		}
		m.VerifiedAt = time.Now().UTC()
		if err := s.Playback.putMaterial(m); err != nil {
			return err
		}
	}
	v.Status, v.Stage, v.Message = "ready", "ready", "上次核验缓存可用"
	v.Attempts = 0
	v.NextRunAt = time.Time{}
	if err := s.save(ctx, *v); err != nil {
		return err
	}
	if s.Logs != nil {
		s.Logs.Append("STRM", "ED2K "+v.Hash[:8], "ED2K STRM", "INFO", "资源已就绪："+safeSTRMName(v.Name))
	}
	return nil
}
