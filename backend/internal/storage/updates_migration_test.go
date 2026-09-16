package storage

import (
	"context"
	"testing"
)

func TestV18RepairsPartiallyAppliedV17(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE emby_state_v17 (
 scope TEXT PRIMARY KEY, active TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'idle',
 count INTEGER NOT NULL DEFAULT 0, scanned INTEGER NOT NULL DEFAULT 0, synced_at TEXT NOT NULL DEFAULT '',
 last_error TEXT NOT NULL DEFAULT '', webhook_at TEXT NOT NULL DEFAULT '', webhook_event TEXT NOT NULL DEFAULT '', webhook_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO emby_state_v17(scope,active,status,count,scanned,synced_at,last_error,webhook_at,webhook_event,webhook_error)
 SELECT scope,active,status,count,scanned,synced_at,last_error,webhook_at,webhook_event,webhook_error FROM emby_state;
DROP TABLE emby_state;
ALTER TABLE emby_state_v17 RENAME TO emby_state;
PRAGMA user_version=17;`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info(emby_state)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	for _, name := range []string{"updates_ready", "staging_generation", "staging_complete", "staging_cutoff", "updates_generation", "updates_cursor", "updates_started_at", "updates_error"} {
		if !columns[name] {
			t.Fatalf("v18 未修复字段 %s", name)
		}
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil || version != 18 {
		t.Fatal("迁移版本错误", version, err)
	}
}
