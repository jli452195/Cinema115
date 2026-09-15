package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/model"
)

// GenerateBatch 为每个来源预先落盘，审核项可独立定时重试，不重复执行成功来源。
func (s *ShareTasks) GenerateBatch(ctx context.Context, inputs []GenerateInput) ([]BatchOutcome, error) {
	ctx = s.pinAccount(ctx)
	batch, ok := s.Generator.(BatchGenerator)
	if !ok {
		return nil, fmt.Errorf("生成服务未配置合并转存能力")
	}
	tasks := make([]*model.ShareTask, len(inputs))
	requests := make([]shareTaskRequest, len(inputs))
	for i, input := range inputs {
		task, req, err := s.prepareTask(ctx, input)
		if err != nil {
			// 尚未调用云端，已记录的任务明确标记失败。
			for j := 0; j < i; j++ {
				_, saveErr := s.finish(ctx, tasks[j], requests[j], nil, err, 0)
				err = errors.Join(err, saveErr)
			}
			return nil, err
		}
		tasks[i], requests[i] = task, req
	}
	started := time.Now()
	if len(requests) > 0 {
		ctx = context.WithValue(ctx, automationContextKey{}, requests[0].Settings)
	}
	outcomes, batchErr := batch.GenerateBatch(ctx, inputs)
	if len(outcomes) != len(inputs) {
		outcomes = make([]BatchOutcome, len(inputs))
		batchErr = errors.Join(batchErr, errors.New("合并转存返回结果数量不正确"))
	}
	elapsed := time.Since(started)
	for i := range outcomes {
		out := &outcomes[i]
		if out.Result == nil && out.Err == nil {
			out.Err = batchErr
			if out.Err == nil {
				out.Err = errors.New("合并转存未返回本来源结果")
			}
		}
		requests[i].Merged, requests[i].OwnShareCID = true, out.RetryCID
		requests[i].OwnRootIDs = out.RetryRootIDs
		if out.Result != nil && out.Result.ReshareURL != "" {
			tasks[i].Note = "本来源已纳入 TG 合并分享，和同批正常来源共用一个长期分享链接。"
			if !out.Result.Cleaned {
				tasks[i].Note += "共同临时目录尚未清理，请核对转存任务记录。"
			}
		}
		out.Result, out.Err = s.finish(ctx, tasks[i], requests[i], out.Result, out.Err, elapsed)
	}
	return outcomes, nil
}
