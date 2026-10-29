package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"strm115/backend/internal/mediacategory"
)

var (
	ErrClassificationConflict    = errors.New("分类规则文件已变化，请重新读取后保存")
	ErrClassificationUnavailable = errors.New("分类规则尚未成功加载，请修正配置后重新加载")
)

// ClassificationSnapshot is immutable and independent of later saves/reloads.
// YAML and Revision can be persisted with a task; Classifier is rebuilt on restart.
type ClassificationSnapshot struct {
	YAML       string                    `json:"yaml"`
	Revision   string                    `json:"revision"`
	Classifier *mediacategory.Classifier `json:"-"`
}

type ClassificationDocument struct {
	YAML           string                     `json:"yaml"`
	Revision       string                     `json:"revision"`
	ActiveRevision string                     `json:"activeRevision"`
	LoadError      string                     `json:"loadError,omitempty"`
	Diagnostic     *mediacategory.ConfigError `json:"diagnostic,omitempty"`
}

// MediaClassification manages the one YAML file. Its compiled snapshot is only
// replaced after a successful save or reload, never by reading the editor state.
type MediaClassification struct {
	mu      sync.RWMutex
	path    string
	current ClassificationSnapshot
}

// OpenMediaClassification returns a usable manager even on a load error, so an
// administrator can repair a broken file through the same settings endpoint.
func OpenMediaClassification(ctx context.Context, path string) (*MediaClassification, error) {
	s := &MediaClassification{path: path}
	if _, revision, err := readClassification(ctx, path); err != nil {
		return s, err
	} else if revision == "" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return s, fmt.Errorf("创建分类规则目录: %w", err)
		}
		if err := writeClassification(ctx, path, mediacategory.DefaultYAML(), ""); err != nil && !errors.Is(err, ErrClassificationConflict) {
			return s, err
		}
	}
	_, err := s.Reload(ctx)
	return s, err
}

// Snapshot uses memory only; normal classification does not read or parse YAML.
func (s *MediaClassification) Snapshot() (ClassificationSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.current.Classifier == nil {
		return ClassificationSnapshot{}, ErrClassificationUnavailable
	}
	return s.current, nil
}

// Read returns the actual file and its validation error without activating it.
// Invalid YAML remains editable, using its own revision for an explicit repair.
func (s *MediaClassification) Read(ctx context.Context) (ClassificationDocument, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	source, revision, err := readClassification(ctx, s.path)
	if err != nil {
		return ClassificationDocument{}, err
	}
	document := ClassificationDocument{YAML: source, Revision: revision, ActiveRevision: s.current.Revision}
	if _, err := mediacategory.Compile(source); err != nil {
		document.LoadError = err.Error()
		errors.As(err, &document.Diagnostic)
	}
	if err := ctx.Err(); err != nil {
		return ClassificationDocument{}, err
	}
	return document, nil
}

func (s *MediaClassification) Reload(ctx context.Context) (ClassificationDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	source, revision, err := readClassification(ctx, s.path)
	if err != nil {
		return ClassificationDocument{}, err
	}
	classifier, err := mediacategory.Compile(source)
	if err != nil {
		return ClassificationDocument{}, err
	}
	if err := ctx.Err(); err != nil {
		return ClassificationDocument{}, err
	}
	s.current = ClassificationSnapshot{YAML: source, Revision: revision, Classifier: classifier}
	return ClassificationDocument{YAML: source, Revision: revision, ActiveRevision: revision}, nil
}

func (s *MediaClassification) Save(ctx context.Context, source, expected string) (ClassificationDocument, error) {
	if err := ctx.Err(); err != nil {
		return ClassificationDocument{}, err
	}
	classifier, err := mediacategory.Compile(source)
	if err != nil {
		return ClassificationDocument{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	disk, revision, err := readClassification(ctx, s.path)
	if err != nil {
		return ClassificationDocument{}, err
	}
	// Replaying the same saved content confirms a lost response without writing.
	if disk != source {
		if revision != expected {
			return ClassificationDocument{}, ErrClassificationConflict
		}
		if err := writeClassification(ctx, s.path, source, expected); err != nil {
			return ClassificationDocument{}, err
		}
	}
	if err := ctx.Err(); err != nil && disk == source {
		return ClassificationDocument{}, err
	}
	revision = classificationRevision(source)
	s.current = ClassificationSnapshot{YAML: source, Revision: revision, Classifier: classifier}
	return ClassificationDocument{YAML: source, Revision: revision, ActiveRevision: revision}, nil
}

func classificationRevision(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

// A missing file has the empty revision; an existing empty file has its hash.
func readClassification(ctx context.Context, path string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("检查分类规则文件: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%w：配置路径须为普通 YAML 文件", mediacategory.ErrConfig)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", fmt.Errorf("读取分类规则文件: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, mediacategory.MaxYAMLBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("读取分类规则内容: %w", err)
	}
	if len(raw) > mediacategory.MaxYAMLBytes {
		return "", "", fmt.Errorf("%w：YAML 不能超过 %d KiB", mediacategory.ErrConfig, mediacategory.MaxYAMLBytes/1024)
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	source := string(raw)
	return source, classificationRevision(source), nil
}

func writeClassification(ctx context.Context, path, source, expected string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".classification-*")
	if err != nil {
		return fmt.Errorf("创建分类规则临时文件: %w", err)
	}
	defer os.Remove(f.Name())
	_, writeErr := io.Copy(f, strings.NewReader(source))
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("写入分类规则: %w", err)
	}
	_, revision, err := readClassification(ctx, path)
	if err != nil {
		return err
	}
	if revision != expected {
		return ErrClassificationConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expected == "" {
		// Installing the initial file must never replace one created meanwhile.
		err = os.Link(f.Name(), path)
		if errors.Is(err, os.ErrExist) {
			return ErrClassificationConflict
		}
	} else {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("保存分类规则文件: %w", err)
	}
	return nil
}
