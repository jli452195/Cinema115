package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"strm115/backend/internal/client/emby"
)

const embyRefreshPendingLimit = 5000

// 排队时固定连接身份，配置修改不能把旧路径发送到另一台服务器。
type embyRefreshTarget struct{ URL, APIKey string }

// 单独保存刷新设置，不连接 Emby，也不修改连接或同步范围。
func (s *EmbyService) SaveMediaRefreshConfig(ctx context.Context, enabled bool, mappings string) (resultErr error) {
	ctx, finish := s.startOperation(ctx, "保存 Emby 刷新设置")
	defer func() { finish(resultErr) }()
	mappings = strings.TrimSpace(mappings)
	if _, err := parseEmbyRefreshMappings(mappings); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("保存 Emby 刷新设置取消: %w", err)
	}
	c := s.config
	if enabled && c.APIKey == "" {
		return fmt.Errorf("请先配置 Emby 连接")
	}
	c.RefreshAfterOrganize, c.RefreshPathMappings = enabled, mappings
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("编码 Emby 刷新设置: %w", err)
	}
	if err := s.secrets.Save(string(data)); err != nil {
		return fmt.Errorf("保存 Emby 刷新设置: %w", err)
	}
	s.config = c
	return nil
}

func embyRefreshPath(raw string) (string, error) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
	windows := len(raw) >= 3 && raw[1] == ':' && raw[2] == '/' && (raw[0] >= 'A' && raw[0] <= 'Z' || raw[0] >= 'a' && raw[0] <= 'z')
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\x00\r\n") || !strings.HasPrefix(raw, "/") && !windows {
		return "", fmt.Errorf("请填写有效的绝对文件路径")
	}
	clean := path.Clean(raw)
	if windows && len(clean) == 2 {
		clean += "/"
	}
	if strings.HasPrefix(raw, "//") {
		clean = "/" + clean
	}
	return clean, nil
}

func parseEmbyRefreshMappings(raw string) ([][2]string, error) {
	if len(raw) > 16384 {
		return nil, fmt.Errorf("路径替换配置过长")
	}
	var mappings [][2]string
	for i, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		from, to, ok := strings.Cut(line, "=>")
		from, fromErr := embyRefreshPath(from)
		to, toErr := embyRefreshPath(to)
		if !ok || strings.Contains(to, "=>") || fromErr != nil || toErr != nil {
			return nil, fmt.Errorf("路径替换第 %d 行应为：整理路径 => Emby 路径，两端须为绝对路径", i+1)
		}
		mappings = append(mappings, [2]string{from, to})
	}
	return mappings, nil
}

func embyMappedRefreshPath(raw string, mappings [][2]string) (string, error) {
	value, err := embyRefreshPath(raw)
	if err != nil {
		return "", err
	}
	for _, mapping := range mappings {
		prefix := strings.TrimRight(mapping[0], "/")
		if value == mapping[0] {
			return mapping[1], nil
		}
		if strings.HasPrefix(value, prefix+"/") {
			return strings.TrimRight(mapping[1], "/") + strings.TrimPrefix(value, prefix), nil
		}
	}
	return value, nil
}

// QueueMediaRefresh 仅做内存去重，不访问网络或数据库，也不为每个文件启动协程。
func (s *EmbyService) QueueMediaRefresh(paths []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.config
	if !c.RefreshAfterOrganize || c.APIKey == "" || len(paths) == 0 {
		return
	}
	mappings, err := parseEmbyRefreshMappings(c.RefreshPathMappings)
	if err != nil {
		return
	}
	target := embyRefreshTarget{c.URL, c.APIKey}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshPending == nil {
		s.refreshPending = make(map[embyRefreshTarget]map[string]struct{})
	}
	if s.refreshPending[target] == nil {
		s.refreshPending[target] = make(map[string]struct{}, min(len(paths), embyRefreshPendingLimit))
	}
	for _, raw := range paths {
		value, err := embyMappedRefreshPath(raw, mappings)
		if err != nil {
			s.refreshDropped++
			continue
		}
		if _, exists := s.refreshPending[target][value]; exists {
			continue
		}
		if s.refreshCount >= embyRefreshPendingLimit {
			s.refreshDropped++
			continue
		}
		s.refreshPending[target][value] = struct{}{}
		s.refreshCount++
	}
	select {
	case s.refreshWake <- struct{}{}:
	default:
	}
}

func (s *EmbyService) RunMediaRefresh(ctx context.Context) {
	s.runMediaRefresh(ctx, 5*time.Second)
}

func (s *EmbyService) runMediaRefresh(ctx context.Context, window time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.refreshWake:
		}
		// 固定窗口不随新文件延长，持续整理时也能及时通知。
		if !embyRefreshWait(ctx, window) {
			return
		}
		s.refreshMu.Lock()
		pending, dropped := s.refreshPending, s.refreshDropped
		s.refreshPending, s.refreshCount, s.refreshDropped = nil, 0, 0
		// 本批次期间的唤醒已包含在 pending 中；发送时的新路径会再次唤醒。
		select {
		case <-s.refreshWake:
		default:
		}
		s.refreshMu.Unlock()
		if dropped > 0 {
			logEmby(s.logContext(ctx, s.snapshot(), "EMBY-REFRESH", "整理后通知"), "WARNING", fmt.Sprintf("跳过 %d 条路径通知：路径无效或待发送队列已达上限", dropped))
		}
		for target, set := range pending {
			paths := make([]string, 0, len(set))
			for value := range set {
				paths = append(paths, value)
			}
			// 整个窗口一起匹配和去重，避免分块后对同一媒体库重复刷新。
			s.sendMediaRefresh(ctx, target, paths, window)
			if ctx.Err() != nil {
				return
			}
		}
	}
}

func embyRefreshWait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *EmbyService) sendMediaRefresh(ctx context.Context, target embyRefreshTarget, paths []string, retryDelay time.Duration) {
	c := EmbyConfig{URL: target.URL, APIKey: target.APIKey}
	logCtx := s.logContext(ctx, c, s.newLogTask(), "整理后刷新")
	client := emby.New(c.URL, c.APIKey)
	var libraries []emby.Library
	// 读取当前服务器全部媒体库，不依赖本地索引或同步页的勾选范围。
	err := s.retryMediaRefresh(ctx, target, retryDelay, func(requestCtx context.Context) error {
		var err error
		libraries, err = client.Libraries(requestCtx)
		return err
	})
	if err != nil {
		if s.mediaRefreshEnabled(ctx, target) {
			logEmby(logCtx, "WARNING", "读取 Emby 媒体库失败，本批未提交刷新，整理结果不受影响："+embyLogError(err))
		}
		return
	}
	targets, unmatched := embyRefreshLibraryTargets(paths, libraries)
	if len(unmatched) > 0 {
		sort.Strings(unmatched)
		logEmby(logCtx, "WARNING", fmt.Sprintf("%d 条文件路径未匹配到 Emby 媒体库，已跳过刷新；请检查路径替换及 Emby 媒体目录，例如：%q", len(unmatched), unmatched[0]))
	}
	ids := make([]string, 0, len(targets))
	for id := range targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		library := targets[id]
		err := s.retryMediaRefresh(ctx, target, retryDelay, func(requestCtx context.Context) error {
			return client.RefreshLibrary(requestCtx, id)
		})
		if !s.mediaRefreshEnabled(ctx, target) {
			return
		}
		if err != nil {
			logEmby(logCtx, "WARNING", fmt.Sprintf("媒体库 %q（ID %s，%d 条文件路径）刷新请求失败，整理结果不受影响：%s", library.Name, id, library.Count, embyLogError(err)))
			continue
		}
		logEmby(logCtx, "INFO", fmt.Sprintf("已向 Emby 提交媒体库 %q（ID %s）的刷新请求，合并 %d 条文件路径；入库仍需等待 Emby 扫描和刮削", library.Name, id, library.Count))
	}
}

type embyRefreshLibrary struct {
	Name  string
	Count int
}

// 按目录边界选择最具体的媒体目录；多个库共用同一路径时分别刷新。
func embyRefreshLibraryTargets(paths []string, libraries []emby.Library) (map[string]embyRefreshLibrary, []string) {
	type directory struct{ path, id, name string }
	directories := make([]directory, 0, len(libraries))
	for _, library := range libraries {
		if strings.TrimSpace(library.ID) == "" {
			continue
		}
		for _, raw := range library.Locations {
			value, err := embyRefreshPath(raw)
			if err == nil {
				directories = append(directories, directory{strings.TrimRight(value, "/"), library.ID, library.Name})
			}
		}
	}
	targets := make(map[string]embyRefreshLibrary)
	unmatched := make([]string, 0)
	matched := make(map[string]string, len(libraries))
	for _, value := range paths {
		longest := -1
		clear(matched)
		for _, dir := range directories {
			if value != dir.path && !strings.HasPrefix(value, dir.path+"/") {
				continue
			}
			if len(dir.path) > longest {
				clear(matched)
				longest = len(dir.path)
			}
			if len(dir.path) == longest {
				matched[dir.id] = dir.name
			}
		}
		if len(matched) == 0 {
			unmatched = append(unmatched, value)
		}
		for id, name := range matched {
			library := targets[id]
			library.Name = name
			library.Count++
			targets[id] = library
		}
	}
	return targets, unmatched
}

func (s *EmbyService) mediaRefreshEnabled(ctx context.Context, target embyRefreshTarget) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ctx.Err() == nil && s.config.RefreshAfterOrganize && s.config.URL == target.URL && s.config.APIKey == target.APIKey
}

func (s *EmbyService) retryMediaRefresh(ctx context.Context, target embyRefreshTarget, retryDelay time.Duration, request func(context.Context) error) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		// 关闭开关或更换连接后丢弃待发通知，绝不把旧路径改发到新目标。
		if !s.mediaRefreshEnabled(ctx, target) {
			return context.Canceled
		}
		requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = request(requestCtx)
		cancel()
		if err == nil {
			return nil
		}
		var httpErr *emby.HTTPError
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == 1 || errors.As(err, &httpErr) && httpErr.StatusCode < 500 && httpErr.StatusCode != 429 {
			break
		}
		if !embyRefreshWait(ctx, retryDelay) {
			return ctx.Err()
		}
	}
	return err
}
