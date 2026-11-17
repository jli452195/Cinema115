package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

var errED2KDirectoryPending = errors.New("ED2K 缓存目录创建结果尚待确认")

func (s *ED2KSTRMService) directoryByName(ctx context.Context, parent, name, cookie string) (string, error) {
	_, files, err := s.Client.ReadDirectoryFiles(ctx, parent, cookie)
	if err != nil {
		return "", err
	}
	id := ""
	for _, f := range files {
		if f.Name != name {
			continue
		}
		if !f.Directory || id != "" {
			return "", generationInputError("缓存目录名称冲突，已停止创建")
		}
		id = f.ID
	}
	return id, nil
}

func (s *ED2KSTRMService) ensureDirectory(ctx context.Context, v *model.ED2KSTRMResource, a P115Account) error {
	cache, err := s.Store.GetED2KSTRMCache(ctx, a.ID)
	if errors.Is(err, storage.ErrNotFound) {
		cache = model.ED2KSTRMCache{AccountID: a.ID, UID: a.UID, Name: "Cinema115-ED2K-" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	} else if err != nil {
		return err
	}
	if cache.UID != a.UID {
		return generationInputError("缓存目录不属于当前主账号")
	}
	if cache.CID != "" {
		dir, err := s.Client.ReadDirectory(ctx, cache.CID, a.Cookie)
		if err == nil {
			if dir.CID != cache.CID || dir.ParentCID != "0" || dir.Name != cache.Name {
				return generationInputError("主缓存目录归属或位置已变化")
			}
		} else {
			missing, checkErr := s.Client.DirectoryMissing(ctx, cache.CID, a.Cookie)
			if checkErr != nil {
				return checkErr
			}
			if !missing {
				return err
			}
			cache.CID = ""
			cache.Pending = false
		}
	}
	if cache.CID == "" {
		if cache.Pending {
			cid, err := s.directoryByName(ctx, "0", cache.Name, a.Cookie)
			if err != nil {
				return err
			}
			if cid == "" {
				v.Status = "uncertain"
				return errED2KDirectoryPending
			}
			cache.CID, cache.Pending = cid, false
		} else {
			cache.Pending = true
			if err := s.Store.SaveED2KSTRMCache(ctx, cache); err != nil {
				return err
			}
			if err := s.Accounts.ValidatePrimary(a.ID); err != nil {
				return err
			}
			cid, err := s.Client.CreateDirectory(ctx, "0", cache.Name, a.Cookie)
			if err != nil {
				v.Status = "uncertain"
				return err
			}
			cache.CID, cache.Pending = cid, false
		}
		if err := s.Store.SaveED2KSTRMCache(ctx, cache); err != nil {
			return err
		}
	}
	name := "ed2k-" + v.Hash + "-" + strconv.FormatInt(v.Size, 10)
	if v.CacheCID != "" {
		dir, err := s.Client.ReadDirectory(ctx, v.CacheCID, a.Cookie)
		if err == nil {
			if dir.CID != v.CacheCID || dir.ParentCID != cache.CID || dir.Name != name {
				return generationInputError("资源缓存目录身份已变化")
			}
			return nil
		}
		missing, checkErr := s.Client.DirectoryMissing(ctx, v.CacheCID, a.Cookie)
		if checkErr != nil {
			return checkErr
		}
		if !missing {
			return err
		}
		v.CacheCID = ""
		v.DirectoryPending = false
	}
	if v.DirectoryPending {
		cid, err := s.directoryByName(ctx, cache.CID, name, a.Cookie)
		if err != nil {
			return err
		}
		if cid == "" {
			v.Status = "uncertain"
			return errED2KDirectoryPending
		}
		v.CacheCID, v.DirectoryPending = cid, false
		return s.save(ctx, *v)
	}
	v.DirectoryPending = true
	if err := s.save(ctx, *v); err != nil {
		return err
	}
	if err := s.Accounts.ValidatePrimary(a.ID); err != nil {
		return err
	}
	cid, err := s.Client.CreateDirectory(ctx, cache.CID, name, a.Cookie)
	if err != nil {
		v.Status = "uncertain"
		return err
	}
	v.CacheCID, v.DirectoryPending = cid, false
	dir, err := s.Client.ReadDirectory(ctx, cid, a.Cookie)
	if err != nil {
		return err
	}
	if dir.CID != cid || dir.ParentCID != cache.CID || dir.Name != name {
		return generationInputError("新资源缓存目录核验失败")
	}
	return s.save(ctx, *v)
}

func (s *ED2KSTRMService) observe(ctx context.Context, v *model.ED2KSTRMResource, a P115Account) error {
	task, err := s.Cloud.InspectED2KTask(ctx, a.Cookie, v.Hash)
	if err != nil {
		return err
	}
	if !task.Present {
		v.Status = "uncertain"
		v.Message = "未找到可确认的原云任务，不会自动重复提交；请检查 115 云下载"
		v.NextRunAt = time.Now().UTC().Add(time.Minute)
		return s.save(ctx, *v)
	}
	if task.Hash != v.Hash || task.TargetCID != "" && task.TargetCID != v.CacheCID {
		v.Status = "blocked"
		return generationInputError("ED2K 任务的资源或缓存目录与原请求不一致")
	}
	if task.State == "failed" {
		v.Status, v.Stage, v.Message = "failed", "locate", "115 云下载失败或已停止，可稍后手动恢复"
		return s.save(ctx, *v)
	}
	if task.State != "completed" {
		v.Stage, v.Status, v.Message = "waiting", "downloading", "115 云下载中，等待文件完成并进入缓存目录"
		v.NextRunAt = time.Now().UTC().Add(10 * time.Second)
		return s.save(ctx, *v)
	}
	files, err := s.resultFiles(ctx, v.CacheCID, a.Cookie, 0)
	if err != nil {
		return err
	}
	var chosen *p115.ShareFile
	resultFound := task.ResultID == v.CacheCID
	parents := make(map[string]string, len(files))
	for _, item := range files {
		parents[item.File.ID] = item.ParentCID
	}
	for i := range files {
		f := &files[i].File
		if f.ID == task.ResultID {
			resultFound = true
		}
		if f.Directory {
			continue
		}
		belongs := task.ResultID == v.CacheCID || task.ResultID == f.ID
		for parent, depth := files[i].ParentCID, 0; !belongs && parent != "" && depth < 5; depth++ {
			belongs = parent == task.ResultID
			parent = parents[parent]
		}
		if !belongs {
			continue
		}
		if f.Size != v.Size {
			continue
		}
		if v.SHA1 != "" && !strings.EqualFold(f.SHA1, v.SHA1) {
			continue
		}
		if chosen != nil {
			v.Status = "blocked"
			return generationInputError("缓存中有多个相同大小的文件，无法唯一核验结果")
		}
		chosen = f
	}
	if !resultFound || chosen == nil {
		return fmt.Errorf("离线完成，但缓存文件资料尚未就绪")
	}
	info, err := s.Client.PickCodeInstantInfo(ctx, chosen.PickCode, a.Cookie)
	if err != nil {
		return err
	}
	if info.FileID != chosen.ID || info.Size != v.Size || !strings.EqualFold(info.SHA1, chosen.SHA1) {
		v.Status = "blocked"
		return generationInputError("离线文件身份核验失败")
	}
	return s.ready(ctx, v, a, p115.DrivePlaybackFile{ID: chosen.ID, CID: v.CacheCID, PickCode: chosen.PickCode, SHA1: chosen.SHA1, Size: chosen.Size, Name: chosen.Name})
}

type ed2kResultFile struct {
	File      p115.ShareFile
	ParentCID string
}

func (s *ED2KSTRMService) resultFiles(ctx context.Context, cid, cookie string, depth int) ([]ed2kResultFile, error) {
	out := make([]ed2kResultFile, 0, 4)
	seen := make(map[string]bool)
	var walk func(string, int) error
	walk = func(id string, level int) error {
		if level > 3 || seen[id] {
			return generationInputError("ED2K 缓存目录层级或身份异常")
		}
		seen[id] = true
		_, files, err := s.Client.ReadDirectoryFiles(ctx, id, cookie)
		if err != nil {
			return err
		}
		if len(out)+len(files) > 128 {
			return generationInputError("ED2K 缓存目录包含过多文件，无法安全关联")
		}
		for _, f := range files {
			out = append(out, ed2kResultFile{File: f, ParentCID: id})
			if f.Directory {
				if err := walk(f.ID, level+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(cid, depth); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *ED2KSTRMService) claimResource(claim strm.ED2K) (model.ED2KSTRMResource, bool, error) {
	if _, err := strm.ED2KPath(claim); err != nil {
		return model.ED2KSTRMResource{}, false, err
	}
	s.mu.Lock()
	v, ok := s.resources[ed2kResourceKey(claim.Hash, claim.Size)]
	s.mu.Unlock()
	if ok && (v.SHA1 == "" || !strings.EqualFold(v.SHA1, claim.SHA1)) {
		return v, true, generationInputError("STRM 声明与已登记资源身份冲突")
	}
	return v, ok, nil
}

// ResolveSource 只在缺少校验片段时读取来源；未知 STRM 不能创建云下载任务。
func (s *ED2KSTRMService) ResolveSource(ctx context.Context, claim strm.ED2K, allowRestore bool) (*p115.DownloadInfo, error) {
	v, known, err := s.claimResource(claim)
	if err != nil {
		return nil, err
	}
	a, err := s.sourceAccount(ctx)
	if err != nil {
		return nil, err
	}
	if known && (v.AccountID != a.ID || v.UID != a.UID) {
		return nil, generationInputError("资源缓存属于原主账号，请先处理账号变更")
	}
	if !known {
		v = model.ED2KSTRMResource{SHA1: claim.SHA1, Size: claim.Size, Name: claim.Name}
	}
	f, err := s.findCurrent(ctx, v, a)
	if err != nil {
		return nil, err
	}
	if f != nil {
		info, err := s.Client.PickCodeInstantInfo(ctx, f.PickCode, a.Cookie)
		if err != nil {
			return nil, err
		}
		if info.FileID != f.ID || info.Size != claim.Size || !strings.EqualFold(info.SHA1, claim.SHA1) {
			return nil, generationInputError("校验来源身份不一致")
		}
		return info, nil
	}
	return nil, s.requestSourceRestore(ctx, claim, v, known, allowRestore, a)
}

func (s *ED2KSTRMService) requestSourceRestore(ctx context.Context, claim strm.ED2K, v model.ED2KSTRMResource, known, allow bool, a P115Account) error {
	if !known {
		return generationInputError("目标账号未命中且无已登记恢复来源，请先导入原 ED2K")
	}
	if allow {
		if v.AccountRevision != a.Revision || v.CredentialScope != ed2kCredentialScope(a) {
			return generationInputError("来源凭据已变化，请在 ED2K STRM 页面重新检查")
		}
		if _, err := s.Request(ctx, claim.Hash, claim.Size, false); err != nil {
			return err
		}
	}
	return &PlaybackPendingError{RetryAt: time.Now().UTC().Add(5 * time.Second), Reason: "ED2K 缓存尚未就绪，请在资源页面查看恢复进度"}
}
