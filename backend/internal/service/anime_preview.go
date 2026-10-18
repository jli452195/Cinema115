package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type animePreviewRepository interface {
	GetAnimeSettings(context.Context) (model.AnimeSettings, error)
	CheckAnimePreviewConfig(context.Context, animepreview.Config) error
	GetAnimePreview(context.Context, string, string) (animepreview.Batch, error)
	CreateAnimePreview(context.Context, string, animepreview.Config, []storage.AnimePreviewSeed, int64) (animepreview.Batch, error)
	LoadAnimePreview(context.Context, string, storage.AnimePreviewRef, mikan.SourcePage, int64) (animepreview.Batch, error)
	SelectAnimePreview(context.Context, string, storage.AnimePreviewRef, []string, []string, int64) (animepreview.Batch, error)
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
	Config    animepreview.Config       `json:"config"`
	SourceRef string                    `json:"previewSourceRef"`
	Take      int                       `json:"take"`
	Refresh   bool                      `json:"refresh"`
	Reuse     []storage.AnimePreviewRef `json:"previewRefs"`
	Previous  *storage.AnimePreviewRef  `json:"previous"`
}

type AnimePreviewIssue struct {
	SourceRef string `json:"sourceRef"`
	Code      string `json:"code"`
}

type AnimePreviewResult struct {
	Batch   *animepreview.Batch    `json:"batch,omitempty"`
	Budget  animepreview.Budget    `json:"budget"`
	Sources []animepreview.Summary `json:"sources"`
	Issues  []AnimePreviewIssue    `json:"issues"`
}

func (s *AnimePreviewService) result(batch animepreview.Batch, issues []AnimePreviewIssue) AnimePreviewResult {
	out := AnimePreviewResult{Batch: &batch, Budget: batch.Budget(), Issues: issues, Sources: make([]animepreview.Summary, 0, len(batch.Sources))}
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

func findAnimePreview(b animepreview.Batch, ref storage.AnimePreviewRef) (animepreview.Source, error) {
	if b.ID != ref.BatchID {
		return animepreview.Source{}, animepreview.ErrInput
	}
	for _, source := range b.Sources {
		if source.ID == ref.PreviewID && source.Ref == ref.SourceRef {
			if source.LoadRevision != ref.LoadRevision {
				return source, animepreview.ErrConflict
			}
			return source, nil
		}
	}
	return animepreview.Source{}, animepreview.ErrInput
}

func (s *AnimePreviewService) Generate(ctx context.Context, actor string, in AnimePreviewInput) (AnimePreviewResult, error) {
	out := AnimePreviewResult{Budget: animepreview.Budget{Limit: 2000, Remaining: 2000}, Issues: []AnimePreviewIssue{}}
	if s.Repository == nil || s.Bindings == nil || s.Bindings.Mikan == nil || actor == "" || strings.TrimSpace(actor) != actor || len(actor) > 256 || len(in.Reuse) > 4 || in.Take < 0 || in.Take > 2000 {
		return out, animepreview.ErrInput
	}
	settings, err := s.Repository.GetAnimeSettings(ctx)
	if err != nil {
		return out, fmt.Errorf("read anime preview settings: %w", err)
	}
	if settings.Revision != in.Config.Settings.Revision {
		return out, animepreview.ErrConflict
	}
	in.Config.Settings = settings // Ignore browser-supplied account/UID/directory fields.
	c, err := animepreview.Normalize(in.Config)
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
		return out, animepreview.ErrInput
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
	if ref.ID != "" || ref.BangumiID != nil {
		entry, _, err := s.Bindings.resolveEntry(ctx, ref, "")
		if err != nil {
			return out, err
		}
		c.Chapter.ID, c.Chapter.BangumiID, c.Chapter.EditionKey, c.Chapter.Revision = entry.ID, entry.BangumiID, entry.EditionKey, entry.Revision
	}
	if err := s.Repository.CheckAnimePreviewConfig(ctx, c); err != nil {
		return out, err
	}
	for _, reuse := range in.Reuse {
		if reuse.SourceRef == in.SourceRef || seen[reuse.SourceRef] {
			return out, animepreview.ErrInput
		}
		seen[reuse.SourceRef] = true
		previous, err := s.Repository.GetAnimePreview(ctx, actor, reuse.BatchID)
		if err != nil {
			return out, fmt.Errorf("read referenced preview: %w", err)
		}
		if !animepreview.SameScope(c, previous.Config) {
			return out, animepreview.ErrInput
		}
		source, err := findAnimePreview(previous, reuse)
		if err != nil {
			return out, err
		}
		fp, err := animepreview.SourceFingerprint(c, reuse.SourceRef)
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
	var old *animepreview.Source
	if in.Previous != nil {
		previous, err := s.Repository.GetAnimePreview(ctx, actor, in.Previous.BatchID)
		if err != nil {
			return out, err
		}
		if !animepreview.SameScope(c, previous.Config) || in.Previous.SourceRef != in.SourceRef {
			return out, animepreview.ErrInput
		}
		source, err := findAnimePreview(previous, *in.Previous)
		if err != nil {
			return out, err
		}
		if source.Page.Source != (mikan.Source{ItemID: *target.ItemID, GroupID: *target.GroupID}) {
			return out, animepreview.ErrInput
		}
		old = &source
		loaded[source.Page.Source] = source.Page.RawCount
	}
	budget, err := NewAnimeMikanBudget(loaded)
	if err != nil {
		return out, err
	}
	take := in.Take
	if take == 0 {
		take = 50
	}
	page, readErr := s.Bindings.Mikan.ReadAnimeMikan(ctx, budget, AnimeMikanInput{Source: mikan.Source{ItemID: *target.ItemID, GroupID: *target.GroupID}, Kind: "history", Take: take, Refresh: in.Refresh})
	if readErr == nil {
		// GroupName is display metadata from the already verified entry cache.
		metadata, err := s.Bindings.Mikan.ReadAnimeMikanEntry(ctx, budget, *target.ItemID, false)
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
		fp, err := animepreview.SourceFingerprint(c, in.SourceRef)
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

func (s *AnimePreviewService) Load(ctx context.Context, actor string, ref storage.AnimePreviewRef, nextTake int) (AnimePreviewResult, error) {
	out := AnimePreviewResult{Issues: []AnimePreviewIssue{}}
	if s.Repository == nil || s.Bindings == nil || s.Bindings.Mikan == nil || nextTake < 1 || nextTake > 2000 {
		return out, animepreview.ErrInput
	}
	batch, err := s.Repository.GetAnimePreview(ctx, actor, ref.BatchID)
	if err != nil {
		return out, err
	}
	out = s.result(batch, out.Issues)
	source, err := findAnimePreview(batch, ref)
	if err != nil {
		return out, err
	}
	if !source.ValidAt(s.timestamp()) {
		return out, animepreview.ErrExpired
	}
	fp, err := animepreview.SourceFingerprint(batch.Config, source.Ref)
	if err != nil {
		return out, err
	}
	if source.Fingerprint != fp {
		return out, animepreview.ErrConflict
	}
	if nextTake <= source.Page.RequestedTake {
		return out, animepreview.ErrInput
	}
	if err := s.Repository.CheckAnimePreviewConfig(ctx, batch.Config); err != nil {
		return out, err
	}
	loaded := make(map[mikan.Source]int, len(batch.Sources))
	for _, source := range batch.Sources {
		loaded[source.Page.Source] = source.Page.RawCount
	}
	budget, err := NewAnimeMikanBudget(loaded)
	if err != nil {
		return out, err
	}
	previous := &AnimeMikanResult{SourcePage: source.Page}
	page, err := s.Bindings.Mikan.ReadAnimeMikan(ctx, budget, AnimeMikanInput{Source: source.Page.Source, Kind: "history", Take: nextTake, Previous: previous})
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.Issues = append(out.Issues, animePreviewReadIssue(source.Ref, err))
		return out, nil // Keep every previously successful source and choice.
	}
	updated, err := s.Repository.LoadAnimePreview(ctx, actor, ref, page.SourcePage, s.timestamp())
	if err != nil {
		return out, err
	}
	return s.result(updated, out.Issues), nil
}

func (s *AnimePreviewService) Select(ctx context.Context, actor string, ref storage.AnimePreviewRef, selected, dismissed []string) (animepreview.Batch, error) {
	if s.Repository == nil {
		return animepreview.Batch{}, animepreview.ErrInput
	}
	return s.Repository.SelectAnimePreview(ctx, actor, ref, selected, dismissed, s.timestamp())
}

func (s *AnimePreviewService) Releases(ctx context.Context, actor, batchID, sourceRef string, page, size int) (animepreview.Page, error) {
	if s.Repository == nil {
		return animepreview.Page{}, animepreview.ErrInput
	}
	b, err := s.Repository.GetAnimePreview(ctx, actor, batchID)
	if err != nil {
		return animepreview.Page{}, err
	}
	for _, source := range b.Sources {
		if source.Ref == sourceRef {
			return source.Releases(page, size, s.timestamp())
		}
	}
	return animepreview.Page{}, animepreview.ErrInput
}
