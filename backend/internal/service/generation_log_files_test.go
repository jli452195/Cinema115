package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func generationLogFileEntry(id uint64, message string) GenerationLog {
	return GenerationLog{ID: id, Time: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), Level: "INFO", Module: "STRM", TaskID: "task-1", Source: "目录生成", Message: message}
}

func generationLogFileLines(t *testing.T, entries ...GenerationLog) []byte {
	t.Helper()
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			t.Fatal(err)
		}
	}
	return data.Bytes()
}

func TestGenerationLogFilesAppendAndRestoreStructuredRows(t *testing.T) {
	directory := t.TempDir()
	files, recovered, err := openGenerationLogFiles(directory)
	if err != nil || len(recovered) != 0 {
		t.Fatalf("open recovered=%+v err=%v", recovered, err)
	}
	first := generationLogFileEntry(1, "第一行\n第二行：完成")
	first.Details = &ProxyLogDetails{Steps: "步骤一\n步骤二", Status: 206}
	for _, entry := range []GenerationLog{first, generationLogFileEntry(2, "再次生成")} {
		if err := files.Write(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, generationLogFileName))
	if err != nil || bytes.Count(data, []byte{'\n'}) != 2 {
		t.Fatalf("not one JSON object per line: %q err=%v", data, err)
	}
	files, recovered, err = openGenerationLogFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	if len(recovered) != 2 || recovered[0].ID != 1 || recovered[1].ID != 2 || recovered[0].Message != first.Message || recovered[0].Details.Steps != first.Details.Steps {
		t.Fatalf("restore lost structured data: %+v", recovered)
	}
}

func TestGenerationLogFilesRepairInterruptedLastLine(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			directory := t.TempDir()
			data := generationLogFileLines(t, generationLogFileEntry(1, "完成"))
			if complete {
				data = append(data, bytes.TrimSuffix(generationLogFileLines(t, generationLogFileEntry(2, "完整但未换行")), []byte{'\n'})...)
			} else {
				data = append(data, []byte(`{"id":2,"message":"incomplete`)...)
			}
			filename := filepath.Join(directory, generationLogFileName)
			if err := os.WriteFile(filename, data, 0600); err != nil {
				t.Fatal(err)
			}
			files, recent, err := openGenerationLogFiles(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer files.Close()
			want := 1
			if complete {
				want = 2
			}
			if len(recent) != want {
				t.Fatalf("recovered %+v, want %d", recent, want)
			}
			if err := files.Write(generationLogFileEntry(3, "重启后追加")); err != nil {
				t.Fatal(err)
			}
			f, err := files.Open(generationLogFileName)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			parsed, err := readGenerationLogTail(f, 10)
			if err != nil || len(parsed) != want+1 || parsed[0].ID != 3 {
				t.Fatalf("append was joined to a broken line: %+v err=%v", parsed, err)
			}
		})
	}
}

func TestGenerationLogFilesRecoverRecentTailAcrossArchives(t *testing.T) {
	directory := t.TempDir()
	entries := make([]GenerationLog, 0, 2500)
	for id := uint64(1); id <= 2500; id++ {
		entries = append(entries, generationLogFileEntry(id, strings.Repeat("x", 120)))
	}
	archive := filepath.Join(directory, "cinema115-2026-10-30T12-00-00.000.log")
	if err := os.WriteFile(archive, generationLogFileLines(t, entries...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, generationLogFileName), generationLogFileLines(t, generationLogFileEntry(2501, "当前文件")), 0600); err != nil {
		t.Fatal(err)
	}
	files, recovered, err := openGenerationLogFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	if len(recovered) != GenerationLogCapacity || recovered[0].ID != 502 || recovered[len(recovered)-1].ID != 2501 {
		t.Fatalf("wrong recent window: count=%d first=%d last=%d", len(recovered), recovered[0].ID, recovered[len(recovered)-1].ID)
	}
}

func TestGenerationLogFilesRotateWhileDownloadRemainsOpen(t *testing.T) {
	directory := t.TempDir()
	files, _, err := openGenerationLogFilesWithLimits(directory, 1, 1, 14)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	if err := files.Write(generationLogFileEntry(1, "下载时的快照")); err != nil {
		t.Fatal(err)
	}
	download, err := files.Open(generationLogFileName)
	if err != nil {
		t.Fatal(err)
	}
	defer download.Close()
	info, err := download.Stat()
	if err != nil {
		t.Fatal(err)
	}
	for id := uint64(2); id <= 40; id++ {
		if err := files.Write(generationLogFileEntry(id, strings.Repeat("x", 80*1024))); err != nil {
			t.Fatal(err)
		}
		if id%12 == 0 {
			time.Sleep(2 * time.Millisecond) // Distinct millisecond backup names.
		}
	}
	snapshot, err := io.ReadAll(io.NewSectionReader(download, 0, info.Size()))
	if err != nil || !bytes.Equal(snapshot, generationLogFileLines(t, generationLogFileEntry(1, "下载时的快照"))) {
		t.Fatalf("rotation invalidated download: %s err=%v", snapshot, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		listed, err := files.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) == 2 {
			if !listed[0].Active || listed[1].Active || listed[0].Size > 1024*1024 || listed[1].Size > 1024*1024 {
				t.Fatalf("invalid rotation metadata: %+v", listed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("old backups were not pruned: %+v", listed)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	files, recovered, err := openGenerationLogFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	if len(recovered) == 0 || recovered[len(recovered)-1].ID != 40 || recovered[0].ID <= 1 {
		t.Fatalf("rotated recovery failed: count=%d", len(recovered))
	}
}

func TestGenerationLogFilesOnlyListManagedRegularFiles(t *testing.T) {
	directory := t.TempDir()
	files, _, err := openGenerationLogFiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer files.Close()
	for _, name := range []string{"notes.txt", "cinema115-other.log", "other.log"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte("unrelated"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := files.Open(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unmanaged file opened: %s err=%v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, "cinema115-2026-10-30T12-00-00.000.log"), 0700); err != nil {
		t.Fatal(err)
	}
	listed, err := files.List()
	if err != nil || len(listed) != 1 || !listed[0].Active {
		t.Fatalf("unmanaged files were listed: %+v err=%v", listed, err)
	}
	if err := files.Close(); err != nil {
		t.Fatal(err)
	}
	if err := files.Write(generationLogFileEntry(1, "after close")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed journal was reopened: %v", err)
	}
}
