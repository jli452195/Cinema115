package service

import (
	"context"
	"fmt"
	"strings"

	"strm115/backend/internal/animeorganize"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/storage"
)

type AnimeNamingPreviewInput struct {
	Target  model.AnimeNamingTarget  `json:"target"`
	Mapping model.AnimeSourceMapping `json:"mapping"`
	Group   string                   `json:"group"`
	Items   []AnimeNamingPreviewFile `json:"items"`
}

type AnimeNamingPreviewFile struct {
	ID    string                  `json:"id"`
	Title string                  `json:"title"`
	Parse model.AnimeReleaseParse `json:"parse"`
}

type AnimeNamingPreviewItem struct {
	ID string `json:"id"`
	model.AnimeNamingProjection
	Result *naming.Result `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type AnimeNamingPreview struct {
	Naming naming.Snapshot          `json:"naming"`
	Items  []AnimeNamingPreviewItem `json:"items"`
}

// The supplied metadata is only a preview, never authority to save a subscription.
// It uses no drive, RSS or metadata requests; saving still verifies the target.
func PreviewAnimeNaming(ctx context.Context, templates NamingTemplates, in AnimeNamingPreviewInput) (AnimeNamingPreview, error) {
	plan := model.AnimeNamingPlan{SchemaVersion: 1, Target: in.Target, Sources: []model.AnimeSourceMapping{in.Mapping}}
	if plan.Validate() != nil || len(in.Items) == 0 || len(in.Items) > 2000 || len(in.Group) > 512 || templates == nil {
		return AnimeNamingPreview{}, storage.ErrAnimeInvalid
	}
	seen := make(map[string]bool, len(in.Items))
	for _, item := range in.Items {
		if !model.ValidAnimeID(item.ID) || seen[item.ID] || strings.TrimSpace(item.Title) == "" || len(item.Title) > 4096 || len(item.Parse.RawEpisode) > 64 || item.Parse.Episode != nil && (*item.Parse.Episode < 0 || *item.Parse.Episode > 99999) {
			return AnimeNamingPreview{}, storage.ErrAnimeInvalid
		}
		seen[item.ID] = true
	}
	snapshot, err := templates.NamingTemplate(ctx, naming.TV)
	if err != nil {
		return AnimeNamingPreview{}, fmt.Errorf("读取 RSS 命名模板: %w", err)
	}
	if snapshot.Template.Kind != naming.TV {
		return AnimeNamingPreview{}, storage.ErrAnimeInvalid
	}
	renderer, err := naming.Compile(snapshot.Template)
	if err != nil {
		return AnimeNamingPreview{}, err
	}
	out := AnimeNamingPreview{Naming: snapshot, Items: make([]AnimeNamingPreviewItem, 0, len(in.Items))}
	for _, item := range in.Items {
		if err := ctx.Err(); err != nil {
			return AnimeNamingPreview{}, err
		}
		projection := plan.Project(in.Mapping.SourceRef, item.Parse)
		row := AnimeNamingPreviewItem{ID: item.ID, AnimeNamingProjection: projection}
		if projection.Status == "mapped" {
			result, err := animeorganize.Render(renderer, in.Target, projection, item.Title, true, mediafacts.Values(item.Title, model.AnimeSpec{}, in.Group))
			if err != nil {
				row.Error = err.Error()
			} else {
				row.Result = &result
			}
		}
		out.Items = append(out.Items, row)
	}
	return out, nil
}
