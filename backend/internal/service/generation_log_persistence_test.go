package service

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerationLogsPersistenceRetainsHistoryBeyondRecentCache(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	logs := &GenerationLogs{}
	if err := logs.EnablePersistence(directory); err != nil {
		t.Fatal(err)
	}
	firstTask := logs.NewTaskID("STRM")
	logs.Append("STRM", firstTask, "目录生成", "ERROR", "earlier failure remains in file")
	for i := 0; i < GenerationLogCapacity+5; i++ {
		logs.Append("STRM", firstTask, "目录生成", "INFO", "completed")
	}
	logs.Append("STRM", firstTask, "目录生成", "DEBUG", "memory-only-debug")
	if len(logs.Snapshot("ERROR")) != 0 {
		t.Fatal("fixture did not exceed the recent cache")
	}
	if err := logs.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "cinema115.log"))
	if err != nil || !strings.Contains(string(data), "earlier failure remains in file") || strings.Contains(string(data), "memory-only-debug") {
		t.Fatalf("unexpected persistent history: err=%v", err)
	}
	restarted := &GenerationLogs{}
	if err := restarted.EnablePersistence(directory); err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	entries := restarted.Snapshot("ALL")
	if len(entries) != GenerationLogCapacity || entries[0].ID != GenerationLogCapacity+6 {
		t.Fatalf("recent cache not restored: count=%d first=%+v", len(entries), entries[0])
	}
	if restarted.NewTaskID("STRM") == firstTask {
		t.Fatal("operation identity was reused after restart")
	}
	restarted.Append("STRM", "new", "目录生成", "INFO", "after restart")
	if restarted.Snapshot("ALL")[0].ID != entries[0].ID+1 {
		t.Fatal("event cursor did not continue after restart")
	}
}

func TestGenerationLogsPersistSanitizedBoundedDetails(t *testing.T) {
	directory := t.TempDir()
	logs := &GenerationLogs{}
	if err := logs.EnablePersistence(directory); err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	details := &ProxyLogDetails{
		URL:             "https://cdn.example/video?signature=signed-secret",
		RequestHeaders:  "User-Agent: player\nCookie: UID=private-uid; CID=private-cid",
		ResponseHeaders: "Location: https://cdn.example/video?token=private-token",
		Lookup:          "api_key=private-key",
		Steps:           strings.Repeat("早期诊断 ", 4000) + "\n最终失败原因仍然保留",
		Method:          "GET", Path: "/Videos/{Id}/stream",
	}
	logs.appendDetails("Emby 302", "task", "反代", "ERROR", "failed https://cdn.example/video?sig=private-signature", details)
	entry := logs.Snapshot("ERROR")[0]
	if entry.Details.URL != details.URL || !strings.Contains(entry.Details.Steps, "最终失败原因仍然保留") {
		t.Fatal("in-memory diagnostics changed or important trace tail was lost")
	}
	size := 0
	for _, text := range []string{entry.Details.Method, entry.Details.Path, entry.Details.URL, entry.Details.Steps, entry.Details.Lookup, entry.Details.RequestHeaders, entry.Details.ResponseHeaders, entry.Details.LookupHeaders} {
		size += len(text)
	}
	if size > generationLogDetailBytes || len(details.Steps) < len(entry.Details.Steps) {
		t.Fatalf("detail budget exceeded or caller object mutated: %d", size)
	}
	data, err := os.ReadFile(filepath.Join(directory, "cinema115.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"cdn.example", "signed-secret", "private-uid", "private-cid", "private-token", "private-key", "private-signature"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("persistent log contains %q", secret)
		}
	}
	var saved GenerationLog
	if err := json.Unmarshal(data, &saved); err != nil || !strings.Contains(saved.Details.Steps, "最终失败原因仍然保留") {
		t.Fatalf("saved diagnostic is invalid: %v", err)
	}
}

type failingGenerationLogJournal struct {
	generationLogJournal
	err error
}

func (f *failingGenerationLogJournal) Write(GenerationLog) error { return f.err }

func TestGenerationLogsFileFailureRemainsVisibleAfterRecovery(t *testing.T) {
	journal := &failingGenerationLogJournal{err: errors.New("disk unavailable")}
	logs := &GenerationLogs{files: journal, persistence: LogPersistenceStatus{Enabled: true}}
	logs.Append("STRM", "task", "目录生成", "ERROR", "failed operation")
	result := logs.Read("ALL", "ALL", 0, false)
	if len(result.Entries) != 1 || result.Persistence.LastError == "" || result.Persistence.FailedWrites != 1 {
		t.Fatalf("write failure was hidden: %+v", result)
	}
	journal.err = nil
	logs.Append("STRM", "task", "目录生成", "INFO", "recovered")
	result = logs.Read("ALL", "ALL", result.Cursor, true)
	if len(result.Entries) != 1 || result.Persistence.LastError != "" || result.Persistence.FailedWrites != 1 {
		t.Fatalf("recovery erased loss count: %+v", result)
	}
}

func TestGenerationLogsIncrementalCursorAndOverwrittenCache(t *testing.T) {
	logs := &GenerationLogs{}
	logs.Append("STRM", "task", "目录生成", "INFO", "first")
	first := logs.Read("ALL", "ALL", 0, false)
	if next := logs.Read("ALL", "ALL", first.Cursor, true); len(next.Entries) != 0 || next.Reset {
		t.Fatal("unchanged polling returned the full cache")
	}
	logs.Append("搜索", "other", "Garden", "DEBUG", "cached")
	if next := logs.Read("INFO", "STRM", first.Cursor, true); len(next.Entries) != 0 || next.Cursor != 2 {
		t.Fatal("filtered polling did not advance its cursor")
	}
	for i := 0; i < GenerationLogCapacity; i++ {
		logs.Append("STRM", "task", "目录生成", "INFO", "next")
	}
	if next := logs.Read("ALL", "ALL", first.Cursor, true); !next.Reset || len(next.Entries) != GenerationLogCapacity {
		t.Fatal("overwritten cache did not trigger a replacement snapshot")
	}
	if next := logs.Read("ALL", "ALL", 100000, true); !next.Reset {
		t.Fatal("cursor from an unavailable process did not reset")
	}
}

func TestGenerationLogsRestartResetsReusedDebugCursor(t *testing.T) {
	directory := t.TempDir()
	old := &GenerationLogs{}
	if err := old.EnablePersistence(directory); err != nil {
		t.Fatal(err)
	}
	old.Append("STRM", "old", "目录生成", "INFO", "saved")
	old.Append("STRM", "old", "目录生成", "DEBUG", "not persisted")
	previous := old.Read("ALL", "ALL", 0, false)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	current := &GenerationLogs{}
	if err := current.EnablePersistence(directory); err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	current.Append("STRM", "new", "目录生成", "INFO", "new process reused cursor")
	next := current.Read("ALL", "ALL", previous.Cursor, true, previous.InstanceID)
	if !next.Reset || next.InstanceID == previous.InstanceID || len(next.Entries) != 2 || next.Entries[0].Message != "new process reused cursor" {
		t.Fatalf("restart at the same numeric cursor lost a new log: %+v", next)
	}
}

func BenchmarkGenerationLogsAppend(b *testing.B) {
	for _, mode := range []string{"memory", "jsonl"} {
		b.Run(mode, func(b *testing.B) {
			logs := &GenerationLogs{}
			if mode == "jsonl" {
				if err := logs.EnablePersistence(b.TempDir()); err != nil {
					b.Fatal(err)
				}
				defer logs.Close()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				logs.Append("STRM", "task", "目录生成", "INFO", "STRM 写入 120，未变化 0，失败 0")
			}
		})
	}
}
