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
	if report, ok := ctx.Value(progressContextKey{}).(func(GenerationProgress)); ok {
		report(GenerationProgress{Stage: stage, Message: message})
	}
}
