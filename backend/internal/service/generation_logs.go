package service

import (
	"context"
	"errors"
	"fmt"
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

// GenerationLogs 使用固定容量环形缓冲区，日志不含分享地址或登录凭证。
type GenerationLogs struct {
	mu                     sync.RWMutex
	entries                [GenerationLogCapacity]GenerationLog
	next, size             int
	sequence, taskSequence uint64
}

func (s *GenerationLogs) append(task, source, level, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence++
	runes := []rune(message)
	if len(runes) > 1000 {
		message = string(runes[:1000]) + "…"
	}
	s.entries[s.next] = GenerationLog{ID: s.sequence, Time: time.Now().UTC(), Level: level, Module: "STRM", TaskID: task, Source: source, Message: message}
	s.next = (s.next + 1) % len(s.entries)
	if s.size < len(s.entries) {
		s.size++
	}
}

// Snapshot 返回独立副本，按最新优先排列，等级筛选采用精确匹配。
func (s *GenerationLogs) Snapshot(level string) []GenerationLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]GenerationLog, 0, s.size)
	for i := 0; i < s.size; i++ {
		entry := s.entries[(s.next-1-i+len(s.entries))%len(s.entries)]
		if level == "ALL" || entry.Level == level {
			result = append(result, entry)
		}
	}
	return result
}

type generationLogKey struct{}
type generationSourceKey struct{}
type generationLogger struct {
	store        *GenerationLogs
	task, source string
}

func WithGenerationSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, generationSourceKey{}, source)
}

func logGeneration(ctx context.Context, level, message string) {
	if logger, ok := ctx.Value(generationLogKey{}).(generationLogger); ok {
		logger.store.append(logger.task, logger.source, level, message)
	}
}

// LoggedGenerator 在网页和 Bot 共用入口记录整次任务，不启动额外协程。
type LoggedGenerator struct {
	Generator MediaGenerator
	Logs      *GenerationLogs
}

func (g *LoggedGenerator) Generate(ctx context.Context, input GenerateInput) (result *GenerationResult, err error) {
	g.Logs.mu.Lock()
	g.Logs.taskSequence++
	task := fmt.Sprintf("STRM-%06d", g.Logs.taskSequence)
	g.Logs.mu.Unlock()
	source, _ := ctx.Value(generationSourceKey{}).(string)
	if source != "Telegram" {
		source = "网页"
	}
	ctx = context.WithValue(ctx, generationLogKey{}, generationLogger{g.Logs, task, source})
	ctx = p115.WithRequestProgress(ctx, func(message string) { reportGeneration(ctx, "retrying", message) })
	started := time.Now()
	logGeneration(ctx, "INFO", "开始生成 STRM")
	defer func() {
		elapsed := time.Since(started).Seconds()
		counts := ""
		if result != nil {
			counts = fmt.Sprintf("，新增 %d", result.Written)
		}
		if err == nil {
			logGeneration(ctx, "INFO", fmt.Sprintf("任务完成%s，总耗时 %.2f 秒", counts, elapsed))
			return
		}
		level, reason := "ERROR", "生成失败："+generationErrorDetail(err, input)
		// 保留错误上下文；地址和凭证先脱敏，再交给日志缓冲区限制长度。
		switch {
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
	return g.Generator.Generate(ctx, input)
}
