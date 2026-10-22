package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
)

const animeOrganizedReleases = `SELECT x.snapshot_json FROM anime_job_selections x JOIN anime_download_jobs j ON j.id=x.job_id WHERE x.source_id=? AND x.status='authorized' AND j.workflow_state='organized' AND NOT EXISTS(SELECT 1 FROM anime_download_jobs newer WHERE newer.claim_id=j.claim_id AND newer.attempt>j.attempt)`

func animeProgressRows(ctx context.Context, q animeReader, query, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0, 32)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}

// Reused by materialized progress and automatic completion: one source's
// authorized snapshot owns its episode number, even for a shared torrent.
func animeOrganizedEpisodes(ctx context.Context, raw []string, source model.AnimeSource) (map[int]bool, error) {
	match, err := animepreview.CompileMatcher(source.Binding, source.Rules)
	if err != nil {
		return nil, err
	}
	out := make(map[int]bool, len(raw))
	for _, row := range raw {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var snapshot model.AnimeDownloadSnapshot
		if err := decodeAnimeJSON(row, &snapshot); err != nil {
			return nil, err
		}
		if !sameAnimeEpisodeRules(snapshot.Decision.Rules, source.Rules) {
			continue
		}
		ok, err := match(snapshot.Release)
		if err != nil {
			return nil, err
		}
		p := snapshot.Release.Parse
		if ok && p.Kind == "episode" && p.Episode != nil {
			out[*p.Episode] = true
		}
	}
	return out, nil
}

func sameAnimeEpisodeRules(a, b model.AnimeRules) bool {
	return a.EpisodePattern == b.EpisodePattern && a.EpisodeGroup == b.EpisodeGroup
}

func animeSourceProgress(ctx context.Context, source model.AnimeSource, sub model.AnimeSubscription, entry model.AnimeEntry, observed, organized []string) (model.AnimeSourceProgress, error) {
	out := model.AnimeSourceProgress{State: "unknown"}
	if source.Rules.MinimumEpisode == nil || source.CheckedAt == nil || source.Binding.Validate(true) != nil || source.Rules.Validate(true) != nil {
		return out, nil
	}
	out.State, out.FirstEpisode = "ongoing", *source.Rules.MinimumEpisode
	if scope := source.Binding.Scope; scope != nil && scope.First != nil && scope.Kind == "range" {
		out.FirstEpisode = max(out.FirstEpisode, *scope.First)
	}
	total := 0
	if entry.Metadata.TotalEpisodes != nil {
		total = *entry.Metadata.TotalEpisodes
	}
	if sub.Form.TotalEpisodes != nil {
		total = *sub.Form.TotalEpisodes
	}
	first, last, targetKnown := animeCompletionRange(sub, source, total)
	if targetKnown {
		out.FirstEpisode = first
	}
	match, err := animepreview.CompileMatcher(source.Binding, source.Rules)
	if err != nil {
		return out, err
	}
	for _, raw := range observed {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var d model.AnimeDecisionSnapshot
		if err := decodeAnimeJSON(raw, &d); err != nil {
			return out, err
		}
		// A changed numbering rule must not reuse an old interpretation as new
		// publication evidence. Discovery will refresh it using the saved rule.
		if !sameAnimeEpisodeRules(d.Rules, source.Rules) {
			continue
		}
		ok, err := match(model.AnimeRelease{Title: d.Title, Parse: d.Parse})
		if err != nil {
			return out, err
		}
		if ok && d.Parse.Episode != nil {
			out.PublishedThrough = max(out.PublishedThrough, *d.Parse.Episode)
		}
	}
	found, err := animeOrganizedEpisodes(ctx, organized, source)
	if err != nil {
		return out, err
	}
	for n := range found {
		out.PublishedThrough = max(out.PublishedThrough, n)
	}
	if targetKnown {
		out.PublishedThrough = min(out.PublishedThrough, last)
	}
	for n := range found {
		if n >= out.FirstEpisode && n <= out.PublishedThrough {
			out.OrganizedCount++
		}
	}
	out.Missing = animepreview.EpisodeGaps(out.FirstEpisode, out.PublishedThrough, found)
	for _, gap := range out.Missing {
		out.MissingCount += gap.Last - gap.First + 1
	}
	if targetKnown && out.PublishedThrough == last && out.MissingCount == 0 {
		out.State = "complete"
	} else if out.MissingCount > 0 {
		out.State = "missing"
	}
	return out, nil
}

// RefreshAnimeSourceProgress processes at most one invalidated source. Read
// a consistent local snapshot, then release SQLite before parsing its history.
// The conditional write discards a result if any input changed in the meantime.
func (s *Store) RefreshAnimeSourceProgress(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id string
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT p.source_id,p.generation FROM anime_source_progress p JOIN anime_subscription_sources s ON s.id=p.source_id WHERE p.generation<>p.computed_generation AND s.status<>'removed' ORDER BY p.source_id LIMIT 1`).Scan(&id, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	source, err := scanAnimeSource(tx.QueryRowContext(ctx, `SELECT `+animeSourceColumns+` FROM anime_subscription_sources WHERE id=?`, id))
	if err != nil {
		return false, err
	}
	sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, source.SubscriptionID))
	if err != nil {
		return false, err
	}
	entry, err := scanAnimeEntry(tx.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE id=?`, sub.EntryID))
	if err != nil {
		return false, err
	}
	observed, err := animeProgressRows(ctx, tx, `SELECT snapshot_json FROM anime_release_decisions WHERE source_id=?`, id)
	if err != nil {
		return false, err
	}
	organized, err := animeProgressRows(ctx, tx, animeOrganizedReleases, id)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	progress, err := animeSourceProgress(ctx, source, sub, entry, observed, organized)
	if err != nil {
		return false, fmt.Errorf("compute anime source progress: %w", err)
	}
	raw, err := animeJSON(progress)
	if err != nil {
		return false, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE anime_source_progress SET computed_generation=?,summary_json=? WHERE source_id=? AND generation=?`, generation, raw, id, generation)
	return true, err
}

// One indexed batch for a page. Dirty results are deliberately neutral; a
// previous configuration's completion badge must not leak into the new one.
// List responses do not load or send the potentially longer missing ranges.
func (s *Store) AnimeSourceProgresses(ctx context.Context, ids []string, details bool) (map[string]model.AnimeSourceProgress, error) {
	out := make(map[string]model.AnimeSourceProgress, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	raw, err := animeJSON(ids)
	if err != nil {
		return nil, err
	}
	projection := `json_remove(summary_json,'$.missing')`
	if details {
		projection = `summary_json`
	}
	rows, err := s.db.QueryContext(ctx, `SELECT source_id,CASE WHEN generation=computed_generation THEN `+projection+` ELSE '{"state":"unknown"}' END FROM anime_source_progress WHERE source_id IN (SELECT value FROM json_each(?))`, raw)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, data string
		var progress model.AnimeSourceProgress
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		if err := decodeAnimeJSON(data, &progress); err != nil {
			return nil, err
		}
		out[id] = progress
	}
	return out, rows.Err()
}
