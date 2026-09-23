package storage

import (
	"context"
	"fmt"
)

// 数量按当前类型和关键词统计全部状态；管理员按影视计数，个人仅统计自己的有效订阅。
// 空用户编号仅供已通过管理员鉴权的管理接口调用。
func (s *Store) FollowCounts(ctx context.Context, userID, kind, query string) (map[string]int, error) {
	q := `SELECT ` + effectiveFollowStatus + `,COUNT(*) FROM follow_requests r` + followCheckJoin + `WHERE EXISTS(SELECT 1 FROM user_follows f WHERE f.kind=r.kind AND f.tmdb_id=r.tmdb_id AND f.active=1`
	args := make([]any, 0, 4)
	if userID != "" {
		q += " AND f.user_id=?"
		args = append(args, userID)
	}
	q += ")"
	if kind != "" {
		q += " AND r.kind=?"
		args = append(args, kind)
	}
	if query != "" {
		q += " AND (instr(lower(r.title),lower(?))>0 OR CAST(r.tmdb_id AS TEXT)=?)"
		args = append(args, query, query)
	}
	q += " GROUP BY " + effectiveFollowStatus
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("统计追更状态: %w", err)
	}
	defer rows.Close()
	counts := map[string]int{"pending": 0, "processing": 0, "caught_up": 0, "closed": 0, "completed": 0}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	counts["ongoing"] = counts["pending"] + counts["processing"] + counts["caught_up"]
	return counts, nil
}
