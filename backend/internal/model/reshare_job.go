package model

import "time"

// ReshareJob 保存云端操作检查点；Finished 表示执行结束，不等于清理成功。
type ReshareJob struct {
	Stats         *ReshareStats `json:"stats,omitempty"`
	SourceURL     string        `json:"sourceURL,omitempty"`
	SourceCID     string        `json:"sourceCID,omitempty"`
	LastError     string        `json:"lastError,omitempty"`
	Finished      bool          `json:"finished"`
	ID            string        `json:"id"`
	DirectoryName string        `json:"directoryName"`
	DirectoryID   string        `json:"directoryID,omitempty"`
	ShareURL      string        `json:"shareURL,omitempty"`
	Status        string        `json:"status"`
	UpdatedAt     time.Time     `json:"updatedAt"`
}

// ReshareStats contains measurements only, never credentials or directory contents.
type ReshareStats struct {
	SourceFiles int                               `json:"sourceFiles"`
	StageMS     map[string]int64                  `json:"stageMs"`
	ScanRounds  int                               `json:"scanRounds"`
	WaitMS      int64                             `json:"waitMs"`
	Directories map[string]*ReshareDirectoryStats `json:"directories"`
}

type ReshareDirectoryStats struct {
	Phase      string `json:"phase"`
	CID        string `json:"cid"`
	Requests   int    `json:"requests"`
	FirstPages int    `json:"firstPages"`
	FullReads  int    `json:"fullReads"`
	Retries    int    `json:"retries"`
	Probes     int    `json:"probes"`
	RequestMS  int64  `json:"requestMs"`
}
