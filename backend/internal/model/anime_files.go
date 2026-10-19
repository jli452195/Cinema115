package model

import (
	"fmt"
	"strings"
	"unicode"
)

// One fixed create intent per download job. It is not a second task queue.
type AnimeDirectoryOperation struct {
	SchemaVersion int    `json:"schemaVersion"`
	State         string `json:"state"`
	Generation    int64  `json:"generation"`
	CID           string `json:"cid,omitempty"`
	ErrorCode     string `json:"errorCode,omitempty"`
	AuthorizedAt  int64  `json:"authorizedAt"`
}

type AnimeDriveEntry struct {
	ID        string `json:"id"`
	ParentCID string `json:"parentCid"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
}

func (e AnimeDriveEntry) Valid() bool {
	if !ValidAnimeExternalID(e.ID) || e.ParentCID != "0" && !ValidAnimeExternalID(e.ParentCID) || e.ID == e.ParentCID || strings.TrimSpace(e.Name) == "" || len(e.Name) > 1024 || e.Name == "." || e.Name == ".." || strings.ContainsAny(e.Name, "/\\") || strings.ContainsFunc(e.Name, unicode.IsControl) || e.Size < 0 {
		return false
	}
	if e.Kind == "directory" {
		return e.Size == 0
	}
	return e.Kind == "file"
}

// The resource root comes from the saved create receipt. Entries are collected
// once from scoped listings; no per-file metadata or content hashes are needed.
type AnimeFileManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	UID           string            `json:"uid"`
	ResultID      string            `json:"resultId"`
	CollectedAt   int64             `json:"collectedAt"`
	Entries       []AnimeDriveEntry `json:"entries"`
}

func (m AnimeFileManifest) Validate(job AnimeDownloadJob) error {
	if m.SchemaVersion != 1 || m.UID != job.UID || m.CollectedAt <= 0 || job.DirectoryCID == nil || job.ResultObjectID == nil || m.ResultID != *job.ResultObjectID || len(m.Entries) < 2 || len(m.Entries) > 512 {
		return fmt.Errorf("invalid anime file manifest")
	}
	entries := make(map[string]AnimeDriveEntry, len(m.Entries))
	files, dirs := 0, 0
	for _, entry := range m.Entries {
		if !entry.Valid() {
			return fmt.Errorf("invalid anime drive entry")
		}
		if _, exists := entries[entry.ID]; exists {
			return fmt.Errorf("duplicate anime drive entry")
		}
		entries[entry.ID] = entry
		if entry.Kind == "file" {
			files++
		} else {
			dirs++
		}
	}
	root, ok := entries[*job.DirectoryCID]
	if !ok || root.Kind != "directory" || root.ParentCID != job.ParentCID || root.Name != job.DirectoryName || files == 0 || dirs > 32 {
		return fmt.Errorf("invalid anime resource root or empty result")
	}
	result, ok := entries[m.ResultID]
	if !ok || result.ID != root.ID && result.ParentCID != root.ID {
		return fmt.Errorf("anime result is outside resource directory")
	}
	for _, entry := range m.Entries {
		if entry.ID == root.ID {
			continue
		}
		current := entry
		for depth := 0; ; depth++ {
			if current.ID == result.ID {
				break
			}
			parent, exists := entries[current.ParentCID]
			if depth >= 8 || !exists || parent.Kind != "directory" || current.ParentCID == root.ID && result.ID != root.ID {
				return fmt.Errorf("anime file tree is incomplete or ambiguous")
			}
			current = parent
		}
	}
	return nil
}

func (job AnimeDownloadJob) CanCollectFiles() bool {
	if job.WorkflowState != "active" || job.DownloadState != "completed" || job.FileState == "verified" || job.DirectoryCID == nil || job.ResultObjectID == nil {
		return false
	}
	if job.BlockCode == nil {
		return true
	}
	switch *job.BlockCode {
	case "FILES_READ_FAILED", "FILES_INCOMPLETE", "TASK_MISSING", "TASK_READ_FAILED":
		return true
	default:
		return false
	}
}
