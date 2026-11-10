package service

import "context"

// 冷历史只在本次查询中使用，同批次以内存中的最新检查点覆盖，不进入常驻任务列表。
func (s *MediaOrganizer) mediaHistoryWithLive(ctx context.Context, records []string, ruleID, excludeID string) ([]MediaOrganizeRun, error) {
	runs := make([]MediaOrganizeRun, 0, len(records))
	positions := make(map[string]int, len(records))
	for _, raw := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		run, err := mediaDecodeStoredRun(raw)
		if err != nil {
			return nil, err
		}
		if run.ID == excludeID {
			continue
		}
		positions[run.ID] = len(runs)
		runs = append(runs, run)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, live := range s.doc.Runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if live.ID == excludeID || ruleID != "" && live.Rule.ID != ruleID {
			continue
		}
		var snapshot MediaOrganizeRun
		cloneMediaRun(live, &snapshot)
		if position, exists := positions[live.ID]; exists {
			runs[position] = snapshot
		} else {
			runs = append(runs, snapshot)
		}
	}
	return runs, ctx.Err()
}

func (s *MediaOrganizer) mediaReferenceHistory(ctx context.Context, run *MediaOrganizeRun, wanted map[string]bool, scopes []string, recursive bool) ([]MediaOrganizeRun, error) {
	parents := make([]string, 0, len(wanted))
	for parent := range wanted {
		parents = append(parents, parent)
	}
	records, err := s.db.LoadMediaOrganizeReferences(ctx, run.Rule.ID, parents, scopes, recursive)
	if err != nil {
		return nil, err
	}
	return s.mediaHistoryWithLive(ctx, records, run.Rule.ID, run.ID)
}
