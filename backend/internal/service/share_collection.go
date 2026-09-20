package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

type shareCollectionContextKey struct{}

// WithShareCollectionScope 仅供网页明确选择的收集入口使用，不改变 Telegram 原有消息窗口。
func WithShareCollectionScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, shareCollectionContextKey{}, scope)
}

type shareRunAccepted struct {
	runID       string
	digest      [32]byte
	fingerprint string
	taskIDs     []string
}

func shareCollectionKey(ctx context.Context, settings AutomationSettings, cookie string, inputs []GenerateInput) (string, error) {
	scope, _ := ctx.Value(shareCollectionContextKey{}).(string)
	if scope == "" || settings.TGBatchWaitSeconds <= 0 {
		return "", nil
	}
	// 只合并转存模式，且本次所有链接必须使用同一套生成参数。
	var options string
	for _, input := range inputs {
		if input.Mode == "direct" || input.Mode == "" && !settings.ReshareEnabled {
			return "", nil
		}
		input.URL, input.Password, input.CID = "https://115cdn.com/s/collection", "", "0"
		key, err := shareRunFingerprint(settings, cookieHash(cookie), []GenerateInput{input})
		if err != nil {
			return "", err
		}
		if options != "" && options != key {
			return "", nil
		}
		options = key
	}
	// 同时固定通知等非生成设置，配置变化不能污染正在收集的批次。
	raw, err := json.Marshal(struct {
		Scope, Options string
		Settings       AutomationSettings
	}{scope, options, settings})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// 调用方持有队列锁；只保存任务编号，不保存凭据。
func (s *ShareRuns) rememberSubmission(id string, digest [32]byte, fingerprint string, run *shareRun, items []ShareRunItem) ShareRunView {
	for key, accepted := range s.accepted {
		if s.runs[accepted.runID] == nil {
			delete(s.accepted, key)
		}
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.TaskID)
	}
	s.accepted[id] = shareRunAccepted{runID: run.view.ID, digest: digest, fingerprint: fingerprint, taskIDs: ids}
	view := cloneShareRun(run.view)
	view.SelectedTaskIDs = append([]string(nil), ids...)
	return view
}

func (s *ShareRuns) appendCollected(ctx context.Context, run *shareRun, id string, digest [32]byte, fingerprint string, inputs []GenerateInput) (ShareRunView, error) {
	if err := ctx.Err(); err != nil {
		return ShareRunView{}, err
	}
	combined := make([]GenerateInput, 0, len(run.requests)+len(inputs))
	settings := run.ctx.Value(automationContextKey{}).(AutomationSettings)
	known := make(map[string]ShareRunItem, len(run.requests)+len(inputs))
	for i, req := range run.requests {
		combined = append(combined, req.Input)
		key, err := shareRunFingerprint(settings, "", []GenerateInput{req.Input})
		if err != nil {
			return ShareRunView{}, err
		}
		known[key] = run.view.Items[i]
	}
	tasks := make([]*model.ShareTask, 0, len(inputs))
	requests := make([]shareTaskRequest, 0, len(inputs))
	items := make([]ShareRunItem, 0, len(inputs))
	selected := make([]ShareRunItem, 0, len(inputs))
	saveCtx, cancel := context.WithTimeout(run.ctx, 10*time.Second)
	defer cancel()
	for _, input := range inputs {
		key, err := shareRunFingerprint(settings, "", []GenerateInput{input})
		if err != nil {
			return ShareRunView{}, err
		}
		if item, exists := known[key]; exists {
			selected = append(selected, item)
			continue
		}
		task, req, err := s.tasks.prepareTask(saveCtx, input)
		if err != nil {
			for i, task := range tasks {
				_, saveErr := s.tasks.finish(run.ctx, task, requests[i], nil, err, 0)
				err = errors.Join(err, saveErr)
			}
			return ShareRunView{}, err
		}
		tasks, requests = append(tasks, task), append(requests, req)
		item := ShareRunItem{TaskID: task.ID, Status: "queued", Message: "已加入收集批次，等待执行"}
		items, selected = append(items, item), append(selected, item)
		known[key] = item
		combined = append(combined, input)
	}
	cacheKey, err := shareRunFingerprint(settings, "", combined)
	if err != nil {
		return ShareRunView{}, err
	}
	run.tasks = append(run.tasks, tasks...)
	run.requests = append(run.requests, requests...)
	run.view.Items = append(run.view.Items, items...)
	run.cacheKey = cacheKey
	s.activeByFingerprint[fingerprint] = run.view.ID
	s.tasks.Logs.Append("STRM", run.view.ID, "网页", "INFO", fmt.Sprintf("加入收集批次：新增 %d 个链接，批次累计 %d 个链接", len(items), len(run.tasks)))
	return s.rememberSubmission(id, digest, fingerprint, run, selected), nil
}
