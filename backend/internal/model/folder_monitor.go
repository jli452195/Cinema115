package model

import (
	"encoding/json"
	"time"
)

// FolderMonitor 保存管理员配置的 115 收件目录及批次检查点。
// 存储层以 JSON 持久化记录，检查点不保存 Cookie，也不依赖进程内存。
type FolderMonitor struct {
	ID                   string             `json:"id"`
	Name                 string             `json:"name"`
	AccountID            string             `json:"accountId"`
	SourceCID            string             `json:"sourceCID"`
	SourcePath           string             `json:"sourcePath"`
	OutputDir            string             `json:"outputDir"`
	BaseURL              string             `json:"baseUrl"`
	VideoExtensions      string             `json:"videoExtensions"`
	MetadataExtensions   string             `json:"metadataExtensions"`
	MinSizeMB            int64              `json:"minSizeMB"`
	Recursive            bool               `json:"recursive"`
	Enabled              bool               `json:"enabled"`
	ScheduleVersion      uint64             `json:"scheduleVersion,omitempty"`
	SchedulePaused       string             `json:"schedulePaused,omitempty"`
	ScheduleMessage      string             `json:"scheduleMessage,omitempty"`
	RecentRuns           []FolderRun        `json:"recentRuns,omitempty"`
	TaskPending          bool               `json:"taskPending,omitempty"`
	TaskTrigger          string             `json:"taskTrigger,omitempty"`
	LogTaskID            string             `json:"logTaskID,omitempty"`
	LogAttempt           int                `json:"logAttempt,omitempty"`
	GenerationTaskID     string             `json:"generationTaskId,omitempty"`
	GenerationSettings   json.RawMessage    `json:"generationSettings,omitempty"`
	EndedBatches         []EndedFolderBatch `json:"endedBatches,omitempty"`
	Running              bool               `json:"-"`
	IntervalSeconds      int                `json:"intervalSeconds"`
	Stage                string             `json:"stage"`
	ShareURL             string             `json:"shareURL,omitempty"`
	SharePassword        string             `json:"sharePassword,omitempty"`
	BatchIDs             []string           `json:"batchIDs,omitempty"`
	BatchDirectoryID     string             `json:"batchDirectoryID,omitempty"`
	BatchDirectoryName   string             `json:"batchDirectoryName,omitempty"`
	BatchCreationPending bool               `json:"batchCreationPending,omitempty"`
	RecoveryMessage      string             `json:"recoveryMessage,omitempty"`
	BatchMoveIndex       int                `json:"batchMoveIndex,omitempty"`
	BatchParentCID       string             `json:"batchParentCID,omitempty"`
	CleanupIndex         int                `json:"cleanupIndex,omitempty"`
	LastScanAt           time.Time          `json:"lastScanAt,omitempty"`
	NextScanAt           time.Time          `json:"nextScanAt,omitempty"`
	LastError            string             `json:"lastError,omitempty"`
	UpdatedAt            time.Time          `json:"updatedAt"`
}

type FolderRun struct {
	TaskID           string    `json:"taskId"`
	Trigger          string    `json:"trigger"`
	Status           string    `json:"status"`
	Message          string    `json:"message"`
	ShareURL         string    `json:"shareUrl,omitempty"`
	DirectoryID      string    `json:"directoryId,omitempty"`
	DirectoryName    string    `json:"directoryName,omitempty"`
	GenerationTaskID string    `json:"generationTaskId,omitempty"`
	Time             time.Time `json:"time"`
}

// EndedFolderBatch 保存用户结束批次时的文件位置和恢复信息。
type EndedFolderBatch struct {
	LogTaskID        string    `json:"logTaskId"`
	GenerationTaskID string    `json:"generationTaskId,omitempty"`
	AccountID        string    `json:"accountId"`
	SourceCID        string    `json:"sourceCID"`
	SourcePath       string    `json:"sourcePath"`
	DirectoryID      string    `json:"directoryId,omitempty"`
	DirectoryName    string    `json:"directoryName,omitempty"`
	ShareURL         string    `json:"shareUrl,omitempty"`
	Stage            string    `json:"stage"`
	LastError        string    `json:"lastError,omitempty"`
	Cleaned          bool      `json:"cleaned"`
	EndedAt          time.Time `json:"endedAt"`
}
