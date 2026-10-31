package storage

import (
	"database/sql"
	"sync"
)

// Only the active report scope is observed. Repeated events replace a source's
// revision instead of retaining an unbounded event history.
type embyMissingChanges struct {
	mu         sync.Mutex
	scope      string
	generation string
	revision   uint64
	series     map[string]uint64
}

// EmbyMissingChanges observes the single active report before reading its rows.
// Its caller advances the cursor only after its replacement report is complete.
func (s *Store) EmbyMissingChanges(scope, generation string, since uint64) (uint64, []string) {
	c := &s.embyChanges
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.series == nil || c.scope != scope || c.generation != generation {
		c.scope, c.generation = scope, generation
		c.revision, c.series = 0, make(map[string]uint64)
	}
	ids := make([]string, 0)
	for id, revision := range c.series {
		if revision > since {
			ids = append(ids, id)
		} else {
			delete(c.series, id)
		}
	}
	return c.revision, ids
}

func (s *Store) changedEmbyMissing(scope, generation string, series map[string]bool) {
	if len(series) == 0 {
		return
	}
	c := &s.embyChanges
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.series == nil || c.scope != scope || c.generation != generation {
		return
	}
	c.revision++
	for id := range series {
		c.series[id] = c.revision
	}
}

func addEmbyMissingSource(series map[string]bool, kind, id, parent string) {
	switch kind {
	case "Series":
		series[id] = true
	case "Episode":
		// Empty SeriesID groups incomplete orphan records for the notice count.
		series[parent] = true
	}
}

func scanEmbyMissingSources(rows *sql.Rows, series map[string]bool) (int64, error) {
	defer rows.Close()
	var count int64
	for rows.Next() {
		var kind, id, parent string
		if err := rows.Scan(&kind, &id, &parent); err != nil {
			return 0, err
		}
		addEmbyMissingSource(series, kind, id, parent)
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return count, rows.Close()
}
