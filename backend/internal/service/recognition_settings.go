package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"strm115/backend/internal/recognition"
)

type RecognitionDocument struct {
	Groups         string                  `json:"groups"`
	Platforms      string                  `json:"platforms"`
	Revision       string                  `json:"revision"`
	ActiveRevision string                  `json:"activeRevision"`
	LoadError      string                  `json:"loadError,omitempty"`
	Diagnostic     *recognition.Diagnostic `json:"diagnostic,omitempty"`
}

// RecognitionSettings 管理两个 UTF-8 TXT；校验、文件保存全部成功后才激活新规则。
type RecognitionSettings struct {
	mu        sync.Mutex
	directory string
	current   recognition.Snapshot
}
type recognitionFile struct {
	source string
	exists bool
}

// 事务日志保存原始字节，进程在两次文件替换之间退出时可以完整恢复旧版本。
type recognitionJournal struct {
	Version          int    `json:"version"`
	Groups           []byte `json:"groups"`
	Platforms        []byte `json:"platforms"`
	GroupsExisted    bool   `json:"groupsExisted"`
	PlatformsExisted bool   `json:"platformsExisted"`
	NextRevision     string `json:"nextRevision"`
	PreviousRevision string `json:"previousRevision"`
}

const recognitionJournalName = ".recognition-transaction.json"

func OpenRecognitionSettings(ctx context.Context, directory string) (_ *RecognitionSettings, resultErr error) {
	defer func() {
		if resultErr != nil {
			recognition.Deactivate()
		}
	}()
	s := &RecognitionSettings{directory: directory}
	if err := ctx.Err(); err != nil {
		return s, err
	}
	_, err := s.Reload(ctx)
	return s, err
}

func (s *RecognitionSettings) Read(ctx context.Context) (RecognitionDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, p, err := s.readFiles(ctx)
	if err != nil {
		return RecognitionDocument{}, err
	}
	d := RecognitionDocument{Groups: g.source, Platforms: p.source, Revision: recognition.Revision(g.source, p.source), ActiveRevision: s.current.Revision}
	if _, err := recognition.Compile(g.source, p.source); err != nil {
		d.LoadError = err.Error()
		errors.As(err, &d.Diagnostic)
	}
	return d, nil
}

func (s *RecognitionSettings) Reload(ctx context.Context) (RecognitionDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, p, err := s.readFiles(ctx)
	if err != nil {
		return RecognitionDocument{}, err
	}
	snapshot, err := recognition.Compile(g.source, p.source)
	if err != nil {
		return RecognitionDocument{}, err
	}
	if err := ctx.Err(); err != nil {
		return RecognitionDocument{}, err
	}
	if err := recognition.Activate(snapshot); err != nil {
		return RecognitionDocument{}, err
	}
	s.current = snapshot
	return RecognitionDocument{Groups: snapshot.Groups, Platforms: snapshot.Platforms, Revision: snapshot.Revision, ActiveRevision: snapshot.Revision}, nil
}

func (s *RecognitionSettings) Save(ctx context.Context, groups, platforms string) (RecognitionDocument, error) {
	snapshot, err := recognition.Compile(groups, platforms)
	if err != nil {
		return RecognitionDocument{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, p, err := s.readFiles(ctx)
	if err != nil {
		return RecognitionDocument{}, err
	}
	if !g.exists || !p.exists || g.source != groups || p.source != platforms {
		if err := s.writeFiles(ctx, g, p, groups, platforms); err != nil {
			return RecognitionDocument{}, err
		}
	} else if err := ctx.Err(); err != nil {
		return RecognitionDocument{}, err
	}
	if err := recognition.Activate(snapshot); err != nil {
		return RecognitionDocument{}, err
	}
	s.current = snapshot
	return RecognitionDocument{Groups: groups, Platforms: platforms, Revision: snapshot.Revision, ActiveRevision: snapshot.Revision}, nil
}

func (s *RecognitionSettings) readFiles(ctx context.Context) (recognitionFile, recognitionFile, error) {
	if err := s.recoverTransaction(ctx); err != nil {
		return recognitionFile{}, recognitionFile{}, err
	}
	g, err := readRecognitionFile(ctx, filepath.Join(s.directory, "release-groups.txt"))
	if err != nil {
		return recognitionFile{}, recognitionFile{}, err
	}
	p, err := readRecognitionFile(ctx, filepath.Join(s.directory, "platforms.txt"))
	return g, p, err
}

func readRecognitionFile(ctx context.Context, filePath string) (recognitionFile, error) {
	if err := ctx.Err(); err != nil {
		return recognitionFile{}, err
	}
	info, err := os.Lstat(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return recognitionFile{}, nil
	}
	if err != nil {
		return recognitionFile{}, fmt.Errorf("检查识别词表: %w", err)
	}
	if !info.Mode().IsRegular() {
		return recognitionFile{}, fmt.Errorf("%w：词表路径须为普通 TXT 文件", recognition.ErrConfig)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return recognitionFile{}, fmt.Errorf("读取识别词表: %w", err)
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, recognition.MaxTextBytes+1))
	if err != nil {
		return recognitionFile{}, fmt.Errorf("读取识别词表内容: %w", err)
	}
	if len(body) > recognition.MaxTextBytes {
		return recognitionFile{}, fmt.Errorf("%w：词表不能超过 256 KiB", recognition.ErrConfig)
	}
	if err := ctx.Err(); err != nil {
		return recognitionFile{}, err
	}
	return recognitionFile{source: string(body), exists: true}, nil
}

func (s *RecognitionSettings) writeFiles(ctx context.Context, oldGroups, oldPlatforms recognitionFile, groups, platforms string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.directory, 0700); err != nil {
		return fmt.Errorf("创建识别词表目录: %w", err)
	}
	groupPath, platformPath := filepath.Join(s.directory, "release-groups.txt"), filepath.Join(s.directory, "platforms.txt")
	gTemp, err := stageRecognitionFile(s.directory, groups)
	if err != nil {
		return err
	}
	defer os.Remove(gTemp)
	pTemp, err := stageRecognitionFile(s.directory, platforms)
	if err != nil {
		return err
	}
	defer os.Remove(pTemp)
	if err := ctx.Err(); err != nil {
		return err
	}
	journal := recognitionJournal{Version: 1, Groups: []byte(oldGroups.source), Platforms: []byte(oldPlatforms.source), GroupsExisted: oldGroups.exists, PlatformsExisted: oldPlatforms.exists, NextRevision: recognition.Revision(groups, platforms), PreviousRevision: recognition.Revision(oldGroups.source, oldPlatforms.source)}
	journalBytes, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("编码词表事务日志: %w", err)
	}
	journalTemp, err := stageRecognitionFile(s.directory, string(journalBytes))
	if err != nil {
		return err
	}
	defer os.Remove(journalTemp)
	journalPath := filepath.Join(s.directory, recognitionJournalName)
	if err := installRecognitionFile(journalTemp, journalPath, false); err != nil {
		return fmt.Errorf("保存词表事务日志: %w", err)
	}
	if err := installRecognitionFile(gTemp, groupPath, oldGroups.exists); err != nil {
		return fmt.Errorf("保存字幕组词表: %w", errors.Join(err, os.Remove(journalPath)))
	}
	if err := installRecognitionFile(pTemp, platformPath, oldPlatforms.exists); err != nil {
		// 第二个文件失败时恢复第一个，内存中的已激活快照始终不变。
		rollbackErr := restoreRecognitionFile(s.directory, groupPath, oldGroups)
		if rollbackErr == nil {
			rollbackErr = os.Remove(journalPath)
		}
		return fmt.Errorf("保存平台词表并恢复字幕组词表: %w", errors.Join(err, rollbackErr))
	}
	if err := os.Remove(journalPath); err != nil {
		return fmt.Errorf("确认词表事务提交: %w", err)
	}
	return nil
}

// recoverTransaction 只在管理器锁内调用；不完整事务先恢复，之后才能读取或激活词表。
func (s *RecognitionSettings) recoverTransaction(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	journalPath := filepath.Join(s.directory, recognitionJournalName)
	info, err := os.Lstat(journalPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查词表事务日志: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w：事务日志须为普通文件", recognition.ErrConfig)
	}
	body, readErr := readRecognitionJournalBody(journalPath)
	if readErr != nil {
		return fmt.Errorf("读取词表事务日志内容: %w", readErr)
	}
	var journal recognitionJournal
	decodeErr := json.Unmarshal(body, &journal)
	_, nextRevisionErr := hex.DecodeString(journal.NextRevision)
	if len(body) > 3*recognition.MaxTextBytes+4096 || decodeErr != nil || journal.Version != 1 || len(journal.Groups) > recognition.MaxTextBytes || len(journal.Platforms) > recognition.MaxTextBytes || len(journal.NextRevision) != 64 || nextRevisionErr != nil || journal.PreviousRevision != recognition.Revision(string(journal.Groups), string(journal.Platforms)) || !journal.GroupsExisted && len(journal.Groups) > 0 || !journal.PlatformsExisted && len(journal.Platforms) > 0 {
		return fmt.Errorf("%w：词表事务日志损坏，请保留日志并检查数据目录", recognition.ErrConfig)
	}
	groupPath, platformPath := filepath.Join(s.directory, "release-groups.txt"), filepath.Join(s.directory, "platforms.txt")
	g, err := readRecognitionFile(ctx, groupPath)
	if err != nil {
		return err
	}
	p, err := readRecognitionFile(ctx, platformPath)
	if err != nil {
		return err
	}
	if !g.exists || !p.exists || recognition.Revision(g.source, p.source) != journal.NextRevision {
		// 两个文件没有全部提交就恢复旧内容，绝不把混合版本当作一次新配置。
		gErr := restoreRecognitionFile(s.directory, groupPath, recognitionFile{source: string(journal.Groups), exists: journal.GroupsExisted})
		pErr := restoreRecognitionFile(s.directory, platformPath, recognitionFile{source: string(journal.Platforms), exists: journal.PlatformsExisted})
		if err := errors.Join(gErr, pErr); err != nil {
			return fmt.Errorf("恢复未完成的词表事务: %w", err)
		}
	}
	if err := os.Remove(journalPath); err != nil {
		return fmt.Errorf("清理已恢复的词表事务: %w", err)
	}
	return nil
}

func readRecognitionJournalBody(journalPath string) ([]byte, error) {
	f, err := os.Open(journalPath)
	if err != nil {
		return nil, fmt.Errorf("读取词表事务日志: %w", err)
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, 3*recognition.MaxTextBytes+4097))
}

func stageRecognitionFile(directory, source string) (string, error) {
	f, err := os.CreateTemp(directory, ".recognition-*")
	if err != nil {
		return "", fmt.Errorf("创建词表临时文件: %w", err)
	}
	name := f.Name()
	_, writeErr := io.Copy(f, strings.NewReader(source))
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("写入词表临时文件: %w", err)
	}
	return name, nil
}

func installRecognitionFile(temp, target string, exists bool) error {
	var err error
	if exists {
		err = os.Rename(temp, target)
	} else {
		err = os.Link(temp, target)
	}
	if err != nil {
		return fmt.Errorf("安装识别词表文件: %w", err)
	}
	return nil
}

func restoreRecognitionFile(directory, target string, previous recognitionFile) error {
	if !previous.exists {
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("移除未完成的词表文件: %w", err)
		}
		return nil
	}
	temp, err := stageRecognitionFile(directory, previous.source)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	return installRecognitionFile(temp, target, true)
}
