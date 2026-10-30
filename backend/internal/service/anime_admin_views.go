package service

import (
	"context"
	"time"

	"strm115/backend/internal/anime/binding"
	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/client/imagecache"
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
	Lifecycle model.AnimeLifecycle `json:"lifecycle"`
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
	Progress         model.AnimeSourceProgress `json:"progress"`
	EffectiveState   string                    `json:"effectiveState"`
	BlockedBy        string                    `json:"blockedBy"`
	ReleaseStats     AnimeReleaseStats         `json:"releaseStats"`
	JobStats         AnimeJobStats             `json:"jobStats"`
	FileStats        AnimeFileStats            `json:"fileStats"`
	CompletionStats  AnimeCompletionStats      `json:"completionStats"`
	PermittedActions []AnimeAction             `json:"permittedActions"`
}
type AnimeSubscriptionList struct {
	Items       []AnimeSubscriptionCard `json:"items"`
	Total       int                     `json:"total"`
	Page        int                     `json:"page"`
	PageSize    int                     `json:"pageSize"`
	DataVersion string                  `json:"dataVersion"`
}

// Lists are a card projection; historical job/decision counts and episode
// naming plans belong to the detail endpoint, not every page refresh.
type AnimeSubscriptionCard struct {
	ID      string            `json:"id"`
	Status  string            `json:"status"`
	Entry   model.AnimeEntry  `json:"entry"`
	Sources []AnimeSourceCard `json:"sources"`
}

type AnimeSourceCard struct {
	ID              string                    `json:"id"`
	GroupName       *string                   `json:"groupName"`
	Position        int                       `json:"position"`
	ConfiguredState string                    `json:"configuredState"`
	RunWhenEnabled  bool                      `json:"runWhenEnabled"`
	Progress        model.AnimeSourceProgress `json:"progress"`
}

// Normalize stored cover addresses for display without changing entry identity,
// persisting a migration, or fetching subject metadata during list requests.
func animeViewEntry(entry model.AnimeEntry) model.AnimeEntry {
	if cover := imagecache.BangumiCoverURL(entry.Metadata.CoverURL); cover != "" {
		entry.Metadata.CoverURL = cover
	}
	return entry
}

func (s *AnimeAdminService) subscriptionView(ctx context.Context, sub model.AnimeSubscription, settings model.AnimeSettings) (AnimeSubscriptionView, error) {
	e, err := s.Store.GetAnimeEntry(ctx, sub.EntryID)
	if err != nil {
		return AnimeSubscriptionView{}, err
	}
	v := AnimeSubscriptionView{AnimeSubscription: sub, Entry: animeViewEntry(e), EffectiveState: sub.Status, Readiness: s.settingsReadiness(settings), PermittedActions: []AnimeAction{{Name: "save", ReasonCode: "CAPABILITY_UNAVAILABLE"}, {Name: "enable", ReasonCode: "CAPABILITY_UNAVAILABLE"}, {Name: "organize", ReasonCode: "CAPABILITY_UNAVAILABLE"}}}
	v.Lifecycle, err = s.Store.AnimeLifecycle(ctx, sub, e, time.Now().UnixMilli())
	if err != nil {
		return v, err
	}
	for _, action := range []string{"pause", "archive", "remove", "unarchive"} {
		allowed := animeLocalControlAllowed(sub.Status, action)
		reason := ""
		if !allowed {
			reason = "STATE_CONFLICT"
		}
		v.PermittedActions = append(v.PermittedActions, AnimeAction{Name: action, Allowed: allowed, ReasonCode: reason})
	}
	v.PermittedActions = append(v.PermittedActions, AnimeAction{Name: "resume", Allowed: s.DownloadAvailable() && sub.FirstEnabledAt != nil && (sub.Status == "paused" || sub.Status == "archived")})
	jobs, err := s.Store.AnimeJobCountsBySource(ctx, sub.ID)
	if err != nil {
		return v, err
	}
	v.JobStats, v.FileStats, v.CompletionStats = animeViewCounts(jobs[""])
	counts, err := s.Store.AnimeDecisionCountsBySource(ctx, sub.ID)
	if err != nil {
		return v, err
	}
	ids := make([]string, 0, len(sub.Sources))
	for _, source := range sub.Sources {
		ids = append(ids, source.ID)
	}
	progress, err := s.Store.AnimeSourceProgresses(ctx, ids, true)
	if err != nil {
		return v, err
	}
	v.Sources = make([]AnimeSourceView, 0, len(sub.Sources))
	main := false
	for _, source := range sub.Sources {
		stats := counts[source.ID]
		effective, blockedBy := animeSourceEffectiveState(sub.Status, source)
		if source.RunWhenEnabled && (source.Status == "draft" || source.Binding.Validate(true) != nil || source.Rules.Validate(true) != nil) {
			v.Readiness.Issues = append(v.Readiness.Issues, "ACTIVE_SOURCE_INCOMPLETE")
		}
		v.Sources = append(v.Sources, AnimeSourceView{AnimeSource: source, Progress: progress[source.ID], EffectiveState: effective, BlockedBy: blockedBy, ReleaseStats: AnimeReleaseStats{SelectedCount: stats["selected"] + stats["linked"], DismissedCount: stats["dismissed"], PendingVerificationCount: stats["pending_verification"]}, PermittedActions: []AnimeAction{{Name: "save", ReasonCode: "CAPABILITY_UNAVAILABLE"}, {Name: "enable", ReasonCode: "CAPABILITY_UNAVAILABLE"}}})
		v.Sources[len(v.Sources)-1].JobStats, v.Sources[len(v.Sources)-1].FileStats, v.Sources[len(v.Sources)-1].CompletionStats = animeViewCounts(jobs[source.ID])
		if source.Role == "main" {
			main = true
			if !source.RunWhenEnabled {
				v.Readiness.Issues = append(v.Readiness.Issues, "MAIN_SOURCE_REQUIRED")
			}
			if source.Status != "paused" && source.Status != "enabled" {
				v.Readiness.Issues = append(v.Readiness.Issues, "MAIN_SOURCE_INCOMPLETE")
			}
		}
	}
	if !main {
		v.Readiness.Issues = append(v.Readiness.Issues, "MAIN_SOURCE_REQUIRED")
	}
	v.Readiness.Ready = len(v.Readiness.Issues) == 0
	v.PermittedActions[1].Allowed = v.PermittedActions[1].Allowed && v.Readiness.Ready
	for _, stats := range counts {
		v.ReleaseStats.SelectedCount += stats["selected"] + stats["linked"]
		v.ReleaseStats.DismissedCount += stats["dismissed"]
		v.ReleaseStats.PendingVerificationCount += stats["pending_verification"]
	}
	v.DataVersion, err = preview.Digest(v)
	return v, err
}

func animeViewCounts(c storage.AnimeJobCounts) (AnimeJobStats, AnimeFileStats, AnimeCompletionStats) {
	return AnimeJobStats{TotalCount: c.Total, QueuedCount: c.Queued, DownloadingCount: c.Downloading, RemoteCompletedCount: c.RemoteCompleted, FailedCount: c.Failed, NeedsReviewCount: c.NeedsReview}, AnimeFileStats{VerifiedCount: c.Verified, NeedsReviewCount: c.FileReview}, AnimeCompletionStats{OriginalSavedCount: c.OriginalSaved, OrganizedCount: c.Organized}
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
	entryIDs, sourceIDs := make([]string, 0, len(p.Items)), make([]string, 0, len(p.Items)*model.AnimeMaxSources)
	for _, sub := range p.Items {
		entryIDs = append(entryIDs, sub.EntryID)
		for _, source := range sub.Sources {
			sourceIDs = append(sourceIDs, source.ID)
		}
	}
	entries, err := s.Store.AnimeEntries(ctx, entryIDs)
	if err != nil {
		return AnimeSubscriptionList{}, err
	}
	progress, err := s.Store.AnimeSourceProgresses(ctx, sourceIDs, false)
	if err != nil {
		return AnimeSubscriptionList{}, err
	}
	out := AnimeSubscriptionList{Total: p.Total, Page: p.Page, PageSize: p.PageSize, Items: make([]AnimeSubscriptionCard, 0, len(p.Items))}
	for _, sub := range p.Items {
		v := AnimeSubscriptionCard{ID: sub.ID, Status: sub.Status, Entry: animeViewEntry(entries[sub.EntryID]), Sources: make([]AnimeSourceCard, 0, len(sub.Sources))}
		for _, source := range sub.Sources {
			v.Sources = append(v.Sources, AnimeSourceCard{ID: source.ID, GroupName: source.GroupName, Position: source.Position, ConfiguredState: source.Status, RunWhenEnabled: source.RunWhenEnabled, Progress: progress[source.ID]})
		}
		out.Items = append(out.Items, v)
	}
	out.DataVersion, err = preview.Digest(out)
	return out, err
}

type AnimeReleaseView struct {
	model.AnimeReleaseRecord
	Numbering     binding.Projection `json:"numbering"`
	IdentityState string             `json:"identityState"`
	Tasks         []string           `json:"tasks"`
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
		n, e := binding.Project(record.Decision.Snapshot.Binding, record.Decision.Snapshot.Parse)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, AnimeReleaseView{AnimeReleaseRecord: record, Numbering: n, IdentityState: "unverified", Tasks: []string{}})
	}
	out.DataVersion, err = preview.Digest(out)
	return out, err
}

func animeLocalControlAllowed(state, action string) bool {
	switch action {
	case "pause":
		return state == "enabled" || state == "paused"
	case "archive":
		return state != "removed"
	case "remove":
		return true
	case "unarchive":
		return state == "archived" || state == "paused"
	}
	return false
}
