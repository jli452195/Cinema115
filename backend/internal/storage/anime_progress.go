package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"

	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/model"
	"strm115/backend/internal/utils/torrentmeta"
)

const animeOrganizedReleases = `SELECT x.snapshot_json FROM anime_job_selections x JOIN anime_download_jobs j ON j.id=x.job_id WHERE x.source_id=? AND x.status='authorized' AND j.workflow_state='organized' AND NOT EXISTS(SELECT 1 FROM anime_download_jobs newer WHERE newer.claim_id=j.claim_id AND newer.attempt>j.attempt)`

// Correlated with source alias s. Dirty summaries never suppress RSS reads.
const animeSourceComplete = `EXISTS(SELECT 1 FROM anime_source_progress g WHERE g.source_id=s.id AND g.generation=g.computed_generation AND json_extract(g.summary_json,'$.state')='complete')`

func animeProgressRows(ctx context.Context, q animeReader, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
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

func sameAnimeEpisodeRules(a, b model.AnimeRules) bool {
	return a.EpisodePattern == b.EpisodePattern && a.EpisodeGroup == b.EpisodeGroup
}

func animeSourceProgress(ctx context.Context, source model.AnimeSource, sub model.AnimeSubscription, entry model.AnimeEntry, observed, organized []string) (model.AnimeSourceProgress, error) {
	out := model.AnimeSourceProgress{State: "unknown"}
	if source.Rules.MinimumEpisode == nil || source.CheckedAt == nil || source.Binding.Validate(true) != nil || source.Rules.Validate(true) != nil {
		return out, nil
	}
	total := 0
	if entry.Metadata.TotalEpisodes != nil {
		total = *entry.Metadata.TotalEpisodes
	}
	if sub.Form.TotalEpisodes != nil {
		total = *sub.Form.TotalEpisodes
	}
	out.State = "ongoing"
	first, zero := -1, 0
	rules := source.Rules
	rules.MinimumEpisode = &zero
	match, err := preview.CompileMatcher(source.Binding, rules)
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
		releases, err := animeProgressReleases(source.Binding, rules, model.AnimeRelease{Title: d.Title, Parse: d.Parse}, d.BatchFiles)
		if err != nil {
			return out, err
		}
		for _, release := range releases {
			ok := d.Parse.Kind == "batch" // Batch files already passed the contextual matcher.
			if !ok {
				ok, err = match(release)
			}
			if err != nil {
				return out, err
			}
			if ok && release.Parse.Episode != nil {
				if first < 0 || *release.Parse.Episode < first {
					first = *release.Parse.Episode
				}
				out.PublishedThrough = max(out.PublishedThrough, *release.Parse.Episode)
			}
		}
	}
	// Use the same compiled matcher for observed titles and organized receipts.
	found := make(map[int]bool, len(organized))
	for _, raw := range organized {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var snapshot model.AnimeDownloadSnapshot
		if err := decodeAnimeJSON(raw, &snapshot); err != nil {
			return out, err
		}
		if !sameAnimeEpisodeRules(snapshot.Decision.Rules, source.Rules) {
			continue
		}
		releases, err := animeProgressReleases(source.Binding, rules, snapshot.Release, snapshot.BatchFiles)
		if err != nil {
			return out, err
		}
		for _, release := range releases {
			ok := snapshot.Release.Parse.Kind == "batch"
			if !ok {
				ok, err = match(release)
			}
			if err != nil {
				return out, err
			}
			if p := release.Parse; ok && p.Kind == "episode" && p.Episode != nil {
				found[*p.Episode] = true
			}
		}
	}
	for n := range found {
		if first < 0 || n < first {
			first = n
		}
		out.PublishedThrough = max(out.PublishedThrough, n)
	}
	if first < 0 {
		return out, nil
	}
	first, last, targetKnown := animeCompletionRange(source, first, total)
	out.FirstEpisode = first
	if targetKnown {
		out.PublishedThrough = min(out.PublishedThrough, last)
	}
	for n := range found {
		if n >= out.FirstEpisode && n <= out.PublishedThrough {
			out.OrganizedCount++
		}
	}
	out.Missing = preview.EpisodeGaps(out.FirstEpisode, out.PublishedThrough, found)
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

func animeProgressReleases(binding model.AnimeBinding, rules model.AnimeRules, release model.AnimeRelease, files []model.AnimeBatchFile) ([]model.AnimeRelease, error) {
	if release.Parse.Kind != "batch" {
		return []model.AnimeRelease{release}, nil
	}
	if len(files) == 0 {
		return nil, nil
	}
	manifest := make([]torrentmeta.File, 0, len(files))
	for _, file := range files {
		manifest = append(manifest, torrentmeta.File{Path: file.Path, Size: file.Size})
	}
	matched, err := preview.MatchBatchFiles(binding, rules, release.Title, manifest)
	if err != nil {
		return nil, err
	}
	out := make([]model.AnimeRelease, 0, len(matched))
	for _, file := range matched {
		out = append(out, model.AnimeRelease{Title: path.Base(file.Path), Parse: file.Parse})
	}
	return out, nil
}

func readAnimeSourceProgress(ctx context.Context, q animeReader, source model.AnimeSource, sub model.AnimeSubscription, entry model.AnimeEntry) (model.AnimeSourceProgress, error) {
	observed, err := animeProgressRows(ctx, q, `SELECT snapshot_json FROM anime_release_decisions WHERE source_id=?`, source.ID)
	if err != nil {
		return model.AnimeSourceProgress{}, err
	}
	organized, err := animeProgressRows(ctx, q, animeOrganizedReleases, source.ID)
	if err != nil {
		return model.AnimeSourceProgress{}, err
	}
	return animeSourceProgress(ctx, source, sub, entry, observed, organized)
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
