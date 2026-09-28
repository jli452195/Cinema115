package p115

import (
	"context"
	"crypto/tls"
	"io"
	"net/http/httptrace"
	"sync"
	"time"
)

// Only fixed stage names and numeric measurements cross the logging boundary.
// No host, path, query string, request headers or upstream error text is exposed.
type PlaybackTiming struct {
	Stage                              string
	Duration, DNS, TCP, TLS, FirstByte time.Duration
	Bytes                              int64
	HTTP, Connected, Reused            bool
	Failed, Canceled                   bool
}
type playbackTimingKey struct{}

func WithPlaybackTiming(ctx context.Context, report func(PlaybackTiming)) context.Context {
	return context.WithValue(ctx, playbackTimingKey{}, report)
}
func emitPlaybackTiming(ctx context.Context, value PlaybackTiming) {
	if report, ok := ctx.Value(playbackTimingKey{}).(func(PlaybackTiming)); ok && report != nil {
		report(value)
	}
}
func tracePlaybackHTTP(ctx context.Context, stage string) (context.Context, func()) {
	if report, ok := ctx.Value(playbackTimingKey{}).(func(PlaybackTiming)); !ok || report == nil {
		return ctx, func() {}
	}
	started := time.Now()
	var mu sync.Mutex
	var dnsStart, tlsStart time.Time
	connections := make(map[string]time.Time)
	value := PlaybackTiming{Stage: stage, HTTP: true}
	trace := &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { mu.Lock(); dnsStart = time.Now(); mu.Unlock() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			mu.Lock()
			if !dnsStart.IsZero() {
				value.DNS += time.Since(dnsStart)
				dnsStart = time.Time{}
			}
			mu.Unlock()
		},
		ConnectStart: func(network, addr string) { mu.Lock(); connections[network+addr] = time.Now(); mu.Unlock() },
		ConnectDone: func(network, addr string, _ error) {
			mu.Lock()
			if t, ok := connections[network+addr]; ok {
				value.TCP += time.Since(t)
				delete(connections, network+addr)
			}
			mu.Unlock()
		},
		TLSHandshakeStart: func() { mu.Lock(); tlsStart = time.Now(); mu.Unlock() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			mu.Lock()
			if !tlsStart.IsZero() {
				value.TLS += time.Since(tlsStart)
				tlsStart = time.Time{}
			}
			mu.Unlock()
		},
		GotConn: func(info httptrace.GotConnInfo) {
			mu.Lock()
			value.Connected = true
			value.Reused = info.Reused
			mu.Unlock()
		},
		GotFirstResponseByte: func() { mu.Lock(); value.FirstByte = time.Since(started); mu.Unlock() },
	}
	return httptrace.WithClientTrace(ctx, trace), func() {
		mu.Lock()
		snapshot := value
		snapshot.Duration = time.Since(started)
		mu.Unlock()
		emitPlaybackTiming(ctx, snapshot)
	}
}

type playbackHashWriter struct {
	writer   io.Writer
	duration time.Duration
}

func (w *playbackHashWriter) Write(p []byte) (int, error) {
	start := time.Now()
	n, err := w.writer.Write(p)
	w.duration += time.Since(start)
	return n, err
}
