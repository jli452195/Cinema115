package service

import (
	"context"
	"reflect"
	"sort"

	"strm115/backend/internal/model"
)

type embyMissingReport struct {
	groups     []EmbyMissingSeries
	checked    int
	incomplete int
}

// Keep derived reports and source identities, not the full episode index.
// All access holds the service lock followed by missingMu. Published reports
// are immutable so request filtering and poster decoration can run unlocked.
type embyMissingCache struct {
	scope, generation string
	revision          uint64
	exclusions        []model.EmbyMissingExclusion
	sources           map[string]string
	groups            map[string]embyMissingReport
	report            embyMissingReport
}

func (s *EmbyService) missingReportLocked(ctx context.Context, generation string, exclusions []model.EmbyMissingExclusion) (embyMissingReport, error) {
	s.missingMu.Lock()
	defer s.missingMu.Unlock()
	if err := s.refreshMissingLocked(ctx, generation, exclusions); err != nil {
		return embyMissingReport{}, err
	}
	return s.missing.report, nil
}

func (s *EmbyService) missingGroupItemsLocked(ctx context.Context, generation, key string, exclusions []model.EmbyMissingExclusion) ([]model.EmbyItem, error) {
	s.missingMu.Lock()
	if err := s.refreshMissingLocked(ctx, generation, exclusions); err != nil {
		s.missingMu.Unlock()
		return nil, err
	}
	ids := make([]string, 0)
	for id, identity := range s.missing.sources {
		if identity == key {
			ids = append(ids, id)
		}
	}
	s.missingMu.Unlock()
	if len(ids) == 0 {
		return nil, ErrEmbyMissingChanged
	}
	// Confirmation always reads current source evidence, including hidden versions.
	return s.db.EmbyMissingItems(ctx, embyScope(s.config), generation, ids)
}

func (s *EmbyService) refreshMissingLocked(ctx context.Context, generation string, exclusions []model.EmbyMissingExclusion) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := &s.missing
	scope := embyScope(s.config)
	full := c.scope != scope || c.generation != generation
	// Register/capture changes before reading rows. A later commit remains pending
	// even if its rows happen to be included in this calculation.
	revision, changed := s.db.EmbyMissingChanges(scope, generation, c.revision)
	if !full && !reflect.DeepEqual(c.exclusions, exclusions) {
		for _, list := range [][]model.EmbyMissingExclusion{c.exclusions, exclusions} {
			for _, exclusion := range list {
				changed = append(changed, exclusion.SeriesID)
			}
		}
	}
	if !full && len(changed) == 0 {
		return nil
	}
	oldKeys := make(map[string]bool, len(changed))
	var ids []string // nil means the initial/full published-index read.
	if !full {
		wanted := make(map[string]bool, len(changed))
		for _, id := range changed {
			wanted[id] = true
			if key, ok := c.sources[id]; ok {
				oldKeys[key] = true
			}
		}
		// Include every old sibling as well as current TMDB siblings from SQLite.
		// Rebinding or removing a source can split a previously merged report.
		for id, key := range c.sources {
			if oldKeys[key] {
				wanted[id] = true
			}
		}
		ids = make([]string, 0, len(wanted))
		for id := range wanted {
			ids = append(ids, id)
		}
	}
	items, err := s.db.EmbyMissingItems(ctx, scope, generation, ids)
	if err != nil {
		return err
	}
	sources, groups, err := buildMissingGroups(ctx, items, exclusions)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Publish only a complete successful calculation. Errors retain the previous
	// report and revision so pending changes are retried on the next request.
	if full {
		c.sources, c.groups = sources, groups
	} else {
		for key := range groups {
			oldKeys[key] = true
		}
		for id, key := range c.sources {
			if oldKeys[key] {
				delete(c.sources, id)
			}
		}
		for key := range oldKeys {
			delete(c.groups, key)
		}
		for id, key := range sources {
			c.sources[id] = key
		}
		for key, group := range groups {
			c.groups[key] = group
		}
	}
	report := embyMissingReport{groups: make([]EmbyMissingSeries, 0, len(c.groups))}
	for _, group := range c.groups {
		report.checked += group.checked
		report.incomplete += group.incomplete
		report.groups = append(report.groups, group.groups...)
	}
	sort.Slice(report.groups, func(i, j int) bool {
		if report.groups[i].Title != report.groups[j].Title {
			return report.groups[i].Title < report.groups[j].Title
		}
		return report.groups[i].Key < report.groups[j].Key
	})
	c.scope, c.generation, c.revision = scope, generation, revision
	c.exclusions, c.report = exclusions, report
	return nil
}

func buildMissingGroups(ctx context.Context, items []model.EmbyItem, exclusions []model.EmbyMissingExclusion) (map[string]string, map[string]embyMissingReport, error) {
	parents := make(map[string]string)
	for _, item := range items {
		if item.Type == "Series" {
			parents[item.ID] = item.TMDB
		}
	}
	sources := make(map[string]string, len(parents))
	groups := make(map[string]embyMissingReport, len(parents))
	for i, item := range items {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
		}
		id := item.SeriesID
		if item.Type == "Series" {
			id = item.ID
		}
		key, _ := embyMissingIdentity(id, parents[id])
		sources[id] = key
		group := groups[key]
		if item.Type == "Episode" && item.Season != 0 && (item.SeriesID == "" || item.Season < 0 || item.Episode < 0) {
			group.incomplete++
		}
		groups[key] = group
	}
	// Apply the exclusion list once, rather than once for every series.
	byKey := make(map[string][]model.EmbyItem, len(groups))
	for _, item := range filterEmbyMissingExclusions(items, exclusions) {
		id := item.SeriesID
		if item.Type == "Series" {
			id = item.ID
		}
		key := sources[id]
		byKey[key] = append(byKey[key], item)
	}
	for key, rows := range byKey {
		results, checked, err := buildEmbyMissing(ctx, rows)
		if err != nil {
			return nil, nil, err
		}
		group := groups[key]
		group.groups, group.checked = results, checked
		groups[key] = group
	}
	return sources, groups, nil
}
