package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/p115"
)

type InstantUploadConfig struct {
	SourceAccountID  string `json:"sourceAccountId"`
	AccountID        string `json:"accountId"`
	TargetCID        string `json:"targetCid"`
	TargetPath       string `json:"targetPath"`
	STRMBaseURL      string `json:"strmBaseUrl"`
	StrictSTRMOrigin bool   `json:"strictStrmOrigin"`
}

type InstantUploadRecord struct {
	ID              string    `json:"id"`
	SourceAccountID string    `json:"sourceAccountId"`
	AccountID       string    `json:"accountId"`
	TargetCID       string    `json:"targetCid"`
	Status          string    `json:"status"`
	Message         string    `json:"message"`
	Name            string    `json:"name"`
	Size            int64     `json:"size"`
	SHA1            string    `json:"sha1"`
	FileID          string    `json:"fileId,omitempty"`
	PickCode        string    `json:"pickCode,omitempty"`
	Challenged      bool      `json:"challenged"`
	StartedAt       time.Time `json:"startedAt"`
	DurationMS      int64     `json:"durationMs"`
}

type instantUploadDocument struct {
	Config  InstantUploadConfig   `json:"config"`
	Records []InstantUploadRecord `json:"records"`
}
type InstantUploadStatus struct {
	Config  InstantUploadConfig   `json:"config"`
	Records []InstantUploadRecord `json:"records"`
	Running bool                  `json:"running"`
}
type instantUploadClient interface {
	DownloadInfoWithUA(context.Context, *p115.ShareInfo, string, string, string) (*p115.DownloadInfo, error)
	InstantUpload(context.Context, *p115.DownloadInfo, string, string, func(p115.InstantProgress)) (*p115.InstantUploadResult, error)
}

// InstantUploads owns only explicit configuration and a bounded diagnostic history.
// Cookies, STRM URLs, share passwords and signed download URLs never enter its document.
type InstantUploads struct {
	mu       sync.Mutex
	store    accountSecretStore
	accounts *P115Accounts
	client   instantUploadClient
	logs     *EmbyProxyLogs
	doc      instantUploadDocument
	running  bool
}

var instantRunID = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,64}$`)
var ErrInstantBusy = errors.New("已有单文件秒传验证正在执行，请等待结果")
var ErrInstantConfig = errors.New("秒传配置或输入无效")

func NewInstantUploads(store *SessionStore, accounts *P115Accounts, client instantUploadClient, logs *EmbyProxyLogs) (*InstantUploads, error) {
	s := &InstantUploads{store: store, accounts: accounts, client: client, logs: logs, doc: instantUploadDocument{Records: []InstantUploadRecord{}}}
	if raw := store.Value(); raw != "" {
		if json.Unmarshal([]byte(raw), &s.doc) != nil || len(s.doc.Records) > 20 {
			return nil, fmt.Errorf("秒传验证配置或记录损坏")
		}
		changed := false
		for i := range s.doc.Records {
			if s.doc.Records[i].Status == "running" {
				s.doc.Records[i].Status = "uncertain"
				s.doc.Records[i].Message = "服务重启，结果未确认；请先检查目标目录"
				changed = true
			}
		}
		if changed {
			if err := s.saveLocked(s.doc); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *InstantUploads) Status() InstantUploadStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return InstantUploadStatus{Config: s.doc.Config, Records: append([]InstantUploadRecord{}, s.doc.Records...), Running: s.running}
}

func (s *InstantUploads) saveLocked(doc instantUploadDocument) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("编码秒传验证记录: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return fmt.Errorf("秒传验证配置或记录保存失败，请检查存储权限")
	}
	s.doc = doc
	return nil
}

func (s *InstantUploads) Apply(config InstantUploadConfig) error {
	if err := s.accounts.ValidateSelection(config.SourceAccountID); err != nil {
		return fmt.Errorf("%w：源账号：%v", ErrInstantConfig, err)
	}
	if err := s.accounts.ValidateSelection(config.AccountID); err != nil {
		return fmt.Errorf("%w：目标账号：%v", ErrInstantConfig, err)
	}
	if _, err := strconv.ParseUint(config.TargetCID, 10, 64); err != nil || config.TargetCID == "" {
		return fmt.Errorf("%w：请明确选择目标目录，根目录填写 0", ErrInstantConfig)
	}
	if len(config.TargetPath) > 2048 || strings.ContainsAny(config.TargetPath, "\r\n\x00") {
		return fmt.Errorf("%w：目录路径无效", ErrInstantConfig)
	}
	if config.StrictSTRMOrigin || config.STRMBaseURL != "" {
		base, err := normalizeWebhookBaseURL(config.STRMBaseURL)
		if err != nil {
			return fmt.Errorf("%w：请填写有效的 STRM 来源地址，严格模式下必填", ErrInstantConfig)
		}
		config.STRMBaseURL = base
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.doc
	doc.Config = config
	return s.saveLocked(doc)
}

func (s *InstantUploads) Run(ctx context.Context, expected InstantUploadConfig, id string, share *p115.ShareInfo, fileID string) (record InstantUploadRecord, resultErr error) {
	if !instantRunID.MatchString(id) || share == nil || fileID == "" {
		return record, ErrInstantConfig
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return record, ErrInstantBusy
	}
	for _, old := range s.doc.Records {
		if old.ID == id {
			s.mu.Unlock()
			return old, nil
		}
	}
	if s.doc.Config != expected {
		s.mu.Unlock()
		return record, fmt.Errorf("%w：配置已改变，请刷新后重新验证", ErrInstantConfig)
	}
	// Resolve each explicit account exactly once, then pin credentials for the whole operation.
	source, err := s.accounts.Get(expected.SourceAccountID)
	if err != nil || source.Cookie == "" {
		s.mu.Unlock()
		return record, fmt.Errorf("%w：源账号已删除或未登录", ErrInstantConfig)
	}
	target := source
	if expected.AccountID != expected.SourceAccountID {
		target, err = s.accounts.Get(expected.AccountID)
	}
	if err != nil || target.Cookie == "" {
		s.mu.Unlock()
		return record, fmt.Errorf("%w：目标账号已删除或未登录", ErrInstantConfig)
	}
	record = InstantUploadRecord{ID: id, SourceAccountID: source.ID, AccountID: target.ID, TargetCID: expected.TargetCID, Status: "running", Message: "正在验证", StartedAt: time.Now().UTC()}
	doc := s.doc
	doc.Records = append([]InstantUploadRecord{record}, doc.Records...)
	if len(doc.Records) > 20 {
		doc.Records = doc.Records[:20]
	}
	if err := s.saveLocked(doc); err != nil {
		s.mu.Unlock()
		return record, err
	}
	s.running = true
	s.mu.Unlock()
	ctx = s.logs.Trace(ctx)
	ctx, cancel := context.WithTimeout(ctx, 80*time.Second)
	defer cancel()
	defer func() {
		record.DurationMS = time.Since(record.StartedAt).Milliseconds()
		s.mu.Lock()
		doc := s.doc
		doc.Records = append([]InstantUploadRecord{}, doc.Records...)
		for i := range doc.Records {
			if doc.Records[i].ID == id {
				doc.Records[i] = record
			}
		}
		if err := s.saveLocked(doc); err != nil {
			resultErr = err
		}
		s.running = false
		s.mu.Unlock()
		level := "INFO"
		if record.Status == "failed" || record.Status == "uncertain" || resultErr != nil {
			level = "ERROR"
		} else if record.Status == "unsupported" {
			level = "WARNING"
		}
		s.logs.Add(ctx, level, "单文件秒传验证结束："+record.Message+"；耗时="+strconv.FormatInt(record.DurationMS, 10)+" ms")
		if resultErr != nil {
			s.logs.Add(ctx, "ERROR", "秒传验证结果未能保存，请先核查目标目录")
		}
	}()
	s.logs.Add(ctx, "INFO", "单文件秒传开始；源账号="+source.ID+"；目标账号="+target.ID+"；目录="+expected.TargetCID)
	info, err := s.client.DownloadInfoWithUA(ctx, share, fileID, source.Cookie, p115.PlaybackUserAgent)
	if err != nil {
		record.Status = "failed"
		record.Message = "源文件取链失败，请检查分享和源账号登录状态"
		return record, nil
	}
	if err := p115.ValidateInstantSource(info); err != nil {
		record.Status = "failed"
		record.Message = err.Error()
		return record, nil
	}
	record.Name, record.Size, record.SHA1 = info.Name, info.Size, info.SHA1
	s.logs.Add(ctx, "INFO", "源文件信息已取得，完整 SHA1 可用；大小="+strconv.FormatInt(info.Size, 10)+" 字节")
	result, err := s.client.InstantUpload(ctx, info, expected.TargetCID, target.Cookie, func(progress p115.InstantProgress) {
		switch progress.Stage {
		case "target":
			s.logs.Add(ctx, "DEBUG", "检查目标账号与缓存目录")
		case "init":
			s.logs.Add(ctx, "INFO", "向目标账号提交秒传初始化")
		case "range":
			s.logs.Add(ctx, "INFO", "115 要求二次校验，开始读取源直链片段；字节数="+strconv.FormatInt(progress.Bytes, 10))
		case "range_done":
			s.logs.Add(ctx, "INFO", "校验片段读取成功，范围与长度校验通过，已计算片段 SHA1")
		}
	})
	if err != nil {
		record.Status, record.Message = "failed", err.Error()
		if errors.Is(err, p115.ErrInstantUnsupported) {
			record.Status = "unsupported"
		}
		if errors.Is(err, p115.ErrInstantUncertain) {
			record.Status = "uncertain"
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			record.Message = "验证已取消或超时，请核查目标目录"
		}
		return record, nil
	}
	if result == nil {
		record.Status = "uncertain"
		record.Message = "未取得目标文件结果，请检查目标目录"
		return record, nil
	}
	record.Status, record.Message = "success", "115 已确认秒传成功，文件已存入目标目录"
	record.FileID, record.PickCode, record.Challenged = result.FileID, result.PickCode, result.Challenged
	return record, nil
}
