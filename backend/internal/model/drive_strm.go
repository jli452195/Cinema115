package model

import "time"

// DriveSTRMSettings applies to directory STRM only. Each scan freezes a copy.
type DriveSTRMSettings struct {
	BaseURL                string `json:"baseUrl"`
	VideoExtensions        string `json:"videoExtensions"`
	MetadataExtensions     string `json:"metadataExtensions"`
	MinSizeMB              int64  `json:"minSizeMB"`
	EventIntervalSeconds   int    `json:"eventIntervalSeconds"`
	ReconcileEnabled       bool   `json:"reconcileEnabled"`
	ReconcileIntervalHours int    `json:"reconcileIntervalHours"`
	FullScanEnabled        bool   `json:"fullScanEnabled"`
	FullScanIntervalHours  int    `json:"fullScanIntervalHours"`
	MetadataRateEnabled    bool   `json:"metadataRateEnabled"`
	MetadataFilesPerSecond int    `json:"metadataFilesPerSecond"`
}

// Seed defaults before decoding JSON; switches retain their selected periods.
func DefaultDriveSTRMSettings() DriveSTRMSettings {
	return DriveSTRMSettings{EventIntervalSeconds: 20, ReconcileEnabled: true, ReconcileIntervalHours: 24, FullScanIntervalHours: 720, MetadataRateEnabled: true, MetadataFilesPerSecond: 2}
}

// DriveSTRM mirrors an existing primary-account directory without modifying it.
// Credentials are resolved only when a scan starts and are never persisted here.
type DriveSTRM struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	AccountID          string            `json:"accountId"`
	SourceCID          string            `json:"sourceCID"`
	SourcePath         string            `json:"sourcePath"`
	OutputDir          string            `json:"outputDir"`
	Enabled            bool              `json:"enabled"`
	Settings           DriveSTRMSettings `json:"-"`
	Status             string            `json:"status"`
	Mode               string            `json:"mode,omitempty"`
	Queued             bool              `json:"queued"`
	FullScan           bool              `json:"fullScan,omitempty"`
	PendingDirectories []string          `json:"pendingDirectories,omitempty"`
	LastScanAt         time.Time         `json:"lastScanAt,omitempty"`
	LastReconcileAt    time.Time         `json:"lastReconcileAt,omitempty"`
	LastFullScanAt     time.Time         `json:"lastFullScanAt,omitempty"`
	NextScanAt         time.Time         `json:"nextScanAt,omitempty"`
	LastError          string            `json:"lastError,omitempty"`
	FailureCount       int               `json:"failureCount,omitempty"`
	MetadataPending    int               `json:"metadataPending"`
	MetadataFailed     int               `json:"metadataFailed"`
	Stats              DriveSTRMStats    `json:"stats"`
	UpdatedAt          time.Time         `json:"updatedAt"`
}

type DriveSTRMStats struct {
	Directories     int `json:"directories"`
	Videos          int `json:"videos"`
	Written         int `json:"written"`
	Unchanged       int `json:"unchanged"`
	MetadataWritten int `json:"metadataWritten"`
	Failed          int `json:"failed"`
}

// DriveSTRMFile is ownership evidence for one managed output or indexed
// directory. Digest covers local bytes, and never represents verified playback
// material or a cloud file hash. Missing local files are always recreated.
type DriveSTRMFile struct {
	MappingID string `json:"mappingId"`
	FileID    string `json:"fileId"`
	ParentCID string `json:"parentCID"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Digest    string `json:"digest,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// DriveSTRMAttachment freezes one accepted download without persisting its
// credentials. Mutable queue state lives in dedicated database columns.
type DriveSTRMAttachment struct {
	ID            int64             `json:"-"`
	Record        DriveSTRMFile     `json:"record"`
	AccountID     string            `json:"accountId"`
	SourceCID     string            `json:"sourceCID"`
	OutputDir     string            `json:"outputDir"`
	Settings      DriveSTRMSettings `json:"settings"`
	Name          string            `json:"name"`
	PickCode      string            `json:"pickCode"`
	SHA1          string            `json:"sha1"`
	Size          int64             `json:"size"`
	Attempts      int               `json:"-"`
	NextAttemptAt time.Time         `json:"-"`
	LastError     string            `json:"-"`
}

// DriveSTRMCleanup retains previous local ownership after a successful move.
// The worker verifies these saved bytes before deleting an obsolete output.
type DriveSTRMCleanup struct {
	ID            int64         `json:"-"`
	Record        DriveSTRMFile `json:"record"`
	Attempts      int           `json:"-"`
	NextAttemptAt time.Time     `json:"-"`
	LastError     string        `json:"-"`
}
