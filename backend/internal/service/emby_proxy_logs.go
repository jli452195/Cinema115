package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
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
	entries  *GenerationLogs
	sequence atomic.Uint64
}

func NewEmbyProxyLogs(entries *GenerationLogs) *EmbyProxyLogs {
	return &EmbyProxyLogs{entries: entries}
}

type proxyTraceKey struct{}

func (s *EmbyProxyLogs) Trace(ctx context.Context) context.Context {
	if s == nil {
		return ctx
	}
	return context.WithValue(ctx, proxyTraceKey{}, fmt.Sprintf("302-%06d", s.sequence.Add(1)))
}

func (s *EmbyProxyLogs) Add(ctx context.Context, level, message string) {
	if s == nil {
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
