package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	GenerationLogFileMaxSizeMB  = 20
	GenerationLogFileMaxBackups = 10
	GenerationLogFileMaxAgeDays = 14
	generationLogFileName       = "cinema115.log"
	generationLogBackupTime     = "2006-01-02T15-04-05.000"
	generationLogReadBlock      = 64 * 1024
	generationLogMaxLine        = 256 * 1024
)

type GenerationLogFile struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Active     bool      `json:"active"`
}

// One process owns a journal. The mutex coordinates rotation with opening a
// download; readers retain their descriptor without locking subsequent writes.
type generationLogFiles struct {
	mu        sync.Mutex
	directory string
	writer    *lumberjack.Logger
	closed    bool
	repair    bool
}

func openGenerationLogFiles(directory string) (*generationLogFiles, []GenerationLog, error) {
	return openGenerationLogFilesWithLimits(directory, GenerationLogFileMaxSizeMB, GenerationLogFileMaxBackups, GenerationLogFileMaxAgeDays)
}

func openGenerationLogFilesWithLimits(directory string, maxSizeMB, maxBackups, maxAgeDays int) (*generationLogFiles, []GenerationLog, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, nil, fmt.Errorf("解析日志目录: %w", err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, nil, fmt.Errorf("创建日志目录: %w", err)
	}
	active := filepath.Join(directory, generationLogFileName)
	if err := prepareGenerationLogFile(active); err != nil {
		return nil, nil, err
	}
	files := &generationLogFiles{directory: directory, writer: &lumberjack.Logger{
		Filename: active, MaxSize: maxSizeMB, MaxBackups: maxBackups, MaxAge: maxAgeDays,
	}}
	recent, err := files.recover()
	if err != nil {
		return nil, nil, fmt.Errorf("恢复最近日志: %w", err)
	}
	return files, recent, nil
}

func prepareGenerationLogFile(active string) error {
	if info, err := os.Lstat(active); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("活动日志不是普通文件")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查活动日志: %w", err)
	}
	f, err := os.OpenFile(active, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("打开活动日志: %w", err)
	}
	if err = f.Chmod(0600); err == nil {
		err = repairGenerationLogTail(f)
	}
	closeErr := f.Close()
	if err := errors.Join(err, closeErr); err != nil {
		return fmt.Errorf("恢复活动日志尾部: %w", err)
	}
	return nil
}

// Repair only the unfinished last line. A complete JSON entry missing its
// newline is retained; an incomplete entry cannot corrupt the next append.
func repairGenerationLogTail(f *os.File) error {
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return err
	}
	position := info.Size()
	var tail []byte
	for position > 0 && len(tail) <= generationLogMaxLine {
		start := max(int64(0), position-generationLogReadBlock)
		block := make([]byte, position-start)
		if _, err := f.ReadAt(block, start); err != nil {
			return err
		}
		tail = append(block, tail...)
		if index := bytes.LastIndexByte(tail, '\n'); index >= 0 {
			if index == len(tail)-1 {
				return nil
			}
			if validGenerationLogLine(tail[index+1:]) {
				_, err = f.WriteAt([]byte{'\n'}, info.Size())
				return err
			}
			return f.Truncate(start + int64(index+1))
		}
		position = start
	}
	if position != 0 {
		return fmt.Errorf("活动日志尾部超过 %d KB 且没有换行，请保留文件后检查", generationLogMaxLine/1024)
	}
	if validGenerationLogLine(tail) {
		_, err = f.WriteAt([]byte{'\n'}, info.Size())
		return err
	}
	return f.Truncate(0)
}

func validGenerationLogLine(line []byte) bool {
	var entry GenerationLog
	return json.Unmarshal(line, &entry) == nil && entry.ID > 0 && !entry.Time.IsZero() && entry.Level != ""
}

func (s *generationLogFiles) Write(entry GenerationLog) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("编码日志: %w", err)
	}
	if len(data) > generationLogMaxLine {
		return fmt.Errorf("单条日志超过 %d KB", generationLogMaxLine/1024)
	}
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	if s.repair {
		if err := prepareGenerationLogFile(s.writer.Filename); err != nil {
			return err
		}
		s.repair = false
	}
	n, err := s.writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// Close resets lumberjack's cached size before a later retry. Startup
		// recovery also discards an interrupted line after an abrupt exit.
		s.repair = true
		return fmt.Errorf("追加日志文件: %w", errors.Join(err, s.writer.Close()))
	}
	return nil
}

func generationLogManagedName(name string) bool {
	if name == generationLogFileName {
		return true
	}
	if !strings.HasPrefix(name, "cinema115-") || !strings.HasSuffix(name, ".log") {
		return false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, "cinema115-"), ".log")
	_, err := time.Parse(generationLogBackupTime, stamp)
	return err == nil
}

func (s *generationLogFiles) List() ([]GenerationLogFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list()
}

func (s *generationLogFiles) list() ([]GenerationLogFile, error) {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return nil, fmt.Errorf("读取历史日志目录: %w", err)
	}
	files := make([]GenerationLogFile, 0, len(entries))
	for _, entry := range entries {
		if !generationLogManagedName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, os.ErrNotExist) {
			continue // Rotation cleanup may remove an archive between reads.
		}
		if err != nil {
			return nil, fmt.Errorf("读取日志文件资料: %w", err)
		}
		if info.Mode().IsRegular() {
			files = append(files, GenerationLogFile{Name: entry.Name(), Size: info.Size(), ModifiedAt: info.ModTime().UTC(), Active: entry.Name() == generationLogFileName})
		}
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Active != files[j].Active {
			return files[i].Active
		}
		return files[i].Name > files[j].Name // Backups use sortable UTC timestamps.
	})
	return files, nil
}

func (s *generationLogFiles) Open(name string) (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open(name)
}

func (s *generationLogFiles) open(name string) (*os.File, error) {
	if !generationLogManagedName(name) {
		return nil, os.ErrNotExist
	}
	full := filepath.Join(s.directory, name)
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	f, err := openGenerationLogRead(full)
	if err != nil {
		return nil, err
	}
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return nil, os.ErrNotExist
	}
	return f, nil
}

func (s *generationLogFiles) recover() ([]GenerationLog, error) {
	files, err := s.list()
	if err != nil {
		return nil, err
	}
	latest := make([]GenerationLog, 0, GenerationLogCapacity)
	for _, info := range files[:min(len(files), GenerationLogFileMaxBackups+1)] {
		f, err := s.open(info.Name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries, readErr := readGenerationLogTail(f, GenerationLogCapacity-len(latest))
		closeErr := f.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, err
		}
		latest = append(latest, entries...)
		if len(latest) == GenerationLogCapacity {
			break
		}
	}
	for i, j := 0, len(latest)-1; i < j; i, j = i+1, j-1 {
		latest[i], latest[j] = latest[j], latest[i]
	}
	return latest, nil
}

// Read backwards in fixed blocks, stopping after the newest requested rows.
// A malformed oversized line is skipped without retaining unbounded bytes.
func readGenerationLogTail(f *os.File, limit int) ([]GenerationLog, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	entries := make([]GenerationLog, 0, limit)
	var pending []byte
	position, skipping := info.Size(), false
	floor := max(int64(0), position-int64(GenerationLogFileMaxSizeMB+1)*1024*1024)
	decode := func(line []byte) {
		if len(line) == 0 || len(line) > generationLogMaxLine || skipping || len(entries) == limit {
			return
		}
		var entry GenerationLog
		if json.Unmarshal(line, &entry) == nil && entry.ID > 0 && !entry.Time.IsZero() && entry.Level != "" {
			entries = append(entries, entry)
		}
	}
	for position > floor && len(entries) < limit {
		start := max(floor, position-generationLogReadBlock)
		block := make([]byte, int(position-start)+len(pending))
		if _, err := f.ReadAt(block[:position-start], start); err != nil {
			return nil, err
		}
		copy(block[position-start:], pending)
		end := len(block)
		for i := len(block) - 1; i >= 0 && len(entries) < limit; i-- {
			if block[i] == '\n' {
				decode(block[i+1 : end])
				skipping = false
				end = i
			}
		}
		if start == floor {
			if floor == 0 {
				decode(block[:end])
			}
			break
		}
		if end > generationLogMaxLine {
			pending, skipping = nil, true
		} else {
			pending = append(pending[:0], block[:end]...)
		}
		position = start
	}
	return entries, nil
}

func (s *generationLogFiles) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.writer.Close()
}
