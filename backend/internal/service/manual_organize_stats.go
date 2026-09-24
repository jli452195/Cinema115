package service

import (
	"context"
	"time"
)

// Request phase durations include queue/cooldown waits. HTTPMS and WaitMS
// independently explain those phases and must not be added to them again.
type OrganizeStats struct {
	BatchSize         int       `json:"batchSize"`
	BatchesTotal      int       `json:"batchesTotal"`
	BatchesCompleted  int       `json:"batchesCompleted"`
	DirectoryRequests int       `json:"directoryRequests"`
	RenameRequests    int       `json:"renameRequests"`
	MoveRequests      int       `json:"moveRequests"`
	DirectoryCreates  int       `json:"directoryCreates"`
	HTTPRequests      int       `json:"httpRequests"`
	Checkpoints       int       `json:"checkpoints"`
	DirectoryMS       int64     `json:"directoryMs"`
	MutationMS        int64     `json:"mutationMs"`
	HTTPMS            int64     `json:"httpMs"`
	WaitMS            int64     `json:"waitMs"`
	SaveMS            int64     `json:"saveMs"`
	ElapsedMS         int64     `json:"elapsedMs"`
	StartedAt         time.Time `json:"startedAt"`
}

type organizeStatsKey struct{}

func organizeStatsFrom(ctx context.Context) *OrganizeStats {
	stats, _ := ctx.Value(organizeStatsKey{}).(*OrganizeStats)
	return stats
}
