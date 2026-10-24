package service

import (
	"context"
	"net/url"
	"strings"
	"time"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/garden"
)

type GardenSearchInput struct {
	garden.SearchOptions
	Fallback string `json:"fallback"`
	Refresh  bool   `json:"refresh"`
}

type GardenSearchResult struct {
	Specs     map[string]ResourceSearchSpec `json:"specs"`
	Items     []garden.Resource             `json:"items"`
	Groups    []animefeed.Group             `json:"groups"`
	Query     string                        `json:"query"`
	Page      int                           `json:"page"`
	HasMore   bool                          `json:"hasMore"`
	Cached    bool                          `json:"cached"`
	FetchedAt time.Time                     `json:"fetchedAt"`
	ExpiresAt time.Time                     `json:"expiresAt"`
}

func (s *GardenService) SearchResources(ctx context.Context, in GardenSearchInput) (GardenSearchResult, error) {
	var out GardenSearchResult
	in.Query, in.Fallback = strings.TrimSpace(in.Query), strings.TrimSpace(in.Fallback)
	if in.Page == 0 {
		in.Page = 1
	}
	path, err := garden.SearchPath(in.SearchOptions)
	if err != nil || in.Fallback != "" && !garden.ValidQuery(in.Fallback) {
		return out, animefeed.ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	read, cached, err := s.read(ctx, path, 2*time.Minute, in.Refresh, nil, func(body []byte) error { _, err := garden.ParseSearchResources(body, in.SearchOptions); return err })
	if err != nil {
		return out, err
	}
	resources, err := garden.ParseSearchResources(read.body, in.SearchOptions)
	if err != nil {
		return out, err
	}
	// Only the default first-page title search may fall back to the original name.
	if len(resources.Rows) == 0 && in.Page == 1 && in.GroupID == "" && in.Fallback != "" && in.Fallback != in.Query {
		in.Query, in.Fallback = in.Fallback, ""
		return s.SearchResources(ctx, in)
	}
	groups := make([]animefeed.Group, 0, len(resources.Rows))
	specs := make(map[string]ResourceSearchSpec, len(resources.Rows))
	seen := make(map[string]bool, len(resources.Rows))
	for i := range resources.Rows {
		r := &resources.Rows[i]
		specs[r.Provider+":"+r.ProviderID] = resourceSearchSpec(r.Title)
		g := garden.ResourceGroup(*r)
		if !seen[g.ID] {
			groups = append(groups, g)
			seen[g.ID] = true
		}
		magnet, _ := url.Parse(r.Magnet) // Already checked by the provider parser.
		r.Magnet = "magnet:?xt=" + magnet.Query().Get("xt")
	}
	return GardenSearchResult{Items: resources.Rows, Specs: specs, Groups: groups, Query: in.Query, Page: in.Page, HasMore: !*resources.Pagination.Complete && in.Page < 100, Cached: cached, FetchedAt: read.observed, ExpiresAt: read.expires}, nil
}
