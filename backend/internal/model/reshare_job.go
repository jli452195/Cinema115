package model

import "time"

// ReshareJob 保存云端操作检查点；Finished 表示执行结束，不等于清理成功。
type ReshareJob struct {
	SourceURL     string    `json:"sourceURL,omitempty"`
	SourceCID     string    `json:"sourceCID,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
	Finished      bool      `json:"finished"`
	ID            string    `json:"id"`
	DirectoryName string    `json:"directoryName"`
	DirectoryID   string    `json:"directoryID,omitempty"`
	ShareURL      string    `json:"shareURL,omitempty"`
	Status        string    `json:"status"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
