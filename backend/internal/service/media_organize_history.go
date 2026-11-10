package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"strm115/backend/internal/storage"
)

type MediaOrganizeResult struct {
	Status  string    `json:"status"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
	Target  string    `json:"target,omitempty"`
	Action  string    `json:"action,omitempty"`
}
type MediaOrganizeHistoryItem struct {
	RunID           string `json:"runId"`
	FileID          string `json:"fileId"`
	Name            string `json:"name"`
	Title           string `json:"title"`
	Kind            string `json:"kind"`
	MediaType       string `json:"mediaType"`
	Season          int    `json:"season"`
	Episode         int    `json:"episode"`
	Source          string `json:"source"`
	AttachmentCount int    `json:"attachmentCount"`
	CanDelete       bool   `json:"canDelete"`
	CanRetry        bool   `json:"canRetry"`
	MediaOrganizeResult
}
type MediaOrganizeHistory struct {
	Items    []MediaOrganizeHistoryItem `json:"items"`
	Page     int                        `json:"page"`
	Pages    int                        `json:"pages"`
	Total    int                        `json:"total"`
	Counts   map[string]int             `json:"counts"`
	Active   bool                       `json:"active"`
	Activity []MediaOrganizeActivity    `json:"activity"`
	Cleanup  []MediaOrganizeCleanup     `json:"cleanup"`
}
type MediaOrganizeCleanup struct {
	RunID       string    `json:"runId"`
	RuleName    string    `json:"ruleName"`
	Files       int       `json:"files"`
	Directories int       `json:"directories"`
	Failed      int       `json:"failed"`
	At          time.Time `json:"at"`
}
type MediaOrganizeState struct {
	StoppingRules []string                              `json:"stoppingRules"`
	Rules         []MediaOrganizeRule                   `json:"rules"`
	Active        bool                                  `json:"active"`
	ActiveRules   []string                              `json:"activeRules"`
	Monitoring    map[string]MediaOrganizeMonitorStatus `json:"monitoring"`
}
type MediaOrganizeActivity struct {
	RunID    string    `json:"runId"`
	RuleName string    `json:"ruleName"`
	Status   string    `json:"status"`
	Message  string    `json:"message,omitempty"`
	At       time.Time `json:"at"`
}
type MediaOrganizeDeleteItem struct {
	RunID  string `json:"runId"`
	FileID string `json:"fileId"`
}
type MediaOrganizeDelete struct {
	Items  []MediaOrganizeDeleteItem `json:"items"`
	Status string                    `json:"status,omitempty"`
}

func mediaStatusGroup(status string) string {
	if status == "done" {
		return "done"
	}
	if mediaRunActive(status) {
		return "running"
	}
	return "failed"
}

type MediaOrganizeTask struct {
	MediaOrganizeHistoryItem
	Rule    MediaOrganizeRule    `json:"rule"`
	Group   MediaOrganizeGroup   `json:"group"`
	Entries []MediaOrganizeEntry `json:"entries"`
}

func mediaInTask(e MediaOrganizeEntry, fileID string) bool {
	return fileID == "" || e.File.ID == fileID || e.VideoID == fileID
}

func mediaInRunTask(run *MediaOrganizeRun, entry MediaOrganizeEntry) bool {
	return mediaInTask(entry, run.TaskFileID) && (run.processing == nil || run.processing[entry.File.ID])
}
func mediaRunActive(status string) bool {
	return status == "queued" || status == "scanning" || status == "running"
}

type mediaTaskPermission struct {
	CanDelete bool
	CanRetry  bool
}

func (s *MediaOrganizer) taskPermissionsLocked(run MediaOrganizeRun) map[string]mediaTaskPermission {
	busy := s.busyRunLocked(run.ID) || mediaRunActive(run.Status)
	out := make(map[string]mediaTaskPermission)
	// 来源补扫接管前保留中断恢复证据，不能作为普通失败历史删除或重试。
	if s.eventRecoveryPendingLocked(run.ID) {
		return out
	}
	for _, entry := range run.Entries {
		if entry.File.MediaType == "video" {
			out[entry.File.ID] = mediaTaskPermission{CanDelete: s.inFlight[run.ID][entry.File.ID] == 0}
		}
	}
	if busy {
		return out
	}
	for _, entry := range run.Entries {
		if entry.Status == "done" || entry.Status == "retained" || entry.File.MediaType == "cleanup" {
			continue
		}
		owner := entry.VideoID
		if entry.File.MediaType == "video" {
			owner = entry.File.ID
		}
		if permission, ok := out[owner]; ok {
			permission.CanRetry = true
			out[owner] = permission
		}
	}
	return out
}

// 历史结果只取决于主文件；查看记录不读取网盘或 TMDB。
func mediaTasks(run MediaOrganizeRun) []MediaOrganizeTask {
	groups := make(map[string]MediaOrganizeGroup, len(run.Groups))
	for _, g := range run.Groups {
		groups[g.ID] = g
	}
	companions := make(map[string][]MediaOrganizeEntry, len(run.Entries))
	for _, e := range run.Entries {
		if e.VideoID != "" && e.File.MediaType != "cleanup" {
			companions[e.VideoID] = append(companions[e.VideoID], e)
		}
	}
	tasks := make([]MediaOrganizeTask, 0, len(run.Entries))
	for _, e := range run.Entries {
		completed, hasCompletion := mediaPrimaryCompletion(e)
		if e.File.MediaType != "video" || e.Reference && !hasCompletion {
			continue
		}
		g := groups[e.Group]
		entries := append([]MediaOrganizeEntry{e}, companions[e.File.ID]...)
		at := e.UpdatedAt
		if at.IsZero() {
			at = run.CreatedAt
		}
		result := MediaOrganizeResult{Status: "done", At: at, Target: e.Target}
		if e.Result != nil {
			result = *e.Result
		}
		if result.At.IsZero() {
			result.At = at
		}
		switch {
		case e.Status == "done":
			if hasCompletion {
				result = completed
			}
			result.Status, result.Message = "done", ""
			if result.Action == "duplicate" {
				result.Message = "SHA1 一致，已删除来源重复文件"
			}
		case mediaRunActive(run.Status) && (run.TaskFileID == e.File.ID || run.TaskFileID == "" && (e.Status == "pending" || e.Status == "ready")):
			result.Status, result.Message = "running", e.Message
			if run.Status == "queued" {
				result.Message = "已排队，等待整理"
			}
		default:
			result.Status = "failed"
			if e.Message != "" {
				result.Message = e.Message
			}
			if result.Message == "" && g.Message != "" {
				result.Message = g.Message
			}
		}
		if result.Status == "failed" && result.Message == "" {
			result.Message = "文件暂未处理完成"
		}
		source := run.Rule.Source
		if run.Rule.Storage == "local" {
			source = filepath.ToSlash(source)
			result.Target = filepath.ToSlash(result.Target)
		}
		item := MediaOrganizeHistoryItem{RunID: run.ID, FileID: e.File.ID, Name: e.File.Name, Title: g.Title, Kind: g.Kind, MediaType: e.File.MediaType, Season: e.File.Season, Episode: e.File.Episode, Source: path.Join(source, e.File.SourcePath), AttachmentCount: len(entries) - 1, MediaOrganizeResult: result}
		tasks = append(tasks, MediaOrganizeTask{MediaOrganizeHistoryItem: item, Rule: run.Rule, Group: g, Entries: entries})
	}
	return tasks
}

func (s *MediaOrganizer) busyRunLocked(id string) bool { return s.active == id || s.actions[id] != "" }

func (s *MediaOrganizer) Task(id, fileID string) (MediaOrganizeTask, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.TaskContext(ctx, id, fileID)
}

func (s *MediaOrganizer) TaskContext(ctx context.Context, id, fileID string) (MediaOrganizeTask, error) {
	if id == "" || fileID == "" {
		return MediaOrganizeTask{}, ErrMediaOrganizeInput
	}
	s.mu.Lock()
	for _, run := range s.doc.Runs {
		if run.ID != id {
			continue
		}
		task, err := s.mediaTaskLocked(run, fileID)
		s.mu.Unlock()
		return task, err
	}
	s.mu.Unlock()
	// 历史详情只解码选中视频及附件，数据库慢读不占业务锁。
	raw, err := s.db.LoadMediaOrganizeTask(ctx, id, fileID)
	if errors.Is(err, sql.ErrNoRows) {
		return MediaOrganizeTask{}, ErrMediaOrganizeTaskNotFound
	}
	if err != nil {
		return MediaOrganizeTask{}, fmt.Errorf("读取整理详情: %w", err)
	}
	run, err := mediaDecodeStoredRun(raw)
	if err != nil {
		return MediaOrganizeTask{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, live := range s.doc.Runs {
		if live.ID == id {
			return s.mediaTaskLocked(live, fileID)
		}
	}
	return s.mediaTaskLocked(run, fileID)
}

func (s *MediaOrganizer) mediaTaskLocked(run MediaOrganizeRun, fileID string) (MediaOrganizeTask, error) {
	selected := run
	selected.Entries = make([]MediaOrganizeEntry, 0, 1)
	for _, entry := range run.Entries {
		if mediaInTask(entry, fileID) {
			selected.Entries = append(selected.Entries, entry)
		}
	}
	permissions := s.taskPermissionsLocked(selected)
	var snapshot MediaOrganizeRun
	cloneMediaRun(selected, &snapshot)
	for _, task := range mediaTasks(snapshot) {
		if task.FileID == fileID {
			permission := permissions[fileID]
			task.CanDelete, task.CanRetry = permission.CanDelete, permission.CanRetry
			return task, nil
		}
	}
	return MediaOrganizeTask{}, ErrMediaOrganizeTaskNotFound
}

func (s *MediaOrganizer) State() MediaOrganizeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := MediaOrganizeState{Rules: append([]MediaOrganizeRule{}, s.doc.Rules...), Active: s.active != "" || len(s.actions) > 0, ActiveRules: []string{}}
	state.Monitoring = make(map[string]MediaOrganizeMonitorStatus, len(s.localMonitors))
	for _, rule := range s.doc.Rules {
		if rule.ScheduleEnabled && rule.Storage != "local" {
			state.Monitoring[rule.ID] = MediaOrganizeMonitorStatus{Mode: "polling"}
		}
		if rule.RealtimeEnabled && rule.Storage == "local" {
			if local := s.localMonitors[rule.ID]; local != nil && mediaSameWatchRule(local.rule, rule) {
				state.Monitoring[rule.ID] = local.status
			}
		}
	}
	for id, cloud := range s.cloudMonitors {
		if cloud.rule.Storage == "115" && cloud.rule.RealtimeEnabled {
			state.Monitoring[id] = cloud.status
		}
	}
	for _, run := range s.doc.Runs {
		if s.stopping[run.ID] {
			state.StoppingRules = append(state.StoppingRules, run.Rule.ID)
		}
		if mediaRunActive(run.Status) || s.busyRunLocked(run.ID) {
			state.ActiveRules = append(state.ActiveRules, run.Rule.ID)
		}
	}
	return state
}

func mediaRunCleanup(run MediaOrganizeRun) MediaOrganizeCleanup {
	out := MediaOrganizeCleanup{RunID: run.ID, RuleName: run.Rule.Name, At: run.UpdatedAt}
	for _, e := range run.Entries {
		if e.File.MediaType == "video" || e.VideoID != "" || e.Result == nil || e.Target != "" || e.Failure != nil {
			continue
		}
		if e.Status != "done" {
			if !mediaRunActive(run.Status) {
				out.Failed++
			}
		} else if e.File.Directory {
			out.Directories++
		} else {
			out.Files++
		}
	}
	return out
}

func (s *MediaOrganizer) History(ctx context.Context, query, status string, page int) (MediaOrganizeHistory, error) {
	if status == "" {
		status = "all"
	}
	if !slices.Contains([]string{"all", "running", "done", "failed"}, status) || len(query) > 500 {
		return MediaOrganizeHistory{}, ErrMediaOrganizeInput
	}
	out := MediaOrganizeHistory{Items: []MediaOrganizeHistoryItem{}, Counts: map[string]int{"all": 0, "running": 0, "done": 0, "failed": 0}, Activity: []MediaOrganizeActivity{}, Cleanup: []MediaOrganizeCleanup{}}
	live, err := s.liveHistoryRows(ctx)
	if err != nil {
		return out, err
	}
	rows, err := s.db.QueryMediaOrganizeLive(ctx, strings.TrimSpace(query), status, page, live)
	if err != nil {
		return out, fmt.Errorf("查询整理记录: %w", err)
	}
	out.Page, out.Pages, out.Total, out.Counts = rows.Page, rows.Pages, rows.Total, rows.Counts
	for _, raw := range rows.Records {
		var item MediaOrganizeHistoryItem
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	for _, raw := range rows.Activity {
		var item MediaOrganizeActivity
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return out, err
		}
		out.Activity = append(out.Activity, item)
	}
	for _, raw := range rows.Cleanup {
		var item MediaOrganizeCleanup
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return out, err
		}
		out.Cleanup = append(out.Cleanup, item)
	}
	// 已结束的成功历史不需要执行权限上下文；失败仅按本页主文件读取必要条目。
	permissions := make(map[string]mediaTaskPermission, len(out.Items))
	for _, item := range out.Items {
		permission := mediaTaskPermission{CanDelete: true}
		if item.Status == "failed" {
			task, err := s.TaskContext(ctx, item.RunID, item.FileID)
			if err != nil && !errors.Is(err, ErrMediaOrganizeTaskNotFound) {
				return out, err
			}
			permission = mediaTaskPermission{CanDelete: task.CanDelete, CanRetry: task.CanRetry}
		}
		permissions[item.RunID+"\x00"+item.FileID] = permission
	}
	// 数据库查询不占业务锁；操作权限在返回前按最新的在途文件重新计算。
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Active = s.active != "" || len(s.actions) > 0
	for i := range out.Items {
		item := &out.Items[i]
		permission := permissions[item.RunID+"\x00"+item.FileID]
		permission.CanDelete = permission.CanDelete && s.inFlight[item.RunID][item.FileID] == 0 && !s.eventRecoveryPendingLocked(item.RunID)
		if s.busyRunLocked(item.RunID) || s.eventRecoveryPendingLocked(item.RunID) {
			permission.CanRetry = false
		}
		item.CanDelete, item.CanRetry = permission.CanDelete, permission.CanRetry
	}
	return out, nil
}

// 只在复制运行摘要时持锁，慢查询或取消请求不阻塞整理检查点。
func (s *MediaOrganizer) liveHistoryRows(ctx context.Context) ([]storage.MediaOrganizeLiveTaskRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	live := make([]storage.MediaOrganizeLiveTaskRow, 0)
	for _, run := range s.doc.Runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !mediaRunActive(run.Status) {
			continue
		}
		for _, task := range mediaTasks(run) {
			if task.Status != "done" && task.Status != "running" {
				continue
			}
			row, err := mediaHistoryTaskRow(task.MediaOrganizeHistoryItem)
			if err != nil {
				return nil, err
			}
			live = append(live, storage.MediaOrganizeLiveTaskRow{RunID: run.ID, MediaOrganizeTaskRow: row})
		}
	}
	return live, ctx.Err()
}

// 删除只移除记录和排队动作；实际文件操作进行时暂时保护对应主文件。
func (s *MediaOrganizer) DeleteHistory(ctx context.Context, input MediaOrganizeDelete) (int, error) {
	if input.Status != "" && input.Status != "done" && input.Status != "failed" {
		return 0, ErrMediaOrganizeInput
	}
	if (input.Status != "") == (len(input.Items) > 0) || len(input.Items) > 20 {
		return 0, ErrMediaOrganizeInput
	}
	selected := make(map[string]map[string]bool)
	for _, item := range input.Items {
		if item.RunID == "" || item.FileID == "" {
			return 0, ErrMediaOrganizeInput
		}
		if selected[item.RunID] == nil {
			selected[item.RunID] = map[string]bool{}
		}
		selected[item.RunID][item.FileID] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	defer s.releaseHistoryLocked()
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	if err := s.loadHistoryLocked(ctx, ids, "", input.Status); err != nil {
		return 0, err
	}
	// 先校验整个请求，避免部分删除后才发现选中了受保护任务。
	for _, run := range s.doc.Runs {
		if selected[run.ID] != nil {
			permissions := s.taskPermissionsLocked(run)
			for _, entry := range run.Entries {
				if entry.File.MediaType == "video" && selected[run.ID][entry.File.ID] && !permissions[entry.File.ID].CanDelete {
					return 0, ErrMediaOrganizeBusy
				}
			}
		}
	}
	deleted := 0
	changed := []string{}
	cancelled := []string{}
	runs := make([]MediaOrganizeRun, 0, len(s.doc.Runs))
	for _, original := range s.doc.Runs {
		ids := selected[original.ID]
		if input.Status != "" {
			ids = map[string]bool{}
			permissions := s.taskPermissionsLocked(original)
			for _, task := range mediaTasks(original) {
				if task.Status == input.Status && permissions[task.FileID].CanDelete {
					ids[task.FileID] = true
				}
			}
		}
		if len(ids) == 0 {
			runs = append(runs, original)
			continue
		}
		valid := map[string]bool{}
		for _, entry := range original.Entries {
			if entry.File.MediaType == "video" && ids[entry.File.ID] {
				valid[entry.File.ID] = true
			}
		}
		if len(valid) == 0 {
			runs = append(runs, original)
			continue
		}
		var run MediaOrganizeRun
		cloneMediaRun(original, &run)
		run.Entries = slices.DeleteFunc(run.Entries, func(e MediaOrganizeEntry) bool { return valid[e.File.ID] || valid[e.VideoID] })
		used := map[string]bool{}
		hasVideo := false
		for _, entry := range run.Entries {
			used[entry.Group] = true
			if entry.File.MediaType == "video" {
				hasVideo = true
			}
		}
		run.Groups = slices.DeleteFunc(run.Groups, func(g MediaOrganizeGroup) bool { return !used[g.ID] })
		if valid[run.TaskFileID] {
			run.TaskFileID = ""
			if s.actions[run.ID] == "retry" && s.active != run.ID {
				cancelled = append(cancelled, run.ID)
				mediaFinishRun(&run)
			}
		}
		deleted += len(valid)
		changed = append(changed, run.ID)
		if hasVideo || s.active == run.ID || s.actions[run.ID] == "auto" {
			runs = append(runs, run)
		}
	}
	if deleted == 0 {
		return 0, nil
	}
	s.doc.Runs = runs
	if err := s.persistSQLiteLockedContext(ctx, changed); err != nil {
		return 0, err
	}
	for _, id := range cancelled {
		s.removeActionLocked(id)
	}
	return deleted, nil
}
