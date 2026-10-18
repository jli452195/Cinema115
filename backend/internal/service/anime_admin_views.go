package service

import (
	"context"
	"strm115/backend/internal/animebinding"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type AnimeAction struct {
	Name       string `json:"name"`
	Allowed    bool   `json:"allowed"`
	ReasonCode string `json:"reasonCode,omitempty"`
}
type AnimeReleaseStats struct {
	SelectedCount            int `json:"selectedCount"`
	DismissedCount           int `json:"dismissedCount"`
	PendingVerificationCount int `json:"pendingVerificationCount"`
}
type AnimeJobStats struct {
	TotalCount           int `json:"totalCount"`
	QueuedCount          int `json:"queuedCount"`
	DownloadingCount     int `json:"downloadingCount"`
	RemoteCompletedCount int `json:"remoteCompletedCount"`
	FailedCount          int `json:"failedCount"`
	NeedsReviewCount     int `json:"needsReviewCount"`
}
type AnimeFileStats struct {
	VerifiedCount    int `json:"verifiedCount"`
	NeedsReviewCount int `json:"needsReviewCount"`
}
type AnimeCompletionStats struct {
	OriginalSavedCount int `json:"originalSavedCount"`
	OrganizedCount     int `json:"organizedCount"`
}
type AnimeSubscriptionView struct {
	model.AnimeSubscription
	Sources          []AnimeSourceView    `json:"sources"`
	Entry            model.AnimeEntry     `json:"entry"`
	EffectiveState   string               `json:"effectiveState"`
	Readiness        AnimeReadiness       `json:"readiness"`
	PermittedActions []AnimeAction        `json:"permittedActions"`
	ReleaseStats     AnimeReleaseStats    `json:"releaseStats"`
	JobStats         AnimeJobStats        `json:"jobStats"`
	FileStats        AnimeFileStats       `json:"fileStats"`
	CompletionStats  AnimeCompletionStats `json:"completionStats"`
	DataVersion      string               `json:"dataVersion"`
}

type AnimeSourceView struct {
	model.AnimeSource
	EffectiveState   string               `json:"effectiveState"`
	BlockedBy        string               `json:"blockedBy"`
	ReleaseStats     AnimeReleaseStats    `json:"releaseStats"`
	JobStats         AnimeJobStats        `json:"jobStats"`
	FileStats        AnimeFileStats       `json:"fileStats"`
	CompletionStats  AnimeCompletionStats `json:"completionStats"`
	PermittedActions []AnimeAction        `json:"permittedActions"`
}
type AnimeSubscriptionList struct {
	Items       []AnimeSubscriptionView `json:"items"`
	Total       int                     `json:"total"`
	Page        int                     `json:"page"`
	PageSize    int                     `json:"pageSize"`
	DataVersion string                  `json:"dataVersion"`
}

func (s *AnimeAdminService) subscriptionView(ctx context.Context, sub model.AnimeSubscription, settings model.AnimeSettings) (AnimeSubscriptionView, error) {
	e, err := s.Store.GetAnimeEntry(ctx, sub.EntryID)
	if err != nil {
		return AnimeSubscriptionView{}, err
	}
	v := AnimeSubscriptionView{AnimeSubscription: sub, Entry: e, EffectiveState: sub.Status, Readiness: s.settingsReadiness(settings), PermittedActions: []AnimeAction{{Name: "save", Allowed: sub.Status == "draft" || sub.Status == "paused"}, {Name: "enable", ReasonCode: "CAPABILITY_UNAVAILABLE"}, {Name: "organize", ReasonCode: "CAPABILITY_UNAVAILABLE"}}}
	counts, err := s.Store.AnimeDecisionCountsBySource(ctx, sub.ID)
	if err != nil {
		return v, err
	}
	v.Sources = make([]AnimeSourceView, 0, len(sub.Sources))
	main := false
	for _, source := range sub.Sources {
		stats := counts[source.ID]
		effective, blockedBy := animeSourceEffectiveState(sub.Status, source)
		v.Sources = append(v.Sources, AnimeSourceView{AnimeSource: source, EffectiveState: effective, BlockedBy: blockedBy, ReleaseStats: AnimeReleaseStats{SelectedCount: stats["selected"], DismissedCount: stats["dismissed"], PendingVerificationCount: stats["selected"]}, PermittedActions: []AnimeAction{{Name: "save", Allowed: sub.Status == "draft" || sub.Status == "paused"}, {Name: "enable", ReasonCode: "CAPABILITY_UNAVAILABLE"}}})
		if source.Role == "main" {
			main = true
			if source.Status != "paused" {
				v.Readiness.Issues = append(v.Readiness.Issues, "MAIN_SOURCE_INCOMPLETE")
			}
		}
	}
	if !main {
		v.Readiness.Issues = append(v.Readiness.Issues, "MAIN_SOURCE_REQUIRED")
	}
	v.Readiness.Ready = len(v.Readiness.Issues) == 0
	for _, stats := range counts {
		v.ReleaseStats.SelectedCount += stats["selected"]
		v.ReleaseStats.DismissedCount += stats["dismissed"]
		v.ReleaseStats.PendingVerificationCount += stats["selected"]
	}
	v.DataVersion, err = animepreview.Digest(v)
	return v, err
}

func animeSourceEffectiveState(parent string, source model.AnimeSource) (state, blockedBy string) {
	if source.Status == "draft" || source.Status == "removed" {
		return source.Status, ""
	}
	if parent == "removed" || parent == "archived" {
		return "stopped", "subscription"
	}
	if source.Status == "paused" || !source.RunWhenEnabled {
		return "paused", "source"
	}
	if parent != "enabled" {
		return "paused", "subscription"
	}
	return "enabled", ""
}
func (s *AnimeAdminService) Subscription(ctx context.Context, id string) (AnimeSubscriptionView, error) {
	sub, err := s.Store.GetAnimeSubscription(ctx, id, false)
	if err != nil {
		return AnimeSubscriptionView{}, err
	}
	settings, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return AnimeSubscriptionView{}, err
	}
	return s.subscriptionView(ctx, sub, settings)
}
func (s *AnimeAdminService) Subscriptions(ctx context.Context, f storage.AnimeSubscriptionFilter) (AnimeSubscriptionList, error) {
	p, err := s.Store.ListAnimeSubscriptions(ctx, f)
	if err != nil {
		return AnimeSubscriptionList{}, err
	}
	settings, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return AnimeSubscriptionList{}, err
	}
	out := AnimeSubscriptionList{Total: p.Total, Page: p.Page, PageSize: p.PageSize, Items: make([]AnimeSubscriptionView, 0, len(p.Items))}
	for _, sub := range p.Items {
		v, e := s.subscriptionView(ctx, sub, settings)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	out.DataVersion, err = animepreview.Digest(out)
	return out, err
}

type AnimeReleaseView struct {
	model.AnimeReleaseRecord
	Numbering     animebinding.Projection `json:"numbering"`
	IdentityState string                  `json:"identityState"`
	Tasks         []string                `json:"tasks"`
}
type AnimeReleaseList struct {
	Items       []AnimeReleaseView `json:"items"`
	Total       int                `json:"total"`
	Page        int                `json:"page"`
	PageSize    int                `json:"pageSize"`
	DataVersion string             `json:"dataVersion"`
}

func (s *AnimeAdminService) Releases(ctx context.Context, id string, f storage.AnimeReleaseFilter) (AnimeReleaseList, error) {
	sub, err := s.Store.GetAnimeSubscription(ctx, id, false)
	if err != nil {
		return AnimeReleaseList{}, err
	}
	found := false
	for _, source := range sub.Sources {
		if source.ID == f.SourceID {
			found = true
		}
	}
	if !found {
		return AnimeReleaseList{}, storage.ErrAnimeInvalid
	}
	p, err := s.Store.ListAnimeReleases(ctx, f)
	if err != nil {
		return AnimeReleaseList{}, err
	}
	out := AnimeReleaseList{Items: make([]AnimeReleaseView, 0, len(p.Items)), Total: p.Total, Page: p.Page, PageSize: p.PageSize}
	for _, record := range p.Items {
		n, e := animebinding.Project(record.Decision.Snapshot.Binding, record.Decision.Snapshot.Parse)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, AnimeReleaseView{AnimeReleaseRecord: record, Numbering: n, IdentityState: "unverified", Tasks: []string{}})
	}
	out.DataVersion, err = animepreview.Digest(out)
	return out, err
}
