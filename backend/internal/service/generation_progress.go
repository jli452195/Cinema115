package service

import "context"

type GenerationProgress struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
}

type progressContextKey struct{}

// WithGenerationProgress 通过请求上下文回传进度，不启动额外后台任务。
func WithGenerationProgress(ctx context.Context, report func(GenerationProgress)) context.Context {
	return context.WithValue(ctx, progressContextKey{}, report)
}

func reportGeneration(ctx context.Context, stage, message string) {
	// 默认阶段细节写 DEBUG；重试保留 WARNING。
	level := "DEBUG"
	if stage == "retrying" {
		level = "WARNING"
	}
	reportGenerationWithLevel(ctx, level, stage, message)
}

// reportGenerationWithLevel 允许模式提示使用 INFO，同时保留网页进度阶段。
func reportGenerationWithLevel(ctx context.Context, level, stage, message string) {
	logMessage := message
	if stage == "waiting" {
		logMessage = "等待 115 完成处理，本阶段最多等待 90 秒"
	}
	logGeneration(ctx, level, logMessage)
	if report, ok := ctx.Value(progressContextKey{}).(func(GenerationProgress)); ok {
		report(GenerationProgress{Stage: stage, Message: message})
	}
}
