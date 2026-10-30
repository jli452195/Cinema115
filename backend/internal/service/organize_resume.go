package service

import (
	"path"
	"strings"
)

// Only cloud-bound files can resume organization. Website paths are never
// interpreted as 115 IDs, and confirmed files are not scanned again.
func canContinueOrganize(job OrganizeJob) bool {
	if (job.Status != "partial" && job.Status != "interrupted") || len(job.Items) == 0 || !organizeID(job.Config.TargetCID) {
		return false
	}
	for _, item := range job.Items {
		if !organizeID(item.File.ID) || item.File.ID == "0" || !organizeID(item.File.ParentID) {
			return false
		}
		if job.Download != nil && !strings.HasPrefix(item.File.SourcePath, strings.TrimRight(job.Config.SourcePath, "/")+"/") {
			return false
		}
		if item.Directory != "" && (path.IsAbs(item.Directory) || path.Clean(item.Directory) != item.Directory || strings.HasPrefix(item.Directory, "..")) {
			return false
		}
	}
	return true
}
