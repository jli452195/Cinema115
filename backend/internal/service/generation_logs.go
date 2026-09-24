package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
)

const GenerationLogCapacity = 2000

type GenerationLog struct {
	ID      uint64    `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Module  string    `json:"module"`
	TaskID  string    `json:"taskID"`
	Source  string    `json:"source"`
	Message string    `json:"message"`
}

// GenerationLogs 使用固定容量环形缓冲区，允许记录分享链接，不记录登录凭证。
type GenerationLogs struct {
	mu                     sync.RWMutex
	entries                [GenerationLogCapacity]GenerationLog
	next, size             int
	sequence, taskSequence uint64
}

func (s *GenerationLogs) append(task, source, level, message string) {
	s.Append("STRM", task, source, level, message)
}

// Append 接收已脱敏的模块日志，所有模块共用固定容量缓冲区。
func (s *GenerationLogs) Append(module, task, source, level, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	runes := []rune(message)
	if len(runes) > 1000 {
		message = string(runes[:1000]) + "…"
	}
	s.entries[s.next] = GenerationLog{ID: s.sequence, Time: time.Now().UTC(), Level: level, Module: module, TaskID: task, Source: source, Message: message}
	s.next = (s.next + 1) % len(s.entries)
	if s.size < len(s.entries) {
		s.size++
	}
}

// Snapshot 返回独立副本，按最新优先排列，等级筛选采用精确匹配。
func (s *GenerationLogs) Snapshot(level string, modules ...string) []GenerationLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]GenerationLog, 0, s.size)
	for i := 0; i < s.size; i++ {
		entry := s.entries[(s.next-1-i+len(s.entries))%len(s.entries)]
		if len(modules) > 0 && modules[0] != "ALL" && entry.Module != modules[0] {
			continue
		}
		if level == "ALL" || entry.Level == level {
			result = append(result, entry)
		}
	}
	return result
}

type generationLogKey struct{}
type generationSourceKey struct{}
type generationBatchKey struct{}

type generationBatch struct {
	id           int64
	index, total int
}

// WithGenerationBatch 传递已保存的消息批次及当前链接序号，不改变队列执行方式。
func WithGenerationBatch(ctx context.Context, id int64, index, total int) context.Context {
	return context.WithValue(ctx, generationBatchKey{}, generationBatch{id, index, total})
}

type generationLogger struct {
	store        *GenerationLogs
	task, source string
	redact       func(string) string
}

func WithGenerationSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, generationSourceKey{}, source)
}

func logGeneration(ctx context.Context, level, message string) {
	if logger, ok := ctx.Value(generationLogKey{}).(generationLogger); ok {
		if logger.redact != nil {
			message = logger.redact(message)
		}
		logger.store.append(logger.task, logger.source, level, message)
	}
}

// LoggedGenerator 在网页和 Bot 共用入口记录整次任务，不启动额外协程。
type LoggedGenerator struct {
	Generator MediaGenerator
	Logs      *GenerationLogs
	Root      string
}

func (g *LoggedGenerator) Generate(ctx context.Context, input GenerateInput) (result *GenerationResult, err error) {
	return g.generateLogged(ctx, input, func(ctx context.Context) (*GenerationResult, error) {
		return g.Generator.Generate(ctx, input)
	})
}

func (g *LoggedGenerator) generateLogged(ctx context.Context, input GenerateInput, run func(context.Context) (*GenerationResult, error)) (result *GenerationResult, err error) {
	g.Logs.mu.Lock()
	g.Logs.taskSequence++
	task := fmt.Sprintf("STRM-%06d", g.Logs.taskSequence)
	g.Logs.mu.Unlock()
	source, _ := ctx.Value(generationSourceKey{}).(string)
	if source != "Telegram" && source != "订阅" && source != "追更" && source != "目录分享" {
		source = "网页"
	}
	if directoryTask, ok := ctx.Value(directoryGenerationKey{}).(string); ok && directoryTask != "" {
		task = directoryTask
	}
	logger := generationLogger{store: g.Logs, task: task, source: source}
	if _, ok := ctx.Value(directoryGenerationKey{}).(string); ok {
		cookie := generationCookie(ctx, nil)
		logger.redact = func(message string) string { return monitorLogDetail(message, cookie) }
	}
	ctx = context.WithValue(ctx, generationLogKey{}, logger)
	ctx = p115.WithRequestProgress(ctx, func(message string) { reportGeneration(ctx, "retrying", message) })
	started := time.Now()
	if batch, ok := ctx.Value(generationBatchKey{}).(generationBatch); ok && source == "Telegram" {
		if batch.index == 1 {
			logGeneration(ctx, "INFO", fmt.Sprintf("创建新的消息处理批次，%d 个链接", batch.total))
		}
		logGeneration(ctx, "INFO", fmt.Sprintf("消息批次 %d：正在处理 %d/%d", batch.id, batch.index, batch.total))
	}
	mode := "仅当前目录"
	if input.Recursive {
		mode = "包含所有子目录"
	}
	logGeneration(ctx, "INFO", "遍历模式："+mode)
	if share, parseErr := p115.ParseShareURL(input.URL); parseErr == nil {
		if input.Password != "" {
			share.Password = strings.TrimSpace(input.Password)
		}
		// 仅重建分享码和实际提取码，避免原始 URL 夹带令牌、用户信息或其他参数。
		link := "https://115cdn.com/s/" + url.PathEscape(share.Code)
		if share.Password != "" {
			link += "?password=" + url.QueryEscape(share.Password)
		}
		logGeneration(ctx, "INFO", "115链接："+link)
		logGeneration(ctx, "INFO", "开始处理任务："+url.PathEscape(share.Code))
	} else {
		logGeneration(ctx, "INFO", "开始处理任务："+task)
	}
	if g.Root != "" {
		if directory, resolveErr := ResolveOutputDirectory(g.Root, input.OutputDir); resolveErr == nil {
			logGeneration(ctx, "INFO", fmt.Sprintf("使用配置的目录：%s（%s）", filepath.ToSlash(filepath.Clean(input.OutputDir)), filepath.ToSlash(directory)))
		}
	}
	defer func() {
		elapsed := time.Since(started).Seconds()
		counts := ""
		if result != nil {
			counts = fmt.Sprintf("，新增 %d", result.Written)
		}
		if err == nil {
			label := "分享生成 STRM 完成！"
			if result != nil && result.ReshareURL != "" {
				label = "转存分享生成 STRM 完成！"
			}
			logGeneration(ctx, "INFO", fmt.Sprintf("%s总耗时 %.2f 秒", label, elapsed))
			return
		}
		level, reason := "ERROR", "生成失败："+generationErrorDetail(err, input)
		// 保留错误上下文；地址和凭证先脱敏，再交给日志缓冲区限制长度。
		switch {
		case errors.Is(err, p115.ErrShareUnderReview):
			level, reason = "WARNING", "分享审核中，已停止处理："+generationErrorDetail(err, input)
		case errors.Is(err, context.Canceled):
			level, reason = "WARNING", "任务已取消"
		case errors.Is(err, context.DeadlineExceeded):
			reason = "任务超时：" + generationErrorDetail(err, input)
		case errors.Is(err, ErrNoMedia):
			level, reason = "WARNING", "没有符合筛选条件的视频"
		case errors.Is(err, ErrGenerationInput):
			reason = "生成参数或登录状态无效：" + generationErrorDetail(err, input)
		}
		logGeneration(ctx, level, fmt.Sprintf("%s%s，耗时 %.2f 秒", reason, counts, elapsed))
	}()
	return run(ctx)
}

func (g *LoggedGenerator) GenerateBatch(ctx context.Context, inputs []GenerateInput) ([]BatchOutcome, error) {
	batch, ok := g.Generator.(BatchGenerator)
	if !ok {
		return nil, fmt.Errorf("生成服务未配置合并转存能力")
	}
	if len(inputs) == 0 {
		return []BatchOutcome{}, nil
	}
	var outcomes []BatchOutcome
	var batchErr error
	_, _ = g.generateLogged(ctx, inputs[0], func(ctx context.Context) (*GenerationResult, error) {
		outcomes, batchErr = batch.GenerateBatch(ctx, inputs)
		total := &GenerationResult{}
		if len(outcomes) != len(inputs) {
			batchErr = errors.Join(batchErr, errors.New("合并转存返回结果数量不正确"))
			return total, batchErr
		}
		loggedErr := batchErr
		for i, out := range outcomes {
			addBatchResult(total, out.Result)
			if out.Result != nil && out.Result.ReshareURL != "" {
				total.ReshareURL = out.Result.ReshareURL
			}
			if out.Err != nil {
				logGeneration(ctx, "WARNING", fmt.Sprintf("合并批次第 %d 条：%s", i+1, generationErrorDetail(out.Err, inputs[i])))
			} else {
				logGeneration(ctx, "INFO", fmt.Sprintf("合并批次第 %d 条：STRM 生成完成", i+1))
			}
			loggedErr = errors.Join(loggedErr, out.Err)
		}
		return total, loggedErr
	})
	return outcomes, batchErr
}
