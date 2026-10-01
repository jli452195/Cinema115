package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// AutomationSettings 在服务端持久保存，网页和机器人读取同一份开关。
type AutomationSettings struct {
	AccountID          string        `json:"accountId"`
	BotID              string        `json:"botId"`
	TelegramEnabled    bool          `json:"telegramEnabled"`
	ReshareEnabled     bool          `json:"reshareEnabled"`
	TGBatchWaitSeconds int           `json:"tgBatchWaitSeconds"`
	FixedSharePassword string        `json:"fixedSharePassword"`
	ParentCID          string        `json:"parentCID"`
	ParentPath         string        `json:"parentPath,omitempty"` // Display hint only; directory operations use ParentCID.
	ReviewRetryEnabled bool          `json:"reviewRetryEnabled"`
	ReviewRetryMinutes int           `json:"reviewRetryMinutes"`
	ReviewRetryMax     int           `json:"reviewRetryMax"`
	Generation         GenerateInput `json:"generation"`
}

type AutomationStore struct {
	PrimaryID func() string
	// 启动时装配，保存时校验账号配置，不在读取旧设置时阻止服务启动。
	ValidateAccount func(string) error
	ValidateBot     func(string, bool) error
	mu              sync.RWMutex
	path, root      string
	value           AutomationSettings
}

func NewAutomationStore(path, root, baseURL string) (*AutomationStore, error) {
	s := &AutomationStore{path: path, root: root, value: AutomationSettings{ParentCID: "0", Generation: GenerateInput{BaseURL: baseURL, OutputDir: "strm", VideoExtensions: DefaultVideoExtensions, Recursive: true}}}
	s.value.ReviewRetryMinutes, s.value.ReviewRetryMax = 30, 6
	s.value.TGBatchWaitSeconds = 5
	s.value.Generation.MetadataExtensions = DefaultMetadataExtensions
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取自动化配置: %w", err)
	}
	if err := json.Unmarshal(data, &s.value); err != nil {
		return nil, fmt.Errorf("自动化配置损坏: %w", err)
	}
	if err := s.validate(s.value); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *AutomationStore) Value() AutomationSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := s.value
	if s.PrimaryID != nil {
		if id := s.PrimaryID(); value.AccountID != id {
			value.AccountID = id
			value.ParentCID = ""
			value.ParentPath = ""
		}
	}
	return value
}

// ValidateCurrentAccount 校验当前主账号，不回退到小号。
func (s *AutomationStore) ValidateCurrentAccount() error {
	value := s.Value()
	if s.ValidateAccount == nil {
		return generationInputError("115 账号校验服务尚未初始化")
	}
	return s.ValidateAccount(value.AccountID)
}

func (s *AutomationStore) ValidateCurrentBot() error {
	value := s.Value()
	if !value.TelegramEnabled {
		return nil
	}
	if s.ValidateBot == nil {
		return generationInputError("Telegram Bot 校验服务尚未初始化")
	}
	return s.ValidateBot(value.BotID, true)
}

var extensionsPattern = regexp.MustCompile(`^[a-zA-Z0-9.,，\s]+$`)

// ValidateGenerationOptions 在转存之前检查所有本地生成选项。
func ValidateGenerationOptions(root string, input GenerateInput) error {
	if _, err := normalizeSharePath(input.SharePath); err != nil {
		return err
	}
	videoTypes := make(map[string]bool)
	videoRaw := input.VideoExtensions
	if strings.TrimSpace(videoRaw) == "" {
		videoRaw = DefaultVideoExtensions
	}
	for _, ext := range strings.Split(strings.ReplaceAll(videoRaw, "，", ","), ",") {
		videoTypes["."+strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))] = true
	}
	if _, err := parseMetadataExtensions(input.MetadataExtensions, videoTypes); err != nil {
		return err
	}
	if _, err := ValidateBaseURL(input.BaseURL); err != nil {
		return err
	}
	if _, err := ResolveOutputDirectory(root, input.OutputDir); err != nil {
		return err
	}
	if input.MinSizeMB < 0 || input.MinSizeMB > 1<<30 {
		return generationInputError("最小文件大小超出范围")
	}
	if input.CID != "" {
		if _, err := strconv.ParseUint(input.CID, 10, 64); err != nil {
			return generationInputError("起始目录 ID 应为数字")
		}
	}
	if len(input.VideoExtensions) > 1024 {
		return generationInputError("视频后缀配置过长")
	}
	if value := strings.TrimSpace(input.VideoExtensions); value != "" {
		if !extensionsPattern.MatchString(value) {
			return generationInputError("视频后缀格式无效")
		}
		valid := false
		for _, ext := range strings.Split(strings.ReplaceAll(value, "，", ","), ",") {
			ext = strings.TrimPrefix(strings.TrimSpace(ext), ".")
			if ext == "" {
				continue
			}
			for _, r := range ext {
				if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
					return generationInputError("视频后缀格式无效")
				}
			}
			valid = true
		}
		if !valid {
			return generationInputError("请至少填写一个视频后缀")
		}
	}
	return nil
}

func (s *AutomationStore) validate(value AutomationSettings) error {
	if len(value.AccountID) > 64 || strings.ContainsAny(value.AccountID, "\r\n\x00") {
		return generationInputError("115 配置编号无效")
	}
	if len(value.BotID) > 64 || strings.ContainsAny(value.BotID, "\r\n\x00") {
		return generationInputError("Telegram Bot 配置编号无效")
	}
	if value.TGBatchWaitSeconds < 0 || value.TGBatchWaitSeconds > 300 {
		return generationInputError("批次收集等待应为 0～300 秒，0 表示不等待")
	}
	if value.ReviewRetryMinutes < 1 || value.ReviewRetryMinutes > 1440 || value.ReviewRetryMax < 1 || value.ReviewRetryMax > 100 {
		return generationInputError("审核重试间隔应为 1～1440 分钟，最大重试次数应为 1～100")
	}
	if value.FixedSharePassword != "" && !sharePasswordPattern.MatchString(value.FixedSharePassword) {
		return generationInputError("固定分享密码应为 4 位英文字母或数字，留空则随机生成")
	}
	if _, err := strconv.ParseUint(value.ParentCID, 10, 64); err != nil {
		return generationInputError("转存父目录 ID 应为数字，根目录使用 0")
	}
	if len(value.ParentPath) > 4096 || strings.ContainsAny(value.ParentPath, "\r\n\x00") {
		return generationInputError("转存目录显示路径无效")
	}
	return ValidateGenerationOptions(s.root, value.Generation)
}

func (s *AutomationStore) Save(value AutomationSettings) error {
	if s.PrimaryID != nil {
		id := s.PrimaryID()
		if value.AccountID != "" && value.AccountID != id {
			return generationInputError("主账号已变化，请重新选择转存目录")
		}
		value.AccountID = id
	}
	// 兼容未传新字段的旧客户端，零值采用默认配置。
	if value.ReviewRetryMinutes == 0 {
		value.ReviewRetryMinutes = 30
	}
	if value.ReviewRetryMax == 0 {
		value.ReviewRetryMax = 6
	}
	value.FixedSharePassword = strings.TrimSpace(value.FixedSharePassword)
	if value.ParentCID == "0" {
		value.ParentPath = "/"
	}
	// 配置仅保存生成选项，不保存上一次收到的分享链接及提取码。
	value.Generation.URL = ""
	value.Generation.Password = ""
	value.Generation.CID = ""
	value.Generation.Mode = ""
	if err := s.validate(value); err != nil {
		return err
	}
	if s.ValidateAccount != nil {
		if err := s.ValidateAccount(value.AccountID); err != nil {
			return err
		}
	}
	if value.TelegramEnabled {
		if s.ValidateBot == nil {
			return generationInputError("Telegram Bot 校验服务尚未初始化")
		}
		if err := s.ValidateBot(value.BotID, true); err != nil {
			return generationInputError(err.Error())
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeAutomationJSON(s.path, value); err != nil {
		return err
	}
	s.value = value
	return nil
}

// MigrateTelegramBotSelection 只用于把旧单 Bot 一次性固化为分享功能绑定。
func (s *AutomationStore) MigrateTelegramBotSelection(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.value.BotID != "" {
		return nil
	}
	value := s.value
	value.BotID, value.TelegramEnabled = id, enabled
	if err := s.validate(value); err != nil {
		return err
	}
	if err := writeAutomationJSON(s.path, value); err != nil {
		return err
	}
	s.value = value
	return nil
}

// 原子替换防止断电或进程退出时留下半份配置和任务记录。
func writeAutomationJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("编码自动化数据: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("创建自动化数据目录: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".automation-*")
	if err != nil {
		return fmt.Errorf("创建自动化临时文件: %w", err)
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return fmt.Errorf("写入自动化数据: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("保存自动化数据: %w", err)
	}
	return nil
}
