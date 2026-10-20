package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/animeresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// AnimeConfigError carries only fixed classifications and local field refs.
type AnimeConfigError struct {
	Code   string
	Issues []string
}

var ErrAnimeDirectoryRead = errors.New("anime directory upstream read failed")

func (e *AnimeConfigError) Error() string { return e.Code }

type AnimeDiscovery interface {
	Calendar(context.Context, int) (bangumi.CalendarResult, error)
	Search(context.Context, bangumi.SearchOptions) (bangumi.SearchResult, error)
	Subject(context.Context, string) (bangumi.SubjectResult, error)
	Relations(context.Context, string) (bangumi.RelationsResult, error)
}

type AnimeEpisodeDiscovery interface {
	Episodes(context.Context, string, int) (bangumi.EpisodesResult, error)
}
type animePrimaryAccounts interface {
	Primary(string) (P115Account, error)
}
type animeDirectories interface {
	ReadDirectory(context.Context, string, string) (p115.DriveDirectory, error)
}

type AnimeAdminService struct {
	Store                *storage.Store
	Bindings             *AnimeBindingService
	Previews             *AnimePreviewService
	Discovery            AnimeDiscovery
	Accounts             animePrimaryAccounts
	Directories          animeDirectories
	Naming               *AnimeNamingService
	Torrents             animeresource.TorrentReader
	Offline              CloudTaskReader
	SubmitGateway        AnimeOfflineGateway
	StatusGateway        AnimeStatusReader
	FileGateway          AnimeFileGateway
	OrganizationGateway  AnimeOrganizationGateway
	ReserveOrganization  func() (func(), error)
	Logs                 *GenerationLogs
	organizationCache    animeOrganizationCache
	DownloadCoordinator  *CloudDownloadCoordinator
	downloadWakeOnce     sync.Once
	downloadWake         chan struct{}
	verificationWakeOnce sync.Once
	verificationWake     chan struct{}
	discoveryWakeOnce    sync.Once
	discoveryWake        chan struct{}
}

type AnimeSaveConfig struct {
	Naming           *model.AnimeNamingInput     `json:"naming"`
	Entry            AnimeEntryRef               `json:"entry"`
	Sources          []model.AnimeSourceConfig   `json:"sources"`
	RemovedSourceIDs []model.AnimeSourceRemoval  `json:"removedSourceIds"`
	FormSelections   []model.AnimeFormSelection  `json:"formSelections"`
	PreviewRefs      []model.AnimeFormPreviewRef `json:"previewRefs"`
}
type AnimeSaveInput struct {
	Config           AnimeSaveConfig `json:"config"`
	SaveMode         string          `json:"saveMode"`
	ExpectedRevision int64           `json:"expectedRevision"`
	SettingsRevision int64           `json:"settingsRevision"`
}

type AnimeStartInput struct {
	Config           AnimeSaveConfig                 `json:"config"`
	ExpectedRevision int64                           `json:"expectedRevision"`
	SettingsRevision int64                           `json:"settingsRevision"`
	Chapter          animepreview.Chapter            `json:"chapter"`
	Sources          []storage.AnimeActivationSource `json:"sources"`
}

func (s *AnimeAdminService) StartSubscription(ctx context.Context, actor, key, id string, in AnimeStartInput) (model.AnimeConfigReceipt, error) {
	return s.saveConfiguration(ctx, actor, key, id, AnimeSaveInput{Config: in.Config, ExpectedRevision: in.ExpectedRevision, SettingsRevision: in.SettingsRevision, SaveMode: "paused"}, &in)
}

// SaveDefinition persists a complete definition. RSS initialization and download
// authorization belong to the background workers, never this HTTP request.
func (s *AnimeAdminService) SaveDefinition(ctx context.Context, actor, key, id string, in AnimeSaveInput) (model.AnimeConfigReceipt, error) {
	in.SaveMode = "paused" // Validation shape; runtime status is server-owned.
	if len(in.Config.FormSelections) != 0 || len(in.Config.PreviewRefs) != 0 {
		return model.AnimeConfigReceipt{}, storage.ErrAnimeInvalid
	}
	return s.saveConfiguration(ctx, actor, key, id, in, nil, true)
}

func animeRequestKey(actor, operation, key string, body any) (storage.AnimeWriteKey, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return storage.AnimeWriteKey{}, fmt.Errorf("encode anime request: %w", err)
	}
	h := sha256.Sum256(raw)
	k := storage.AnimeWriteKey{ActorKey: actor, Operation: operation, Key: key, Digest: hex.EncodeToString(h[:])}
	return k, k.Validate()
}

// SaveConfiguration validates all local fields before upstream reads. A source
// omitted by PATCH is retained; only explicit removals can delete one.
func (s *AnimeAdminService) saveConfiguration(ctx context.Context, actor, key, id string, in AnimeSaveInput, start *AnimeStartInput, definition ...bool) (out model.AnimeConfigReceipt, resultErr error) {
	runtime := len(definition) != 0 && definition[0]
	ctx, finishTiming := s.observeAnimeStage(ctx, id, "subscription")
	defer finishTiming()
	// Deep-copy before canonicalization; caller form slices remain untouched.
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	var owned AnimeSaveInput
	if err := json.Unmarshal(raw, &owned); err != nil {
		return out, err
	}
	in = owned
	for i := range in.Config.Sources {
		v := &in.Config.Sources[i]
		v.GroupName = nil // Never trust or fingerprint browser display metadata.
		v.Rules, err = animepreview.NormalizeRules(v.Rules)
		if err != nil {
			return out, storage.ErrAnimeInvalid
		}
	}
	if in.Config.Sources == nil {
		in.Config.Sources = []model.AnimeSourceConfig{}
	}
	if in.Config.RemovedSourceIDs == nil {
		in.Config.RemovedSourceIDs = []model.AnimeSourceRemoval{}
	}
	if in.Config.FormSelections == nil {
		in.Config.FormSelections = []model.AnimeFormSelection{}
	}
	if in.Config.PreviewRefs == nil {
		in.Config.PreviewRefs = []model.AnimeFormPreviewRef{}
	}
	sort.Slice(in.Config.Sources, func(i, j int) bool { return in.Config.Sources[i].Ref < in.Config.Sources[j].Ref })
	sort.Slice(in.Config.RemovedSourceIDs, func(i, j int) bool { return in.Config.RemovedSourceIDs[i].ID < in.Config.RemovedSourceIDs[j].ID })
	for i := range in.Config.FormSelections {
		f := &in.Config.FormSelections[i]
		if f.SelectedReleaseIDs == nil {
			f.SelectedReleaseIDs = []string{}
		}
		if f.UnresolvedReleaseIDs == nil {
			f.UnresolvedReleaseIDs = []string{}
		}
		if f.DismissedReleaseIDs == nil {
			f.DismissedReleaseIDs = []string{}
		}
		sort.Strings(f.SelectedReleaseIDs)
		sort.Strings(f.UnresolvedReleaseIDs)
		sort.Strings(f.DismissedReleaseIDs)
	}
	sort.Slice(in.Config.FormSelections, func(i, j int) bool {
		return in.Config.FormSelections[i].SourceRef < in.Config.FormSelections[j].SourceRef
	})
	sort.Slice(in.Config.PreviewRefs, func(i, j int) bool { return in.Config.PreviewRefs[i].SourceRef < in.Config.PreviewRefs[j].SourceRef })
	op := "POST /api/admin/anime/subscriptions"
	if id != "" {
		op = "PATCH /api/admin/anime/subscriptions/" + id
	}
	var body any = in
	if start != nil {
		op += "/start"
		body = struct {
			Config  AnimeSaveInput
			Chapter animepreview.Chapter
			Sources []storage.AnimeActivationSource
		}{in, start.Chapter, start.Sources}
	}
	k, err := animeRequestKey(actor, op, key, body)
	if err != nil {
		return out, err
	}
	if receipt, err := s.Store.LookupAnimeReceipt(ctx, k); err != nil {
		return out, err
	} else if receipt != nil {
		if err := json.Unmarshal(receipt.Body, &out); err != nil {
			return out, fmt.Errorf("read anime configuration receipt: %w", err)
		}
		return out, nil
	}
	defer s.replayAnimeConcurrentWrite(ctx, k, &out, &resultErr)
	if in.SaveMode == "enabled" {
		return out, storage.ErrAnimeCapability
	}
	if in.SaveMode != "draft" && in.SaveMode != "paused" || (id == "") != (in.ExpectedRevision == 0) || id != "" && !model.ValidAnimeID(id) || in.ExpectedRevision < 0 || in.ExpectedRevision >= model.AnimeMaxRevision || in.SettingsRevision < 1 || in.SettingsRevision > model.AnimeMaxRevision || in.Config.Entry.validate() != nil {
		return out, storage.ErrAnimeInvalid
	}
	for _, v := range in.Config.Sources {
		if v.Status == "enabled" {
			return out, storage.ErrAnimeCapability
		}
		if v.Status != "draft" && v.Status != "paused" {
			return out, storage.ErrAnimeInvalid
		}
	}
	settings, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return out, err
	}
	if settings.Revision != in.SettingsRevision {
		return out, storage.ErrAnimeSettingsChanged
	}
	c := model.AnimeSubscriptionConfig{ID: id, EntryID: "pending", ExpectedRevision: in.ExpectedRevision, Status: in.SaveMode, Sources: in.Config.Sources, RemovedSources: in.Config.RemovedSourceIDs, Form: model.AnimeForm{SchemaVersion: 1, FormSelections: in.Config.FormSelections, PreviewRefs: in.Config.PreviewRefs}}
	// Validate partial source structures first, so incomplete paused input gets
	// CONFIG_INCOMPLETE while illegal enums/ranges remain INVALID_INPUT.
	partial := c
	partial.Sources = append([]model.AnimeSourceConfig{}, c.Sources...)
	for i := range partial.Sources {
		partial.Sources[i].Status = "draft"
		partial.Sources[i].GroupName = nil
	}
	if partial.Validate() != nil {
		return out, storage.ErrAnimeInvalid
	}
	if start != nil {
		if len(start.Sources) < 1 || len(start.Sources) > model.AnimeMaxSources {
			return out, storage.ErrAnimeInvalid
		}
		for _, ref := range start.Sources {
			if !model.ValidAnimeID(ref.BatchID) || !model.ValidAnimeID(ref.SourceRef) || !model.ValidAnimeID(ref.PreviewID) || ref.LoadRevision < 1 || ref.LoadRevision >= model.AnimeMaxRevision || ref.SelectedIDs == nil || ref.DismissedIDs == nil || len(ref.SelectedIDs)+len(ref.DismissedIDs) > 2000 {
				return out, storage.ErrAnimeInvalid
			}
		}
		for _, source := range c.Sources {
			if source.Status != "paused" || source.ItemID == nil || source.GroupID == nil || source.Binding.Validate(true) != nil || source.Rules.Validate(true) != nil {
				return out, &AnimeConfigError{Code: "CONFIG_INCOMPLETE", Issues: []string{"sources." + source.Ref}}
			}
		}
	}
	if start != nil || runtime {
		if settings.OrganizeCID == nil || in.Config.Naming == nil {
			return out, &AnimeConfigError{Code: "CONFIG_INCOMPLETE", Issues: []string{"organization"}}
		}
		if in.Config.Naming.Validate() != nil {
			return out, storage.ErrAnimeInvalid
		}
	}
	bindingEntry := in.Config.Entry
	if id == "" {
		entryID := bindingEntry.ID
		if entryID == "" && bindingEntry.BangumiID != nil {
			local, e := s.Store.FindAnimeEntry(ctx, *bindingEntry.BangumiID, bindingEntry.EditionKey)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return out, e
			}
			if e == nil {
				entryID = local.ID
				bindingEntry = AnimeEntryRef{ID: local.ID}
			}
		}
		if entryID != "" {
			existing, e := s.Store.FindAnimeSubscriptionID(ctx, entryID)
			if e != nil {
				return out, e
			}
			if existing != "" {
				return out, &storage.AnimeExistsError{Kind: "subscription", ID: existing}
			}
		}
	}
	var old model.AnimeSubscription
	if id != "" {
		old, err = s.Store.GetAnimeSubscription(ctx, id, false)
		if err != nil {
			return out, err
		}
		if old.Revision != in.ExpectedRevision {
			return out, storage.ErrAnimeConflict
		}
		if in.Config.Entry.ID != old.EntryID {
			return out, storage.ErrAnimeIdentity
		}
		for _, v := range c.Sources {
			if v.ID == "" {
				continue
			}
			found := false
			for _, o := range old.Sources {
				if o.ID == v.ID {
					found = true
					if o.Revision != v.ExpectedRevision {
						return out, storage.ErrAnimeConflict
					}
					if !o.AcceptsIdentity(v.Provider, v.ItemID, v.GroupID) {
						return out, storage.ErrAnimeIdentity
					}
				}
			}
			if !found {
				return out, storage.ErrAnimeConflict
			}
		}
		for _, v := range c.RemovedSources {
			found := false
			for _, o := range old.Sources {
				if o.ID == v.ID && o.Revision == v.ExpectedRevision {
					found = true
				}
			}
			if !found {
				return out, storage.ErrAnimeConflict
			}
		}
	} else {
		for _, v := range c.Sources {
			if v.ID != "" {
				return out, storage.ErrAnimeInvalid
			}
		}
		if len(c.RemovedSources) > 0 {
			return out, storage.ErrAnimeInvalid
		}
	}
	// Verify source metadata, never RSS availability, under one total deadline.
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if in.Config.Naming != nil {
		if s.Naming == nil {
			return out, storage.ErrAnimeCapability
		}
		c.Naming, err = s.Naming.Resolve(ctx, *in.Config.Naming, old.Naming)
		if err != nil {
			return out, err
		}
	}
	var entry model.AnimeEntry
	if bindingEntry.ID != "" || bindingEntry.BangumiID != nil {
		entry, _, err = s.Bindings.resolveEntry(ctx, bindingEntry, "")
		if err != nil {
			return out, err
		}
	}
	for i := range c.Sources {
		v := &c.Sources[i]
		// Existing immutable source identity already has verified metadata. Local
		// edits must continue working when an upstream entry has disappeared.
		verified := false
		for _, prior := range old.Sources {
			if v.ID == prior.ID && prior.GroupName != nil && animeEqualString(v.ItemID, prior.ItemID) && animeEqualString(v.GroupID, prior.GroupID) {
				v.GroupName = prior.GroupName
				verified = true
				break
			}
		}
		if verified {
			continue
		}
		if v.ItemID != nil && v.GroupID != nil {
			budget, e := NewAnimeSourceBudget(nil)
			if e != nil {
				return out, e
			}
			checked, e := s.Bindings.CheckBinding(ctx, budget, AnimeBindingInput{Entry: bindingEntry, Source: animefeed.NewSource(v.Provider, *v.ItemID, *v.GroupID), Binding: v.Binding})
			if e != nil {
				return out, e
			}
			v.GroupName = &checked.Source.GroupName
			entry = checked.Entry
		} else if v.Status == "paused" {
			return out, &AnimeConfigError{Code: "CONFIG_INCOMPLETE", Issues: []string{"sources." + v.Ref + ".binding"}}
		}
	}
	if entry.Title == "" {
		title := ""
		for _, v := range c.Sources {
			if v.ItemID != nil {
				budget, e := NewAnimeSourceBudget(nil)
				if e != nil {
					return out, e
				}
				read, e := s.Bindings.ReadEntry(ctx, budget, v.Provider, *v.ItemID, false)
				if e != nil {
					return out, e
				}
				title = read.Entry.Title
				break
			}
		}
		entry, _, err = s.Bindings.resolveEntry(ctx, bindingEntry, title)
		if err != nil {
			return out, err
		}
	}
	issues := make([]string, 0, model.AnimeMaxSources)
	for _, v := range c.Sources {
		if v.Validate() != nil {
			issues = append(issues, "sources."+v.Ref)
		}
	}
	// Build the final source set to assess the retained main as well.
	final := make(map[string]model.AnimeSourceConfig, len(old.Sources)+len(c.Sources))
	for _, v := range old.Sources {
		final[v.ID] = animeSourceConfig(v)
	}
	for _, v := range c.RemovedSources {
		delete(final, v.ID)
	}
	for _, v := range c.Sources {
		ref := v.ID
		if ref == "" {
			ref = v.Ref
		}
		final[ref] = v
	}
	if len(final) > model.AnimeMaxSources {
		return out, storage.ErrAnimeInvalid
	}
	mains := 0
	feeds := make(map[string]bool, len(final))
	for _, v := range final {
		if v.Role == "main" {
			mains++
			if runtime && !v.RunWhenEnabled {
				issues = append(issues, "mainSource")
			}
			if in.SaveMode == "paused" && v.Status != "paused" && !(runtime && v.Status == "enabled") {
				issues = append(issues, "mainSource")
			}
		}
		if v.ItemID != nil && v.GroupID != nil {
			feed := v.Provider + ":" + *v.ItemID + ":" + *v.GroupID
			if feeds[feed] {
				return out, &storage.AnimeExistsError{Kind: "source"}
			}
			feeds[feed] = true
		}
	}
	if mains > 1 {
		return out, storage.ErrAnimeInvalid
	}
	if in.SaveMode == "paused" {
		if mains != 1 {
			issues = append(issues, "mainSource")
		}
		ready := s.settingsReadiness(settings)
		issues = append(issues, ready.Issues...)
	}
	if len(issues) > 0 {
		return out, &AnimeConfigError{Code: "CONFIG_INCOMPLETE", Issues: issues}
	}
	// Preview references are restoration hints, including expired snapshots.
	// They cannot be reused across administrators or unrelated forms.
	for _, ref := range in.Config.PreviewRefs {
		b, e := s.Store.GetAnimePreview(ctx, actor, ref.BatchID)
		if e != nil {
			return out, e
		}
		savedHint := false
		for _, saved := range old.Form.PreviewRefs {
			if saved == ref {
				savedHint = true
				break
			}
		}
		if !savedHint && (b.Config.SubscriptionID != id || b.Config.Chapter.ID != "" && b.Config.Chapter.ID != entry.ID || b.Config.Chapter.EditionKey != entry.EditionKey || !animeEqualString(b.Config.Chapter.BangumiID, entry.BangumiID)) {
			return out, storage.ErrAnimeInvalid
		}
		matched := false
		for _, preview := range b.Sources {
			if preview.ID == ref.SourcePreviewID && preview.Ref == ref.SourceRef && ref.LoadRevision <= preview.LoadRevision {
				for _, source := range final {
					if (ref.SourceID != "" && ref.SourceID == source.ID || ref.SourceID == "" && ref.SourceRef == source.Ref) && source.ItemID != nil && source.GroupID != nil && preview.Page.Source == (animefeed.NewSource(source.Provider, *source.ItemID, *source.GroupID)) {
						matched = true
					}
				}
			}
		}
		if !matched {
			return out, storage.ErrAnimeInvalid
		}
	}
	guard := storage.AnimeConfigGuard{Entry: entry, Settings: settings, RequireAccount: in.SaveMode == "paused"}
	if runtime {
		if id == "" {
			c.Status = "enabled"
		} else {
			if old.Status != "enabled" && old.Status != "paused" {
				return out, storage.ErrAnimeTransition
			}
			c.Status = old.Status
		}
		for i := range c.Sources {
			if c.Sources[i].RunWhenEnabled {
				c.Sources[i].Status = "enabled"
			}
		}
		guard.Runtime = true
		out, err = s.Store.SaveAnimeConfiguration(ctx, c, k, guard)
		if err == nil {
			s.wakeAnimeDiscovery()
		}
		return out, err
	}
	if start != nil {
		session, err := s.PrepareDownloadTarget(ctx, in.SettingsRevision)
		if err != nil {
			return out, err
		}
		expected := animepreview.Config{Chapter: start.Chapter, SubscriptionID: id, Revision: in.ExpectedRevision, Settings: settings, Sources: c.Sources}
		out, err = s.Store.StartAnimeConfiguration(ctx, c, k, guard, expected, start.Sources, session.Target())
		if err == nil {
			s.wakeAnimeVerification()
		}
		return out, err
	}
	return s.Store.SaveAnimeConfiguration(ctx, c, k, guard)
}

func animeEqualString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func animeSourceConfig(v model.AnimeSource) model.AnimeSourceConfig {
	return model.AnimeSourceConfig{ID: v.ID, Ref: v.ID, ExpectedRevision: v.Revision, Role: v.Role, Position: v.Position, Status: v.Status, RunWhenEnabled: v.RunWhenEnabled, Provider: v.Provider, ItemID: v.ItemID, GroupID: v.GroupID, GroupName: v.GroupName, Binding: v.Binding, Rules: v.Rules}
}

type AnimeReadiness struct {
	Ready  bool     `json:"ready"`
	Issues []string `json:"issues"`
}
type AnimeSettingsView struct {
	Settings       model.AnimeSettings  `json:"settings"`
	PrimaryAccount *AnimePrimarySummary `json:"primaryAccount"`
	Readiness      AnimeReadiness       `json:"readiness"`
}
type AnimePrimarySummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	UID      string `json:"uid"`
	Revision uint64 `json:"revision"`
}

func (s *AnimeAdminService) settingsReadiness(v model.AnimeSettings) AnimeReadiness {
	out := AnimeReadiness{Issues: []string{}}
	var a P115Account
	var err error
	if s.Accounts == nil {
		err = storage.ErrAnimeAccountChanged
	} else {
		a, err = s.Accounts.Primary("")
	}
	if err != nil || a.UID == "" {
		out.Issues = append(out.Issues, "PRIMARY_ACCOUNT_REQUIRED")
	} else if v.AccountID == nil || v.UID == nil || v.AccountRevision == nil || *v.AccountID != a.ID || *v.UID != a.UID || uint64(*v.AccountRevision) != a.Revision {
		out.Issues = append(out.Issues, "ACCOUNT_SNAPSHOT_CHANGED")
	}
	if v.DownloadCID == nil || v.DownloadPath == nil {
		out.Issues = append(out.Issues, "DOWNLOAD_DIRECTORY_REQUIRED")
	}
	out.Ready = len(out.Issues) == 0
	return out
}
func (s *AnimeAdminService) Settings(ctx context.Context) (AnimeSettingsView, error) {
	v, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return AnimeSettingsView{}, err
	}
	out := AnimeSettingsView{Settings: v, Readiness: s.settingsReadiness(v)}
	if s.Accounts != nil {
		if a, e := s.Accounts.Primary(""); e == nil {
			out.PrimaryAccount = &AnimePrimarySummary{a.ID, a.Name, a.UID, a.Revision}
		}
	}
	return out, nil
}

type AnimeSettingsInput struct {
	ExpectedRevision               int64   `json:"expectedRevision"`
	DownloadCID                    string  `json:"downloadCid"`
	OrganizeCID                    *string `json:"organizeCid"`
	ExpectedPrimaryAccountID       string  `json:"expectedPrimaryAccountId"`
	ExpectedPrimaryAccountRevision int64   `json:"expectedPrimaryAccountRevision"`
}

func (s *AnimeAdminService) SaveSettings(ctx context.Context, actor, key string, in AnimeSettingsInput) (out model.AnimeSettings, resultErr error) {
	k, err := animeRequestKey(actor, "PATCH /api/admin/anime/settings", key, in)
	if err != nil {
		return out, err
	}
	if r, e := s.Store.LookupAnimeReceipt(ctx, k); e != nil {
		return out, e
	} else if r != nil {
		if err := json.Unmarshal(r.Body, &out); err != nil {
			return out, fmt.Errorf("read anime settings receipt: %w", err)
		}
		return out, nil
	}
	defer s.replayAnimeConcurrentWrite(ctx, k, &out, &resultErr)
	validCID := func(v string) bool { return v == "0" || model.ValidAnimeExternalID(v) }
	if in.ExpectedRevision < 1 || in.ExpectedRevision >= model.AnimeMaxRevision || !validCID(in.DownloadCID) || in.OrganizeCID != nil && !validCID(*in.OrganizeCID) || !model.ValidAnimeID(in.ExpectedPrimaryAccountID) || in.ExpectedPrimaryAccountRevision < 1 || in.ExpectedPrimaryAccountRevision > model.AnimeMaxRevision {
		return out, storage.ErrAnimeInvalid
	}
	settings, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return out, err
	}
	if settings.Revision != in.ExpectedRevision {
		return out, storage.ErrAnimeConflict
	}
	if s.Accounts == nil || s.Directories == nil {
		return out, &AnimeConfigError{Code: "CONFIG_INCOMPLETE", Issues: []string{"PRIMARY_ACCOUNT_REQUIRED"}}
	}
	a, err := s.Accounts.Primary(in.ExpectedPrimaryAccountID)
	if err != nil || a.Revision != uint64(in.ExpectedPrimaryAccountRevision) || !model.ValidAnimeExternalID(a.UID) {
		return out, storage.ErrAnimeAccountChanged
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d, err := s.Directories.ReadDirectory(ctx, in.DownloadCID, a.Cookie)
	if err != nil {
		return out, fmt.Errorf("%w: %w", ErrAnimeDirectoryRead, err)
	}
	if d.CID != in.DownloadCID || d.Path == "" {
		return out, &AnimeConfigError{Code: "UPSTREAM_INVALID"}
	}
	revision := int64(a.Revision)
	out = model.AnimeSettings{Revision: in.ExpectedRevision, AccountID: &a.ID, AccountRevision: &revision, UID: &a.UID, DownloadCID: &d.CID, DownloadPath: &d.Path}
	if in.OrganizeCID != nil {
		o := d
		if *in.OrganizeCID != d.CID {
			o, err = s.Directories.ReadDirectory(ctx, *in.OrganizeCID, a.Cookie)
			if err != nil {
				return out, fmt.Errorf("%w: %w", ErrAnimeDirectoryRead, err)
			}
		}
		if o.CID != *in.OrganizeCID || o.Path == "" {
			return out, &AnimeConfigError{Code: "UPSTREAM_INVALID"}
		}
		out.OrganizeCID, out.OrganizePath = &o.CID, &o.Path
	}
	return s.Store.SaveAnimeSettings(ctx, out, k)
}

// A parallel retry can commit after the initial lookup but before this call
// detects a stale version or duplicate subscription. Prefer its same-key
// receipt on failure; do not retry upstream reads or create a new operation.
func (s *AnimeAdminService) replayAnimeConcurrentWrite(ctx context.Context, key storage.AnimeWriteKey, out any, resultErr *error) {
	if *resultErr == nil {
		return
	}
	receipt, err := s.Store.LookupAnimeReceipt(ctx, key)
	if errors.Is(err, storage.ErrAnimeIdempotency) {
		*resultErr = err
		return
	}
	if err != nil || receipt == nil {
		return
	}
	if err := json.Unmarshal(receipt.Body, out); err != nil {
		*resultErr = fmt.Errorf("read committed anime receipt: %w", err)
		return
	}
	*resultErr = nil
}

// Compile-time guarantees keep discovery and directory dependencies read-only.
var _ AnimeDiscovery = (*bangumi.Client)(nil)
