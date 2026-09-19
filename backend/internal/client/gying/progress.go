package gying

import "context"

type progressKey struct{}

// WithProgress attaches an operation-scoped observer. Messages must contain
// only fixed descriptions and counts, never URLs, credentials or response bodies.
// The observer can be called by concurrent detail workers and must be safe.
func WithProgress(ctx context.Context, observer func(level, message string)) context.Context {
	return context.WithValue(ctx, progressKey{}, observer)
}

func reportProgress(ctx context.Context, level, message string) {
	if observer, ok := ctx.Value(progressKey{}).(func(string, string)); ok && observer != nil {
		observer(level, message)
	}
}
