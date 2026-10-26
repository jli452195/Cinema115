package preview

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"strm115/backend/internal/anime/binding"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
)

func hexPostKey(key string) ([]byte, error) {
	b, err := hex.DecodeString(key)
	if err != nil {
		return nil, ErrInput
	}
	return b, nil
}

type Step struct {
	Stage      string `json:"stage"`
	Result     string `json:"result"` // passed, filtered, needs_confirmation
	ReasonCode string `json:"reasonCode,omitempty"`
}

type Item struct {
	Release                 model.AnimeRelease  `json:"release"`
	Observation             mikan.SourceRelease `json:"observation"`
	Publication             Publication         `json:"publication"`
	Projection              binding.Projection  `json:"projection"`
	Steps                   []Step              `json:"steps"`
	Decision                string              `json:"decision"`
	ReasonCode              string              `json:"reasonCode"`
	Selectable              bool                `json:"selectable"`
	DefaultSelected         bool                `json:"defaultSelected"`
	ConfirmationFingerprint string              `json:"confirmationFingerprint"`
}

// Pending retains the old choice even when the cumulative window loses it.
type Pending struct {
	ReleaseID   string `json:"releaseId"`
	Fingerprint string `json:"fingerprint"`
	Reason      string `json:"reason"` // changed or out_of_loaded_scope
	Snapshot    *Item  `json:"previousItem"`
}

type Changes struct {
	RetainedIDs   []string `json:"retainedIds"`
	ChangedIDs    []string `json:"changedIds"`
	UnresolvedIDs []string `json:"unresolvedIds"`
	AddedIDs      []string `json:"addedIds"`
}

type Source struct {
	ID             string           `json:"previewId"`
	Ref            string           `json:"sourceRef"`
	SourceID       string           `json:"sourceId"`
	SourceRevision int64            `json:"sourceRevision"`
	Fingerprint    string           `json:"fingerprint"`
	LoadRevision   int64            `json:"loadRevision"`
	T0             int64            `json:"t0"`
	GeneratedAt    int64            `json:"generatedAt"`
	ExpiresAt      int64            `json:"expiresAt"`
	Page           mikan.SourcePage `json:"snapshot"`
	SelectedIDs    []string         `json:"selectedIds"`
	DismissedIDs   []string         `json:"dismissedIds"`
	Pending        []Pending        `json:"pendingSelections"`
	Changes        Changes          `json:"selectionChanges"`
	Items          []Item           `json:"items"`
}

type Batch struct {
	ID          string   `json:"batchId"`
	Actor       string   `json:"-"`
	Config      Config   `json:"config"`
	Fingerprint string   `json:"fingerprint"`
	CreatedAt   int64    `json:"createdAt"`
	Sources     []Source `json:"sources"`
}

func (b *Batch) OrderSources() {
	order := make(map[string]int, len(b.Config.Sources))
	for i, source := range b.Config.Sources {
		order[source.Ref] = i
	}
	sort.SliceStable(b.Sources, func(i, j int) bool { return order[b.Sources[i].Ref] < order[b.Sources[j].Ref] })
}

type Budget struct {
	TotalRawCount int `json:"totalRawCount"`
	Limit         int `json:"limit"`
	Remaining     int `json:"remaining"`
}

func (b Batch) Budget() Budget {
	total := 0
	for _, s := range b.Sources {
		total += s.Page.RawCount
	}
	return Budget{total, mikan.AnimeMaxRows, mikan.AnimeMaxRows - total}
}

func (s Source) ValidAt(now int64) bool { return now < s.ExpiresAt }

func evaluate(binding model.AnimeBinding, rules model.AnimeRules, item *Item) error {
	compiled, err := CompileRules(rules)
	if err != nil {
		return err
	}
	return evaluateCompiled(binding, rules, compiled, item)
}

func evaluateCompiled(sourceBinding model.AnimeBinding, rules model.AnimeRules, compiled SourceRules, item *Item) error {
	item.Steps = []Step{{Stage: "source", Result: "passed"}}
	stop := func(stage, result, code string) {
		item.Steps = append(item.Steps, Step{stage, result, code})
		item.Decision, item.ReasonCode = result, code
	}
	pass := func(stage string) { item.Steps = append(item.Steps, Step{Stage: stage, Result: "passed"}) }
	// A batch title is only a candidate. All match/exclude/numbering rules are
	// applied to its actual files before a download can be claimed.
	if item.Release.Parse.Kind == "batch" && sourceBinding.Validate(true) == nil && rules.MinimumEpisode != nil {
		item.Decision, item.ReasonCode, item.Selectable = "matched", "BATCH_FILES_PENDING", true
		item.Steps = append(item.Steps, Step{Stage: "files", Result: "needs_confirmation", ReasonCode: item.ReasonCode})
		return nil
	}
	if code := compiled.Filter(item.Release.Title); code != "" {
		stop("patterns", "filtered", code)
		return nil
	}
	pass("patterns")
	p, err := binding.Project(sourceBinding, item.Release.Parse)
	if err != nil {
		return err
	}
	item.Projection = p
	if item.Release.Parse.Kind != "episode" || item.Release.Parse.Episode == nil {
		stop("number", "needs_confirmation", "EPISODE_NUMBER_UNRESOLVED")
		return nil
	}
	pass("number")
	if p.Status != "in_scope" {
		result := "needs_confirmation"
		if p.Status == "out_of_scope" {
			result = "filtered"
		}
		stop("scope", result, p.ReasonCode)
		return nil
	}
	pass("scope")
	if rules.MinimumEpisode == nil {
		stop("minimum", "needs_confirmation", "SOURCE_RULES_INCOMPLETE")
		return nil
	}
	if *item.Release.Parse.Episode < *rules.MinimumEpisode {
		stop("minimum", "filtered", "BELOW_SOURCE_MINIMUM")
		return nil
	}
	pass("minimum")
	// No info-hash has been verified in P1. This is not a download decision.
	item.Steps = append(item.Steps, Step{Stage: "identity", Result: "needs_confirmation", ReasonCode: "HASH_NOT_VERIFIED"})
	item.Decision, item.ReasonCode, item.Selectable = "matched", "MATCHED_METADATA_ONLY", true
	return nil
}

// MatchesRelease reuses the preview's metadata rules at the execution boundary.
// It does not grant a selection or verify the resource identity.
func MatchesRelease(binding model.AnimeBinding, rules model.AnimeRules, release model.AnimeRelease) (bool, error) {
	match, err := CompileMatcher(binding, rules)
	if err != nil {
		return false, err
	}
	return match(release)
}

// CompileMatcher shares the execution rules without recompiling expressions
// for each row of a local coverage check.
func CompileMatcher(binding model.AnimeBinding, rules model.AnimeRules) (func(model.AnimeRelease) (bool, error), error) {
	compiled, err := CompileRules(rules)
	if err != nil {
		return nil, err
	}
	return func(release model.AnimeRelease) (bool, error) {
		item := Item{Release: release}
		if err := evaluateCompiled(binding, rules, compiled, &item); err != nil {
			return false, err
		}
		return item.Selectable, nil
	}, nil
}

// Build interprets one already verified single-group page. Release IDs are
// attached by the repository later; post keys are not trusted BT identities.
func Build(c Config, ref string, page mikan.SourcePage, now int64) (Source, error) {
	return buildSource(c, ref, page, now, true)
}

// BuildCandidates leaves version ranking to discovery, which must first apply
// publication boundaries and durable episode claims to the entire read window.
func BuildCandidates(c Config, ref string, page mikan.SourcePage, now int64) (Source, error) {
	return buildSource(c, ref, page, now, false)
}

func buildSource(c Config, ref string, page mikan.SourcePage, now int64, rankHistory bool) (Source, error) {
	out := Source{Ref: ref, LoadRevision: 1, GeneratedAt: now, ExpiresAt: now + LifetimeMillis,
		SelectedIDs: []string{}, DismissedIDs: []string{}, Pending: []Pending{}, Items: make([]Item, 0, len(page.Releases)),
		Changes: emptyChanges()}
	var err error
	c, err = Normalize(c)
	if err != nil {
		return out, err
	}
	var config *model.AnimeSourceConfig
	for i := range c.Sources {
		if c.Sources[i].Ref == ref {
			config = &c.Sources[i]
			break
		}
	}
	if config == nil || config.ItemID == nil || config.GroupID == nil ||
		page.Source != (animefeed.NewSource(config.Provider, *config.ItemID, *config.GroupID)) ||
		(page.Kind != "history" && page.Kind != "rss") || page.ObservedAt.IsZero() || page.ObservedAt.UnixMilli() <= 0 || now < page.ObservedAt.UnixMilli() ||
		page.RawCount < len(page.Releases) || page.RawCount > mikan.AnimeMaxRows ||
		(page.Completeness != "partial" && page.Completeness != "visible_end") ||
		page.Kind == "history" && (page.RequestedTake < 1 || page.RequestedTake > mikan.AnimeMaxRows) {
		return out, ErrInput
	}
	if page.Completeness == "visible_end" && (page.Kind != "history" || (page.RawCount >= page.RequestedTake && !((page.Source.Site() == "anibt" || page.Source.Site() == "garden") && page.Total != nil && *page.Total == page.RawCount)) || page.StopReason != "") {
		return out, ErrInput
	}
	fp, err := SourceFingerprint(c, ref)
	if err != nil {
		return out, err
	}
	out.Fingerprint, out.SourceID, out.SourceRevision = fp, config.ID, config.ExpectedRevision
	out.Page = page
	// A minute is the conservative fallback for empty/undated snapshots. For
	// known dates the coarsest represented precision governs the source T0.
	precision := int64(0)
	seen := make(map[string]bool, len(page.Releases))
	compiled, err := CompileRules(config.Rules)
	if err != nil {
		return out, err
	}
	for _, row := range page.Releases {
		if seen[row.PostKey] {
			continue
		}
		if page.Source.Site() == "mikan" {
			if row.PostKey != strings.ToLower(row.PostKey) || len(row.PostKey) != 40 {
				return out, ErrInput
			}
			if _, err := hexPostKey(row.PostKey); err != nil {
				return out, err
			}
		} else if !strings.HasPrefix(row.PostKey, "rel_") || !model.ValidAnimeID(row.PostKey) {
			return out, ErrInput
		}
		seen[row.PostKey] = true
		parse, err := compiled.Parse(row.Title)
		if err != nil {
			return out, fmt.Errorf("parse preview title: %w", err)
		}
		published, err := Published(row.PublishedRaw, row.AlternatePublishedRaw, page.ObservedAt)
		if err != nil {
			return out, fmt.Errorf("parse preview publication: %w", err)
		}
		if published.PrecisionMillis > precision {
			precision = published.PrecisionMillis
		}
		content, err := Digest(row)
		if err != nil {
			return out, err
		}
		r := model.AnimeRelease{Provider: page.Source.Site(), PostKey: row.PostKey, Title: row.Title, TimeVersion: TimeVersion,
			PublishedAt: published.UnixMillis, Parse: parse, ParserVersion: releaseparse.ParserVersion, ContentFingerprint: content, ObservedAt: page.ObservedAt.UnixMilli()}
		if row.OriginalGUID != "" {
			value := row.OriginalGUID
			r.OriginalGUID = &value
		}
		if row.PublishedRaw != "" {
			value := row.PublishedRaw
			r.PublishedRaw = &value
		}
		if err := r.Validate(); err != nil {
			return out, fmt.Errorf("validate preview release: %w", err)
		}
		item := Item{Release: r, Observation: row, Publication: published}
		if err := evaluateCompiled(config.Binding, config.Rules, compiled, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	// RSS candidates are ranked by the repository after current rule and
	// durable authorization checks, so an ineligible revision cannot win.
	if rankHistory && page.Kind == "history" {
		PreferCurrentVersions(out.Items)
	}
	for i := range out.Items {
		item := &out.Items[i]
		item.ConfirmationFingerprint, err = Digest(struct {
			Source, Content string
			Projection      binding.Projection
			Selectable      bool
		}{fp, item.Release.ContentFingerprint, item.Projection, item.Selectable})
		if err != nil {
			return out, err
		}
		item.DefaultSelected = item.Selectable
	}
	if precision == 0 {
		precision = 60000
	}
	out.T0 = page.ObservedAt.UnixMilli() / precision * precision
	return out, nil
}

func emptyChanges() Changes { return Changes{[]string{}, []string{}, []string{}, []string{}} }

func (s *Source) InitializeSelections() {
	for _, item := range s.Items {
		if item.DefaultSelected {
			s.SelectedIDs = append(s.SelectedIDs, item.Release.ID)
		}
	}
	sort.Strings(s.SelectedIDs)
}

// Reconcile never defaults newly observed rows into an existing choice. Old
// snapshots survive separately until retained, explicitly selected or dismissed.
func (s *Source) Reconcile(old Source) {
	s.Changes = emptyChanges()
	s.SelectedIDs, s.Pending = []string{}, []Pending{}
	s.DismissedIDs = append([]string{}, old.DismissedIDs...)
	current, previous := make(map[string]Item, len(s.Items)), make(map[string]Item, len(old.Items))
	for _, i := range s.Items {
		current[i.Release.ID] = i
	}
	for _, i := range old.Items {
		previous[i.Release.ID] = i
	}
	wanted := make(map[string]Pending, len(old.SelectedIDs)+len(old.Pending))
	for _, id := range old.SelectedIDs {
		item := previous[id]
		wanted[id] = Pending{ReleaseID: id, Fingerprint: item.ConfirmationFingerprint, Snapshot: &item}
	}
	for _, p := range old.Pending {
		wanted[p.ReleaseID] = p
	}
	for id, p := range wanted {
		item, exists := current[id]
		switch {
		case !exists:
			p.Reason = "out_of_loaded_scope"
			s.Pending = append(s.Pending, p)
			s.Changes.UnresolvedIDs = append(s.Changes.UnresolvedIDs, id)
		case !item.Selectable || item.ConfirmationFingerprint != p.Fingerprint:
			p.Reason = "changed"
			s.Pending = append(s.Pending, p)
			s.Changes.ChangedIDs = append(s.Changes.ChangedIDs, id)
		default:
			s.SelectedIDs = append(s.SelectedIDs, id)
			s.Changes.RetainedIDs = append(s.Changes.RetainedIDs, id)
		}
	}
	s.T0 = old.T0
	for i := range s.Items {
		item := &s.Items[i]
		item.DefaultSelected = false
		if _, ok := previous[item.Release.ID]; !ok {
			s.Changes.AddedIDs = append(s.Changes.AddedIDs, item.Release.ID)
		}

	}
	sort.Strings(s.SelectedIDs)
	sort.Slice(s.Pending, func(i, j int) bool { return s.Pending[i].ReleaseID < s.Pending[j].ReleaseID })
	for _, ids := range [][]string{s.Changes.RetainedIDs, s.Changes.ChangedIDs, s.Changes.UnresolvedIDs, s.Changes.AddedIDs} {
		sort.Strings(ids)
	}
}

func (s *Source) Select(selected, dismissed []string, now int64) error {
	if !s.ValidAt(now) {
		return ErrExpired
	}
	if len(selected) > 2000 || len(dismissed) > 4000 {
		return ErrInput
	}
	items := make(map[string]Item, len(s.Items))
	pending := make(map[string]bool, len(s.Pending))
	for _, i := range s.Items {
		items[i.Release.ID] = i
	}
	for _, p := range s.Pending {
		pending[p.ReleaseID] = true
	}
	sel, dis := make(map[string]bool, len(selected)), make(map[string]bool, len(dismissed)+len(s.DismissedIDs))
	for _, id := range selected {
		if sel[id] || !items[id].Selectable {
			return ErrInput
		}
		sel[id] = true
	}
	for _, id := range s.DismissedIDs {
		dis[id] = true
	}
	for _, id := range dismissed {
		_, exists := items[id]
		if sel[id] || !exists && !pending[id] {
			return ErrInput
		}
		dis[id] = true
	}
	// Removing a selected row is an explicit dismissal, not a future default.
	for _, id := range s.SelectedIDs {
		if !sel[id] {
			dis[id] = true
		}
	}
	for id := range sel {
		delete(dis, id)
	}
	remaining := make([]Pending, 0, len(s.Pending))
	for _, p := range s.Pending {
		if !sel[p.ReleaseID] && !dis[p.ReleaseID] {
			remaining = append(remaining, p)
		}
	}
	s.SelectedIDs = append([]string{}, selected...)
	sort.Strings(s.SelectedIDs)
	s.DismissedIDs = make([]string, 0, len(dis))
	for id := range dis {
		s.DismissedIDs = append(s.DismissedIDs, id)
	}
	sort.Strings(s.DismissedIDs)
	s.Pending = remaining
	return nil
}

func (s Source) ValidateSelection(now int64) error {
	if !s.ValidAt(now) {
		return ErrExpired
	}
	if len(s.Pending) > 0 {
		return ErrReview
	}
	copy := s
	return copy.Select(s.SelectedIDs, nil, now)
}
