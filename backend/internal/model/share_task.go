package model

import (
	"encoding/json"
	"time"
)

// ShareTask 是普通分享和转存分享共用的持久任务日志。
type ShareTask struct {
	ID           string          `json:"id"`
	Source       string          `json:"source"`
	SourceURL    string          `json:"sourceUrl"`
	ShareURL     string          `json:"shareUrl"`
	Mode         string          `json:"mode"`
	Status       string          `json:"status"`
	Written      int             `json:"written"`
	Total        int             `json:"total"`
	Error        string          `json:"error"`
	Note         string          `json:"note"`
	Retries      int             `json:"retries"`
	MaxRetries   int             `json:"maxRetries"`
	RetryMinutes int             `json:"retryMinutes"`
	NextRetryAt  *time.Time      `json:"nextRetryAt"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	Elapsed      float64         `json:"elapsed"`
	Request      json.RawMessage `json:"-"`
}
