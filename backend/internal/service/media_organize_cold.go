package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

func mediaDecodeStoredRun(raw string) (MediaOrganizeRun, error) {
	type runFields MediaOrganizeRun
	var stored struct {
		runFields
		Rule json.RawMessage `json:"rule"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return MediaOrganizeRun{}, fmt.Errorf("解码整理批次: %w", err)
	}
	rule, err := mediaStoredRule(stored.Rule)
	if err != nil {
		return MediaOrganizeRun{}, err
	}
	run := MediaOrganizeRun(stored.runFields)
	run.Rule = rule
	run.scanScopes, run.StoredScanScopes = slices.Clone(run.StoredScanScopes), nil
	return mediaPersistentRun(run), nil
}

// 结束的历史只留在数据库；执行、排队和补扫交接中的证据仍保留在工作集。
func (s *MediaOrganizer) releaseHistoryLocked() {
	s.doc.Runs = slices.DeleteFunc(s.doc.Runs, func(run MediaOrganizeRun) bool {
		return !mediaRunActive(run.Status) && !s.busyRunLocked(run.ID) && len(s.inFlight[run.ID]) == 0 && !s.eventRecoveryPendingLocked(run.ID)
	})
	if len(s.doc.Runs) == 0 {
		s.doc.Runs = nil
	} else if cap(s.doc.Runs) > 2*len(s.doc.Runs) {
		// 批量管理临时载入大量历史后，也释放其切片底层数组。
		s.doc.Runs = slices.Clone(s.doc.Runs)
	}
}

// 变更冷记录时按批次临时载入，调用方在事务结束后释放；工作集中的新状态优先。
func (s *MediaOrganizer) includeHistoryLocked(raws []string) error {
	loaded := make(map[string]bool, len(s.doc.Runs)+len(raws))
	for _, run := range s.doc.Runs {
		loaded[run.ID] = true
	}
	for _, raw := range raws {
		var head struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &head); err != nil {
			return fmt.Errorf("读取整理批次编号: %w", err)
		}
		if loaded[head.ID] {
			continue
		}
		run, err := mediaDecodeStoredRun(raw)
		if err != nil {
			return err
		}
		s.doc.Runs = append(s.doc.Runs, run)
		loaded[run.ID] = true
	}
	return nil
}

func (s *MediaOrganizer) loadHistoryLocked(ctx context.Context, ids []string, ruleID, status string) error {
	raws, err := s.db.LoadMediaOrganizeRuns(ctx, ids, ruleID, status)
	if err != nil {
		return fmt.Errorf("读取整理历史: %w", err)
	}
	return s.includeHistoryLocked(raws)
}
