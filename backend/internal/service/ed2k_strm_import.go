package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"strm115/backend/internal/strm"
)

// ImportTo 对内容只登记一次，对每次明确输出建立独立请求；同一请求重放不补写文件。
func (s *ED2KSTRMService) ImportTo(ctx context.Context, targetID string, links []string, requestID string) (model.ED2KSTRMImportResult, error) {
	return s.importTo(ctx, targetID, nil, links, requestID)
}

// ImportToTarget 将入口已选定的目录和分配与入队原子核对，防止配置变化使批次改投其他目录。
func (s *ED2KSTRMService) ImportToTarget(ctx context.Context, target model.ED2KSTRMTarget, links []string, requestID string) (model.ED2KSTRMImportResult, error) {
	return s.importTo(ctx, target.ID, &target, links, requestID)
}

func (s *ED2KSTRMService) importTo(ctx context.Context, targetID string, expected *model.ED2KSTRMTarget, links []string, requestID string) (model.ED2KSTRMImportResult, error) {
	out := model.ED2KSTRMImportResult{Resources: make([]model.ED2KSTRMResource, 0), Tasks: make([]model.ED2KSTRMTask, 0)}
	if len(links) == 0 {
		return out, generationInputError("请提供至少一条 ED2K 链接")
	}
	if requestID == "" || len(requestID) > 128 || strings.IndexFunc(requestID, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return out, generationInputError("请提供有效的输出请求编号")
	}
	claims := make([]strm.ED2K, 0, len(links))
	fingerprintParts := make([]string, 0, len(links)+1)
	fingerprintParts = append(fingerprintParts, targetID)
	for _, raw := range links {
		v, err := parseED2KImport(raw)
		if err != nil {
			return out, err
		}
		claims = append(claims, v)
		fingerprintParts = append(fingerprintParts, ed2kResourceKey(v.Hash, v.Size)+"/"+v.Name)
	}
	requestID = DirectLinkKey("ed2k-output-request", requestID)
	fingerprint := DirectLinkKey(fingerprintParts...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != nil {
		current, ok := s.targets[targetID]
		if !ok || current != *expected {
			return out, generationInputError("STRM 输出目录或 Bot 分配已变化，请重新提交")
		}
	}
	for _, t := range s.tasks {
		if t.RequestID != requestID {
			continue
		}
		if t.Fingerprint != fingerprint {
			return out, generationInputError("输出请求编号已被其他内容或目录使用，请重新提交")
		}
		out.Tasks = append(out.Tasks, t)
		if v, ok := s.resources[ed2kResourceKey(t.Hash, t.Size)]; ok {
			out.Resources = append(out.Resources, v)
		}
	}
	if len(out.Tasks) > 0 {
		return out, nil
	}
	target, ok := s.targets[targetID]
	if !ok {
		return out, generationInputError("请选择有效的 STRM 输出目录")
	}
	settings, err := s.Store.GetED2KSTRMSettings(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		return out, generationInputError("请先保存 ED2K STRM 服务地址")
	}
	if err != nil {
		return out, err
	}
	a, err := s.account()
	if err != nil {
		return out, err
	}
	if err := validateED2KCacheSettings(settings, a); err != nil {
		return out, err
	}
	values := make(map[string]model.ED2KSTRMResource, len(claims))
	created := make(map[string]bool, len(claims))
	seen := make(map[string]bool, len(claims))
	now := time.Now().UTC()
	for _, claim := range claims {
		key := ed2kResourceKey(claim.Hash, claim.Size)
		v, exists := values[key]
		if !exists {
			v, exists = s.resources[key]
			v.Aliases = slices.Clone(v.Aliases)
		}
		if exists && (v.AccountID != a.ID || v.UID != a.UID) {
			return model.ED2KSTRMImportResult{}, generationInputError("该资源属于原主账号，请先处理账号变更，不能自动转移缓存或任务")
		}
		if !exists {
			v = model.ED2KSTRMResource{Hash: claim.Hash, Size: claim.Size, Name: claim.Name, AccountID: a.ID, UID: a.UID, AccountRevision: a.Revision, CredentialScope: ed2kCredentialScope(a), Status: "queued", CacheState: "unknown", Stage: "locate", Message: "已排队，等待核验缓存资料", CreatedAt: now, UpdatedAt: now}
			v.CacheTargetCID, v.CacheTargetPath = settings.CacheCID, settings.CachePath
			created[key] = true
		}
		if claim.Name != v.Name && !slices.Contains(v.Aliases, claim.Name) && len(v.Aliases) < 16 {
			v.Aliases = append(v.Aliases, claim.Name)
		}
		values[key] = v
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Tasks = append(out.Tasks, model.ED2KSTRMTask{ID: DirectLinkKey("ed2k-output", requestID, key), RequestID: requestID, Fingerprint: fingerprint, Hash: v.Hash, Size: v.Size, Name: claim.Name, TargetID: target.ID, TargetName: target.Name, OutputDir: target.OutputDir, BaseURL: settings.BaseURL, Status: "queued", CreatedAt: now, UpdatedAt: now})
	}
	for _, v := range values {
		out.Resources = append(out.Resources, v)
	}
	if err := s.Store.SaveED2KSTRMImport(ctx, out.Resources, out.Tasks); err != nil {
		return model.ED2KSTRMImportResult{}, err
	}
	for key, v := range values {
		s.resources[key] = v
		if created[key] {
			s.pinned[key] = a
		}
	}
	for _, t := range out.Tasks {
		s.tasks[t.ID] = t
	}
	s.notify()
	return out, nil
}
