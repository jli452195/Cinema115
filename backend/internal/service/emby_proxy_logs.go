package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// PlaybackLogName keeps display names on one bounded log line.
func PlaybackLogName(name string) string {
	clean := make([]rune, 0, 80)
	for _, r := range name {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		clean = append(clean, r)
		if len(clean) == 80 {
			break
		}
	}
	value := strings.TrimSpace(string(clean))
	if value == "" {
		value = "未提供名称"
	}
	return strconv.Quote(value)
}

func PlaybackAccountLabel(name, uid, accountID string) string {
	if uid == "" {
		uid = "未知"
	}
	return PlaybackLogName(name) + "（UID " + uid + "，配置=" + accountID + "）"
}

// EmbyProxyLogs writes to the shared real-time log buffer under its own module.
// Callers supply fixed diagnostic messages and validated identifiers, never raw URLs or credentials.
type EmbyProxyLogs struct {
	entries *GenerationLogs
}

func NewEmbyProxyLogs(entries *GenerationLogs) *EmbyProxyLogs {
	return &EmbyProxyLogs{entries: entries}
}

type proxyTraceKey struct{}

// Administrator-only diagnostics. Header strings must be sanitized by the caller.
type ProxyLogDetails struct {
	Steps           string `json:"steps,omitempty"`
	URL             string `json:"url,omitempty"`
	RequestHeaders  string `json:"requestHeaders,omitempty"`
	ResponseHeaders string `json:"responseHeaders,omitempty"`
	LookupHeaders   string `json:"lookupHeaders,omitempty"`
	Lookup          string `json:"lookup,omitempty"`
	Method          string `json:"method,omitempty"`
	Path            string `json:"path,omitempty"`
	DurationMS      int64  `json:"durationMs"`
	Status          int    `json:"status"`
}

func ProxyTraceID(ctx context.Context) string {
	id, _ := ctx.Value(proxyTraceKey{}).(string)
	return id
}

func (s *EmbyProxyLogs) AddDetails(ctx context.Context, level, message string, details ProxyLogDetails) {
	if s == nil {
		return
	}
	if group, ok := ctx.Value(proxyGroupKey{}).(*proxyLogGroup); ok {
		group.mu.Lock()
		defer group.mu.Unlock()
		group.details = details
		if group.bytes+len(message) <= 16384 && len(group.lines) < 64 {
			group.lines = append(group.lines, group.step(level, message))
			group.bytes += len(message)
		}
		if group.level == "DEBUG" && level != "DEBUG" || level == "ERROR" || level == "WARNING" && group.level != "ERROR" {
			group.level = level
		}
		if group.summary == "" {
			group.summary = message
		}
		return
	}
	s.entries.appendDetails("Emby 302", ProxyTraceID(ctx), "反代", level, message, &details)
}

type proxyGroupKey struct{}
type proxyLogGroup struct {
	mu             sync.Mutex
	started        time.Time
	debug          bool
	lines          []string
	bytes          int
	level, summary string
	details        ProxyLogDetails
}

func (g *proxyLogGroup) step(level, message string) string {
	if !g.debug {
		return level + " · " + message
	}
	return fmt.Sprintf("%s · [+%d ms] %s", level, time.Since(g.started).Milliseconds(), message)
}

// The immutable request setting is also inherited by request-owned workers.
func ProxyDebugEnabled(ctx context.Context) bool {
	g, ok := ctx.Value(proxyGroupKey{}).(*proxyLogGroup)
	return ok && g.debug
}

// Group one HTTP request into one row; steps remain available on expansion.
func (s *EmbyProxyLogs) BeginRequest(ctx context.Context, debug bool) (context.Context, func()) {
	g := &proxyLogGroup{level: "DEBUG", started: time.Now(), debug: debug}
	ctx = context.WithValue(ctx, proxyGroupKey{}, g)
	return ctx, func() {
		if s == nil {
			return
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if len(g.lines) == 0 && g.summary == "" {
			return
		}
		if g.summary == "" {
			g.summary = g.lines[len(g.lines)-1]
		}
		if len(g.lines) > 0 {
			g.details.Steps = strings.Join(g.lines, "\n")
		}
		s.entries.appendDetails("Emby 302", ProxyTraceID(ctx), "反代", g.level, g.summary, &g.details)
	}
}

func (s *EmbyProxyLogs) Trace(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, proxyTraceKey{}, s.entries.NewTaskID("302"))
}

func (s *EmbyProxyLogs) Add(ctx context.Context, level, message string) {
	if s == nil {
		return
	}
	if group, ok := ctx.Value(proxyGroupKey{}).(*proxyLogGroup); ok {
		group.mu.Lock()
		defer group.mu.Unlock()
		if group.level == "DEBUG" && level == "INFO" {
			group.level = "INFO"
		}
		if group.bytes+len(message) <= 16384 && len(group.lines) < 64 {
			group.lines = append(group.lines, group.step(level, message))
			group.bytes += len(message)
		}
		if level == "ERROR" || level == "WARNING" && group.level != "ERROR" {
			group.level = level
			group.summary = message
		}
		if (strings.HasPrefix(message, "播放取链结果：") || strings.HasPrefix(message, "下载取链结果：")) && group.level == "INFO" {
			group.summary = message
		}
		return
	}
	id, _ := ctx.Value(proxyTraceKey{}).(string)
	s.entries.Append("Emby 302", id, "反代", level, message)
}

func (s *EmbyProxyLogs) Snapshot() []GenerationLog {
	if s == nil || s.entries == nil {
		return []GenerationLog{}
	}
	return s.entries.Snapshot("ALL", "Emby 302")
}
