package model

import (
	"encoding/json"
	"time"
)

// FolderMonitor stores one administrator-owned 115 inbox monitor. Record is
// persisted by the storage layer as JSON so a batch checkpoint can evolve
// without exposing cookies or relying on process memory.
type FolderMonitor struct {
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	AccountID            string          `json:"accountId"`
	SourceCID            string          `json:"sourceCID"`
	SourcePath           string          `json:"sourcePath"`
	OutputDir            string          `json:"outputDir"`
	BaseURL              string          `json:"baseUrl"`
	VideoExtensions      string          `json:"videoExtensions"`
	MetadataExtensions   string          `json:"metadataExtensions"`
	MinSizeMB            int64           `json:"minSizeMB"`
	Recursive            bool            `json:"recursive"`
	Enabled              bool            `json:"enabled"`
	TaskPending          bool            `json:"taskPending,omitempty"`
	TaskTrigger          string          `json:"taskTrigger,omitempty"`
	LogTaskID            string          `json:"logTaskID,omitempty"`
	LogAttempt           int             `json:"logAttempt,omitempty"`
	GenerationTaskID     string          `json:"generationTaskId,omitempty"`
	GenerationSettings   json.RawMessage `json:"generationSettings,omitempty"`
	Running              bool            `json:"-"`
	IntervalSeconds      int             `json:"intervalSeconds"`
	Stage                string          `json:"stage"`
	ShareURL             string          `json:"shareURL,omitempty"`
	SharePassword        string          `json:"sharePassword,omitempty"`
	ShareReviewing       bool            `json:"shareReviewing,omitempty"`
	BatchIDs             []string        `json:"batchIDs,omitempty"`
	BatchFingerprint     string          `json:"batchFingerprint,omitempty"`
	BatchDirectoryID     string          `json:"batchDirectoryID,omitempty"`
	BatchDirectoryName   string          `json:"batchDirectoryName,omitempty"`
	BatchMoveIndex       int             `json:"batchMoveIndex,omitempty"`
	BatchParentCID       string          `json:"batchParentCID,omitempty"`
	BatchAccountRevision uint64          `json:"batchAccountRevision,omitempty"`
	BatchCredentialHash  string          `json:"batchCredentialHash,omitempty"`
	CleanupIndex         int             `json:"cleanupIndex,omitempty"`
	StableFingerprint    string          `json:"stableFingerprint,omitempty"`
	StableScans          int             `json:"stableScans,omitempty"`
	LastScanAt           time.Time       `json:"lastScanAt,omitempty"`
	NextScanAt           time.Time       `json:"nextScanAt,omitempty"`
	LastError            string          `json:"lastError,omitempty"`
	UpdatedAt            time.Time       `json:"updatedAt"`
}
