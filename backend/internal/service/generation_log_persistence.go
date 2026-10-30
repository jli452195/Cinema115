package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

type LogPersistenceStatus struct {
	Enabled      bool   `json:"enabled"`
	LastError    string `json:"lastError"`
	FailedWrites uint64 `json:"failedWrites"`
}

type generationLogJournal interface {
	Write(GenerationLog) error
	List() ([]GenerationLogFile, error)
	Open(string) (*os.File, error)
	Close() error
}

type LogSnapshot struct {
	Entries     []GenerationLog      `json:"entries"`
	Capacity    int                  `json:"capacity"`
	Cursor      uint64               `json:"cursor"`
	InstanceID  string               `json:"instanceID"`
	Reset       bool                 `json:"reset"`
	Persistence LogPersistenceStatus `json:"persistence"`
}

// EnablePersistence is called before producers start. Runtime failures leave
// recent logs available and are exposed independently of the failing writer.
func (s *GenerationLogs) EnablePersistence(directory string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.files != nil {
		return fmt.Errorf("日志文件持久化已启用")
	}
	var session [6]byte
	if _, err := rand.Read(session[:]); err != nil {
		return err
	}
	files, recovered, err := openGenerationLogFiles(directory)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = hex.EncodeToString(session[:])
	if err != nil {
		s.persistence.LastError = monitorLogDetail(err.Error(), "")
		return err
	}
	s.files = files
	s.persistence = LogPersistenceStatus{Enabled: true}
	for _, entry := range recovered {
		if entry.ID <= s.sequence {
			continue
		}
		entry.Details = nil
		s.entries[s.next] = entry
		s.next = (s.next + 1) % len(s.entries)
		s.size = min(s.size+1, len(s.entries))
		s.sequence = entry.ID
	}
	return nil
}

func (s *GenerationLogs) Close() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.files == nil {
		return nil
	}
	err := s.files.Close()
	s.files = nil
	return err
}

func (s *GenerationLogs) LogFiles() ([]GenerationLogFile, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.files == nil {
		return []GenerationLogFile{}, nil
	}
	return s.files.List()
}

func (s *GenerationLogs) OpenLogFile(name string) (*os.File, int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.files == nil {
		return nil, 0, os.ErrNotExist
	}
	file, err := s.files.Open(name)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

// Direct append uses the OS page cache, with no per-line fsync or async queue
// that could silently overflow. The existing ring is only the recent UI cache.
func (s *GenerationLogs) persist(entry GenerationLog) {
	if s.files == nil || entry.Level == "DEBUG" {
		return
	}
	entry = persistentGenerationLog(entry)
	err := s.files.Write(entry)
	s.mu.Lock()
	previous := s.persistence.LastError
	s.persistence.LastError = ""
	if err != nil {
		s.persistence.FailedWrites++
		s.persistence.LastError = monitorLogDetail(err.Error(), "")
	}
	current := s.persistence.LastError
	s.mu.Unlock()
	if err != nil && (previous != current || entry.Level == "WARNING" || entry.Level == "ERROR") {
		data, _ := json.Marshal(entry)
		log.Printf("日志持久化失败（最近记录仍可查看）：%s；未保存记录=%s", current, data)
	}
}

const generationLogDetailBytes = 16 << 10

func boundedLogText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return ""
	}
	end := limit - 3
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "…"
}

// Preserve status/routing and the end of the trace, where the failure is.
func boundGenerationLogDetails(details *ProxyLogDetails) {
	details.Method = boundedLogText(details.Method, 16)
	details.Path = boundedLogText(details.Path, 256)
	budget := generationLogDetailBytes - len(details.Method) - len(details.Path)
	for _, field := range []*string{&details.Steps, &details.Lookup, &details.RequestHeaders, &details.ResponseHeaders, &details.LookupHeaders, &details.URL} {
		limit := min(budget, 8<<10)
		if field == &details.Steps && len(*field) > limit {
			start := len(*field) - limit + 3
			for start < len(*field) && !utf8.RuneStart((*field)[start]) {
				start++
			}
			*field = "…" + (*field)[start:]
		} else {
			*field = boundedLogText(*field, limit)
		}
		budget -= len(*field)
	}
}

var persistentLogCredential = regexp.MustCompile(`(?i)(["']?\b(?:api[_-]?key|x-emby-token|x-mediabrowser-token|signature|sig|auth_key|secret)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}&\]\r\n]+)`)

func persistentLogText(value string) string {
	if value == "" {
		return ""
	}
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		line = persistentLogCredential.ReplaceAllString(line, "${1}[已隐藏]")
		lines[i] = generationErrorDetail(errors.New(line), GenerateInput{})
	}
	return strings.Join(lines, "\n")
}

func persistentGenerationLog(entry GenerationLog) GenerationLog {
	entry.Message = persistentLogText(entry.Message)
	entry.Source = persistentLogText(entry.Source)
	// Proxy diagnostics are memory-only at every severity; files retain the summary.
	entry.Details = nil
	return entry
}
