package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

// DownloadShare keeps generation settings pinned without storing credentials.
type DownloadShare struct {
	HandedOff  bool               `json:"handedOff,omitempty"`
	Directory  string             `json:"directory"`
	Settings   AutomationSettings `json:"settings"`
	ReadyAt    time.Time          `json:"readyAt,omitempty"`
	LeaderID   string             `json:"leaderId,omitempty"`
	SourceDirs []string           `json:"sourceDirs,omitempty"`
}

type downloadShareQueue interface {
	SnapshotDownloadShare(string) (AutomationSettings, error)
	EnqueueDownloadShare(context.Context, OrganizeJob, string) error
}

type downloadShareClient interface {
	ShareLister
	DriveFiles(context.Context, string, int, string) (*p115.SharePage, error)
	SendShare(context.Context, []string, string) (*p115.ShareInfo, error)
	ConfigureShare(context.Context, *p115.ShareInfo, string, string) error
	DeleteDirectory(context.Context, string, string) error
}

type downloadShareRequest struct {
	ParentCID   string           `json:"parentCID"`
	Directory   string           `json:"directory"`
	DirectoryID string           `json:"directoryId,omitempty"`
	Manifest    []string         `json:"manifest"`
	Files       []p115.ShareFile `json:"files"`
	Attempted   bool             `json:"attempted,omitempty"`
	Configured  bool             `json:"configured,omitempty"`
	Generated   *ShareRunResult  `json:"generated,omitempty"`
}

// A failed durable boundary must stop the worker, without saving over it.
type shareCheckpointError struct{ error }

func pendingDownloadShare(job OrganizeJob) bool {
	return job.Status == "completed" && job.Download != nil && job.Download.Share != nil && !job.Download.Share.HandedOff && (job.Download.Share.LeaderID == "" || job.Download.Share.LeaderID == job.ID)
}

func (s *DownloadOrganize) ShareSettings(accountID string) (AutomationSettings, error) {
	if s.Shares == nil {
		return AutomationSettings{}, errors.New("分享 STRM 服务尚未初始化")
	}
	return s.Shares.SnapshotDownloadShare(accountID)
}

func (s *ShareTasks) SnapshotDownloadShare(accountID string) (AutomationSettings, error) {
	if s.Settings == nil || s.Store == nil || s.OwnClient == nil || s.Generator == nil {
		return AutomationSettings{}, errors.New("分享 STRM 服务尚未初始化")
	}
	settings := s.Settings.ShareValue()
	if accountID == "" || settings.AccountID != accountID {
		return settings, errors.New("请先配置当前主账号的分享 STRM 设置")
	}
	if err := s.Settings.ValidateCurrentAccount(); err != nil {
		return settings, err
	}
	if err := ValidateGenerationOptions(s.Settings.root, settings.Generation); err != nil {
		return settings, fmt.Errorf("请先完善分享 STRM 设置：%w", err)
	}
	if !settings.Generation.Recursive {
		return settings, errors.New("请先在分享 STRM 设置中开启递归子目录")
	}
	return settings, nil
}

// The organize ID also identifies its one share task. Repeated handoffs only
// retrieve the existing record; share errors can never resubmit a download.
func (s *ShareTasks) EnqueueDownloadShare(ctx context.Context, job OrganizeJob, cookie string) error {
	if job.Download == nil || job.Download.Share == nil || job.Status != "completed" || len(job.Items) == 0 {
		return errors.New("整理任务尚未完成，不能交接分享")
	}
	share := job.Download.Share
	if share.Settings.AccountID != job.Config.AccountID || strings.TrimSpace(cookie) == "" {
		return errors.New("整理与分享的账号快照不一致")
	}
	s.enqueueMu.Lock()
	defer s.enqueueMu.Unlock()
	if _, err := s.Store.GetShareTask(ctx, job.ID); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("查询分享交接记录: %w", err)
	}
	download := &downloadShareRequest{ParentCID: job.Config.TargetCID, Directory: share.Directory, Files: make([]p115.ShareFile, 0, len(job.Items))}
	entries := make(map[string]bool, len(job.Items)*2)
	for _, item := range job.Items {
		if item.Status != "done" || item.Directory != share.Directory && !strings.HasPrefix(item.Directory, share.Directory+"/") {
			return errors.New("文件尚未全部整理到本次独立目录")
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(item.Directory, share.Directory), "/")
		prefix := ""
		if relative != "" {
			for _, name := range strings.Split(relative, "/") {
				prefix += "/" + strconv.Quote(name)
				entries[prefix+":true:0"] = true
			}
		}
		entries[prefix+"/"+strconv.Quote(item.Name)+":false:"+strconv.FormatInt(item.File.Size, 10)] = true
		f := item.File.ShareFile
		f.Name = item.Name
		download.Files = append(download.Files, f)
	}
	for entry := range entries {
		download.Manifest = append(download.Manifest, entry)
	}
	sort.Strings(download.Manifest)
	input := share.Settings.Generation
	input.Mode, input.URL, input.Password, input.CID, input.SharePath = "direct", "", "", "0", ""
	req := shareTaskRequest{Download: download, Input: input, Settings: share.Settings, CookieHash: cookieHash(cookie)}
	now := time.Now().UTC()
	task := model.ShareTask{ID: job.ID, Source: "磁力整理", Mode: "direct", Status: "queued", Note: path.Join(job.Config.TargetPath, share.Directory), CreatedAt: now, UpdatedAt: now, MaxRetries: share.Settings.ReviewRetryMax, RetryMinutes: share.Settings.ReviewRetryMinutes}
	if err := s.save(ctx, &task, req); err != nil {
		return err
	}
	select {
	case s.RetryWake <- struct{}{}:
	default:
	}
	return nil
}

func (s *ShareTasks) prepareDownloadShare(ctx context.Context, task *model.ShareTask, req *shareTaskRequest) (context.Context, error) {
	if s.OwnClient == nil {
		return ctx, errors.New("分享 STRM 服务尚未初始化")
	}
	d, cookie := req.Download, generationCookie(ctx, s.Cookie)
	if req.Input.URL == "" {
		if d.Attempted {
			return ctx, errors.New("创建分享的结果未确认，请核对 115 分享记录；不会重复创建分享")
		}
		// Resolve only this unpredictable wrapper, then verify its exact tree and
		// file identities. No broad destination scan or unrelated files are shared.
		roots, err := listDriveEntries(ctx, s.OwnClient, d.ParentCID, cookie)
		if err != nil {
			return ctx, err
		}
		rootID := ""
		for _, root := range roots {
			if root.Directory && root.Name == d.Directory {
				if rootID != "" {
					return ctx, errors.New("本次整理目录不唯一")
				}
				rootID = root.ID
			}
		}
		if rootID == "" {
			return ctx, errors.New("未找到本次整理结果目录，文件可能已被移动")
		}
		manifest, shareRoots, files, err := scanShareTree(ctx, rootID, "正在核对本次分享文件", func(ctx context.Context, id string, offset int) (*p115.SharePage, error) {
			return s.OwnClient.DriveFiles(ctx, id, offset, cookie)
		})
		if err != nil {
			return ctx, err
		}
		if !slices.Equal(manifest, d.Manifest) || !sameDownloadShareFiles(d.Files, files) {
			return ctx, errors.New("整理目录与所选文件不一致，未创建分享；请核对目录")
		}
		d.DirectoryID = rootID
		d.Attempted = true
		if err := s.save(ctx, task, *req); err != nil {
			return ctx, &shareCheckpointError{err}
		}
		own, err := s.OwnClient.SendShare(ctx, fileIDs(shareRoots), cookie)
		if err != nil {
			return ctx, fmt.Errorf("创建分享结果未确认，请核对 115 分享记录: %w", err)
		}
		if own == nil || own.Code == "" {
			return ctx, errors.New("115 未返回有效分享，结果未确认，请核对分享记录")
		}
		req.Input.URL = "https://115cdn.com/s/" + url.PathEscape(own.Code)
		// Preserve the received password until ConfigureShare confirms the saved one.
		req.Input.Password = own.Password
		task.ShareURL = req.Input.URL
		if err := s.save(ctx, task, *req); err != nil {
			return ctx, &shareCheckpointError{err}
		}
	}
	share, err := p115.ParseShareURL(req.Input.URL)
	if err != nil {
		return ctx, err
	}
	share.Password = req.Input.Password
	if !d.Configured {
		if err = s.OwnClient.ConfigureShare(ctx, share, req.Settings.FixedSharePassword, cookie); err != nil {
			return ctx, fmt.Errorf("设置分享有效期与提取码: %w", err)
		}
		d.Configured = true
		req.Input.Password = share.Password
		task.ShareURL = req.Input.URL
		if share.Password != "" {
			task.ShareURL += "?password=" + url.QueryEscape(share.Password)
		}
		if err := s.save(ctx, task, *req); err != nil {
			return ctx, &shareCheckpointError{err}
		}
	}
	manifest, snapshot, err := captureShareSnapshot(ctx, s.OwnClient, share, "0", cookie)
	if err != nil {
		return ctx, err
	}
	// This is our own share, read with the pinned owner cookie. As in the
	// reshare workflow, a readable complete snapshot can generate immediately;
	// its public UnderReview flag is not a generation prerequisite.
	if !slices.Equal(manifest, d.Manifest) || !sameDownloadShareFiles(d.Files, snapshot.Files) {
		return ctx, p115.ErrShareSnapshotPending
	}
	return context.WithValue(ctx, generationSnapshotKey{}, snapshot), nil
}

// The durable generation receipt lets retries recycle only the exact task
// directory, including after a restart or a lost deletion response.
func (s *ShareTasks) cleanupDownloadShare(ctx context.Context, task *model.ShareTask, req *shareTaskRequest, result *GenerationResult) error {
	d := req.Download
	id, err := strconv.ParseUint(d.DirectoryID, 10, 64)
	if err != nil || id == 0 || d.DirectoryID == d.ParentCID {
		return errors.New("STRM 已生成，但缺少有效的本次整理目录 ID，未执行回收")
	}
	if d.Generated == nil {
		result.Mode, result.ReshareURL, result.TempDirectoryID = "direct", task.ShareURL, d.DirectoryID
		result.Cleaned = false
		d.Generated = summarizeGenerationResult(result)
		task.Written, task.Total = result.Written, len(result.Files)
		if err := s.save(ctx, task, *req); err != nil {
			return &shareCheckpointError{err}
		}
	}
	if d.Generated.Cleaned {
		return nil
	}
	if s.OwnClient == nil {
		return errors.New("STRM 已生成，目录尚未回收：分享 STRM 服务尚未初始化")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reportGeneration(ctx, "deleting", "STRM 与元数据已生成，正在将本次整理目录移入回收站")
	if err := s.OwnClient.DeleteDirectory(ctx, d.DirectoryID, generationCookie(ctx, s.Cookie)); err != nil {
		return fmt.Errorf("STRM 已生成，但本次整理目录回收未确认；重试仅清理此目录: %w", err)
	}
	d.Generated.Cleaned, result.Cleaned = true, true
	task.Note = "STRM 已生成，本次整理目录已移入回收站：" + d.Directory
	return nil
}

func sameDownloadShareFiles(expected, actual []p115.ShareFile) bool {
	if len(expected) != len(actual) {
		return false
	}
	byID := make(map[string]p115.ShareFile, len(expected))
	for _, file := range expected {
		byID[file.ID] = file
	}
	for _, file := range actual {
		want, ok := byID[file.ID]
		if !ok || want.Name != file.Name || want.Size != file.Size || file.Directory {
			return false
		}
		delete(byID, file.ID)
	}
	return len(byID) == 0
}
