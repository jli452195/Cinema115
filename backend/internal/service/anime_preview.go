package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type animePreviewRepository interface {
	GetAnimeSettings(context.Context) (model.AnimeSettings, error)
	CheckAnimePreviewConfig(context.Context, preview.Config) error
	GetAnimePreview(context.Context, string, string) (preview.Batch, error)
	CreateAnimePreview(context.Context, string, preview.Config, []storage.AnimePreviewSeed, int64) (preview.Batch, error)
	SelectAnimePreview(context.Context, string, storage.AnimePreviewRef, []string, []string, int64) (preview.Batch, error)
}

// Actor must come from the authenticated boundary, never from request JSON.
// This service has no worker, 115 client, baseline or execution-write interface.
type AnimePreviewService struct {
	Repository animePreviewRepository
	Bindings   *AnimeBindingService
	now        func() time.Time
}

func (s *AnimePreviewService) timestamp() int64 {
	if s.now != nil {
		return s.now().UTC().UnixMilli()
	}
	return time.Now().UTC().UnixMilli()
}

type AnimePreviewInput struct {
	Config    preview.Config            `json:"config"`
	SourceRef string                    `json:"previewSourceRef"`
	Refresh   bool                      `json:"refresh"`
	Reuse     []storage.AnimePreviewRef `json:"previewRefs"`
	Previous  *storage.AnimePreviewRef  `json:"previous"`
}

type AnimePreviewIssue struct {
	SourceRef string `json:"sourceRef"`
	Code      string `json:"code"`
}

type AnimePreviewResult struct {
	SelectionNotice string              `json:"selectionNotice,omitempty"`
	Batch           *preview.Batch      `json:"batch,omitempty"`
	Budget          preview.Budget      `json:"budget"`
	Sources         []preview.Summary   `json:"sources"`
	Issues          []AnimePreviewIssue `json:"issues"`
}

func (s *AnimePreviewService) result(batch preview.Batch, issues []AnimePreviewIssue) AnimePreviewResult {
	out := AnimePreviewResult{Batch: &batch, Budget: batch.Budget(), Issues: issues, Sources: make([]preview.Summary, 0, len(batch.Sources))}
	for _, source := range batch.Sources {
		out.Sources = append(out.Sources, source.Summary(s.timestamp()))
	}
	return out
}

func animePreviewReadIssue(ref string, err error) AnimePreviewIssue {
	code := "SOURCE_READ_FAILED"
	var limit *mikan.LimitError
	if errors.As(err, &limit) {
		if limit.Kind == "rows" {
			code = "PREVIEW_ROW_BUDGET"
		} else {
			code = "SOURCE_READ_BUDGET"
		}
	}
	return AnimePreviewIssue{ref, code}
}

func findAnimePreview(b preview.Batch, ref storage.AnimePreviewRef) (preview.Source, error) {
	if b.ID != ref.BatchID {
		return preview.Source{}, preview.ErrInput
	}
	for _, source := range b.Sources {
		if source.ID == ref.PreviewID && source.Ref == ref.SourceRef {
			if source.LoadRevision != ref.LoadRevision {
				return source, preview.ErrConflict
			}
			return source, nil
		}
	}
	return preview.Source{}, preview.ErrInput
}

func (s *AnimePreviewService) Generate(ctx context.Context, actor string, in AnimePreviewInput) (AnimePreviewResult, error) {
	out := AnimePreviewResult{Budget: preview.Budget{Limit: 2000, Remaining: 2000}, Issues: []AnimePreviewIssue{}}
	if s.Repository == nil || s.Bindings == nil || actor == "" || strings.TrimSpace(actor) != actor || len(actor) > 256 || len(in.Reuse) > 4 {
		return out, preview.ErrInput
	}
	settings, err := s.Repository.GetAnimeSettings(ctx)
	if err != nil {
		return out, fmt.Errorf("read anime preview settings: %w", err)
	}
	if settings.Revision != in.Config.Settings.Revision {
		return out, preview.ErrConflict
	}
	in.Config.Settings = settings // Ignore browser-supplied account/UID/directory fields.
	c, err := preview.Normalize(in.Config)
	if err != nil {
		return out, err
	}
	var target model.AnimeSourceConfig
	for _, source := range c.Sources {
		if source.Ref == in.SourceRef {
			target = source
			break
		}
	}
	if target.ItemID == nil || target.GroupID == nil {
		return out, preview.ErrInput
	}
	seeds := make([]storage.AnimePreviewSeed, 0, len(in.Reuse)+1)
	loaded := make(map[mikan.Source]int, len(in.Reuse)+1)
	seen := make(map[string]bool, len(in.Reuse)+1)
	// Resolve the local/external chapter from trusted metadata, without relying
	// on an upstream page for an existing local chapter's continued existence.
	ref := AnimeEntryRef{ID: c.Chapter.ID}
	if ref.ID == "" {
		ref.BangumiID, ref.EditionKey = c.Chapter.BangumiID, c.Chapter.EditionKey
	}
	missingSubject := false
	if ref.ID != "" || ref.BangumiID != nil {
		entry, _, err := s.Bindings.resolveEntry(ctx, ref, "")
		missingSubject = ref.ID == "" && errors.Is(err, bangumi.ErrNotFound)
		if err != nil && !missingSubject {
			return out, err
		}
		if !missingSubject {
			c.Chapter.ID, c.Chapter.BangumiID, c.Chapter.EditionKey, c.Chapter.Revision = entry.ID, entry.BangumiID, entry.EditionKey, entry.Revision
		}
	}
	if err := s.Repository.CheckAnimePreviewConfig(ctx, c); err != nil {
		return out, err
	}
	for _, reuse := range in.Reuse {
		if reuse.SourceRef == in.SourceRef || seen[reuse.SourceRef] {
			return out, preview.ErrInput
		}
		seen[reuse.SourceRef] = true
		previous, err := s.Repository.GetAnimePreview(ctx, actor, reuse.BatchID)
		if err != nil {
			return out, fmt.Errorf("read referenced preview: %w", err)
		}
		if !preview.SameScope(c, previous.Config) {
			return out, preview.ErrInput
		}
		source, err := findAnimePreview(previous, reuse)
		if err != nil {
			return out, err
		}
		fp, err := preview.SourceFingerprint(c, reuse.SourceRef)
		if err != nil {
			return out, err
		}
		if !source.ValidAt(s.timestamp()) {
			out.Issues = append(out.Issues, AnimePreviewIssue{reuse.SourceRef, "PREVIEW_EXPIRED"})
			continue
		}
		if fp != source.Fingerprint {
			out.Issues = append(out.Issues, AnimePreviewIssue{reuse.SourceRef, "REPREVIEW_REQUIRED"})
			continue
		}
		copy := reuse
		seeds = append(seeds, storage.AnimePreviewSeed{SourceRef: reuse.SourceRef, Previous: &copy})
		loaded[source.Page.Source] = source.Page.RawCount
	}
	var old *preview.Source
	if in.Previous != nil {
		previous, err := s.Repository.GetAnimePreview(ctx, actor, in.Previous.BatchID)
		if err != nil {
			return out, err
		}
		if !preview.SameScope(c, previous.Config) || in.Previous.SourceRef != in.SourceRef {
			return out, preview.ErrInput
		}
		source, err := findAnimePreview(previous, *in.Previous)
		if err != nil {
			return out, err
		}
		if source.Page.Source != (animefeed.NewSource(target.Provider, *target.ItemID, *target.GroupID)) {
			return out, preview.ErrInput
		}
		old = &source
		loaded[source.Page.Source] = source.Page.RawCount
	}
	budget, err := NewAnimeSourceBudget(loaded)
	if err != nil {
		return out, err
	}
	if missingSubject {
		read, err := s.Bindings.ReadEntry(ctx, budget, target.Provider, *target.ItemID, false)
		if err != nil {
			return out, err
		}
		if _, err := animeEntryFromSource(ref, read.Entry, true); err != nil {
			return out, err
		}
	}
	source := animefeed.NewSource(target.Provider, *target.ItemID, *target.GroupID)
	take := max(1, min(animeSourceHistoryLimit(target.Provider), budget.allowance(source)))
	page, readErr := s.Bindings.ReadSource(ctx, budget, AnimeSourceInput{Source: source, Kind: "history", Take: take, Refresh: in.Refresh})
	if readErr == nil {
		// GroupName is display metadata from the already verified entry cache.
		metadata, err := s.Bindings.ReadEntry(ctx, budget, target.Provider, *target.ItemID, false)
		if err != nil {
			readErr = err
		} else {
			for i := range c.Sources {
				if c.Sources[i].Ref == target.Ref {
					for _, g := range metadata.Entry.Groups {
						if g.ID == *target.GroupID {
							name := g.Name
							c.Sources[i].GroupName = &name
						}
					}
				}
			}
		}
	}
	if readErr == nil {
		seeds = append(seeds, storage.AnimePreviewSeed{SourceRef: in.SourceRef, Page: &page.SourcePage, Previous: in.Previous})
	} else {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Issues = append(out.Issues, animePreviewReadIssue(in.SourceRef, readErr))
		fp, err := preview.SourceFingerprint(c, in.SourceRef)
		if err != nil {
			return out, err
		}
		if old != nil && old.ValidAt(s.timestamp()) && old.Fingerprint == fp {
			seeds = append(seeds, storage.AnimePreviewSeed{SourceRef: in.SourceRef, Previous: in.Previous})
		}
	}
	if len(seeds) == 0 {
		return out, fmt.Errorf("generate anime source preview: %w", readErr)
	}
	batch, err := s.Repository.CreateAnimePreview(ctx, actor, c, seeds, s.timestamp())
	if err != nil {
		return out, err
	}
	return s.result(batch, out.Issues), nil
}

func (s *AnimePreviewService) Select(ctx context.Context, actor string, ref storage.AnimePreviewRef, selected, dismissed []string) (preview.Batch, error) {
	if s.Repository == nil {
		return preview.Batch{}, preview.ErrInput
	}
	return s.Repository.SelectAnimePreview(ctx, actor, ref, selected, dismissed, s.timestamp())
}

func (s *AnimePreviewService) Releases(ctx context.Context, actor, batchID, sourceRef string) (preview.Page, error) {
	if s.Repository == nil {
		return preview.Page{}, preview.ErrInput
	}
	b, err := s.Repository.GetAnimePreview(ctx, actor, batchID)
	if err != nil {
		return preview.Page{}, err
	}
	for _, source := range b.Sources {
		if source.Ref == sourceRef {
			return source.Releases(s.timestamp()), nil
		}
	}
	return preview.Page{}, preview.ErrInput
}
