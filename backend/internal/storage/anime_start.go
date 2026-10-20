package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
)

// StartAnimeConfiguration commits configuration, history decisions and the
// scheduler baseline together. Preview data is read locally; no RSS/115 I/O is
// performed and any failure rolls back the entire subscription.
func (s *Store) StartAnimeConfiguration(ctx context.Context, in model.AnimeSubscriptionConfig, key AnimeWriteKey, guard AnimeConfigGuard, expected animepreview.Config, refs []AnimeActivationSource, target model.AnimeDownloadTarget) (model.AnimeConfigReceipt, error) {
	var out model.AnimeConfigReceipt
	if err := key.validate(); err != nil {
		return out, err
	}
	if in.Status != "paused" || target.Validate() != nil || len(refs) < 1 || len(refs) > model.AnimeMaxSources {
		return out, ErrAnimeInvalid
	}
	expected, err := animepreview.Normalize(expected)
	if err != nil {
		return out, err
	}
	if expected.SubscriptionID != in.ID || expected.Revision != in.ExpectedRevision || expected.Chapter.EditionKey != guard.Entry.EditionKey || !sameAnimeString(expected.Chapter.BangumiID, guard.Entry.BangumiID) || expected.Chapter.ID != "" && expected.Chapter.ID != guard.Entry.ID {
		return out, animepreview.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("begin anime subscription start: %w", err)
	}
	defer tx.Rollback()
	if found, err := readAnimeActionReceiptTx(ctx, tx, key, &out); found || err != nil {
		return out, err
	}
	if err := validateAnimePreviewConfigTx(ctx, tx, expected); err != nil {
		return out, err
	}
	// A source may reuse a preview from another batch if that source's exact
	// rules and form scope still match. Changes to another source need no RSS read.
	previews := make(map[string]animepreview.Source, len(refs))
	for _, ref := range refs {
		if _, exists := previews[ref.SourceRef]; exists || ref.SelectedIDs == nil || ref.DismissedIDs == nil {
			return out, ErrAnimeInvalid
		}
		b, err := readAnimePreviewTx(ctx, tx, key.ActorKey, ref.BatchID)
		if err != nil {
			return out, err
		}
		if !animepreview.SameScope(expected, b.Config) || b.Config.Revision != expected.Revision || b.Config.Chapter.Revision != expected.Chapter.Revision {
			return out, animepreview.ErrConflict
		}
		preview, err := previewReferencedSource(b, ref.AnimePreviewRef)
		if err != nil {
			return out, err
		}
		fingerprint, err := animepreview.SourceFingerprint(expected, ref.SourceRef)
		if err != nil || fingerprint != preview.Fingerprint {
			return out, animepreview.ErrConflict
		}
		previews[ref.SourceRef] = preview
	}
	out, err = saveAnimeConfigurationTx(ctx, tx, in, nil, &guard)
	if err != nil {
		return out, err
	}
	byID := make(map[string]animepreview.Source, len(refs))
	stableRefs := make([]AnimeActivationSource, 0, len(refs))
	for _, ref := range refs {
		id, exists := out.SourceIDs[ref.SourceRef]
		if !exists {
			return out, ErrAnimeInvalid
		}
		preview := previews[ref.SourceRef]
		// Only local identity/version fields change. The releases, decisions,
		// observations, T0 and absolute expiry remain the reviewed snapshot.
		preview.Ref, preview.SourceID, preview.SourceRevision = id, id, out.SourceRevisions[id]
		byID[id] = preview
		ref.SourceRef = id
		stableRefs = append(stableRefs, ref)
	}
	activated, err := enableAnimeSubscriptionTx(ctx, tx, out.ID, out.Revision, target, stableRefs, key, func(_ context.Context, _ *sql.Tx, source model.AnimeSource, _ AnimeActivationSource) (animepreview.Source, error) {
		preview, ok := byID[source.ID]
		if !ok {
			return preview, animepreview.ErrConflict
		}
		return preview, nil
	})
	if err != nil {
		return out, err
	}
	out.Status, out.Revision, out.RequiresRepreview = activated.Status, activated.Revision, false
	for _, id := range activated.ResumedSourceIDs {
		out.SourceRevisions[id]++
	}
	if err := writeAnimeActionReceiptTx(ctx, tx, key, out, time.Now().UnixMilli()); err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("commit anime subscription start: %w", err)
	}
	return out, nil
}
