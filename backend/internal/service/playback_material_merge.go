package service

import (
	"fmt"
	"strings"
)

// Concurrent target UIDs may learn different origins/ranges for one material.
// Merge while holding the document mutex, before committing the transaction.
func mergePlaybackMaterial(old, incoming PlaybackMaterial) (PlaybackMaterial, error) {
	merged := clonePlaybackMaterials([]PlaybackMaterial{old})[0]
	if incoming.VerifiedAt.After(merged.VerifiedAt) {
		merged.VerifiedAt = incoming.VerifiedAt
	}
	for _, origin := range incoming.Origins {
		found := false
		for _, v := range merged.Origins {
			if v == origin {
				found = true
				break
			}
		}
		if !found {
			merged.Origins = append(merged.Origins, origin)
		}
	}
	for _, r := range incoming.Ranges {
		found := false
		for _, v := range merged.Ranges {
			if v.Start == r.Start && v.End == r.End {
				if !strings.EqualFold(v.SHA1, r.SHA1) {
					return old, fmt.Errorf("已验证校验片段冲突，请重新核验文件资料")
				}
				found = true
				break
			}
		}
		if !found {
			merged.Ranges = append(merged.Ranges, r)
		}
	}
	if len(merged.Origins) > 64 {
		merged.Origins = merged.Origins[len(merged.Origins)-64:]
	}
	if len(merged.Ranges) > 64 {
		merged.Ranges = merged.Ranges[len(merged.Ranges)-64:]
	}
	return merged, nil
}
