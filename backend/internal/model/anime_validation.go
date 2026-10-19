package model

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"strm115/backend/internal/client/animefeed"
	"unicode/utf8"
)

// DecodeAnimeJSON is shared by stored DTOs and future request boundaries.
// Unknown fields and multiple JSON values are rejected rather than discarded.
func DecodeAnimeJSON(r io.Reader, dst any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("decode anime JSON: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple anime JSON values")
		}
		return fmt.Errorf("read anime JSON end: %w", err)
	}
	return nil
}

func ValidAnimeID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func ValidAnimeExternalID(id string) bool {
	if len(id) == 0 || len(id) > 64 || id[0] == '0' {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func animeText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= max && (!required || strings.TrimSpace(s) != "")
}

func animeEpisode(n int) bool    { return n >= 1 && n <= 99999 }
func animeRevision(n int64) bool { return n >= 0 && n < AnimeMaxRevision }

func (e AnimeEntry) Validate() error {
	if e.ID != "" && !ValidAnimeID(e.ID) || !animeRevision(e.Revision) {
		return fmt.Errorf("invalid entry identity/revision")
	}
	if e.BangumiID != nil && !ValidAnimeExternalID(*e.BangumiID) {
		return fmt.Errorf("invalid Bangumi ID")
	}
	if !animeText(e.EditionKey, 64, true) || !animeText(e.Title, 512, true) || !animeText(e.OriginalTitle, 512, false) {
		return fmt.Errorf("invalid entry title/edition")
	}
	m := e.Metadata
	if m.SchemaVersion != AnimeSchemaVersion || !animeText(m.Synopsis, 16000, false) || !animeText(m.CoverURL, 2048, false) || !animeText(m.AirDate, 64, false) || m.TotalEpisodes != nil && (*m.TotalEpisodes < 0 || *m.TotalEpisodes > 99999) {
		return fmt.Errorf("invalid entry metadata")
	}
	return nil
}

func (b AnimeBinding) Validate(complete bool) error {
	if b.SchemaVersion != AnimeSchemaVersion {
		return fmt.Errorf("unsupported binding schema")
	}
	if complete && (!b.Confirmed || b.Scope == nil) || b.Confirmed && b.Scope == nil {
		return fmt.Errorf("binding is incomplete")
	}
	if r := b.Review; r != nil && (r.Method != "source_review" || !ValidAnimeExternalID(r.ItemID) || !(animefeed.ValidGroup("mikan", r.GroupID) || animefeed.ValidGroup("anibt", r.GroupID) || animefeed.ValidGroup("garden", r.GroupID)) || !b.Confirmed || b.Scope == nil) {
		return fmt.Errorf("invalid source review")
	}
	if s := b.Scope; s != nil {
		switch s.Kind {
		case "feed":
			if s.First != nil || s.Last != nil || s.Evidence != "" || b.ChapterCorrespondence != nil || b.Confirmed && b.Review == nil {
				return fmt.Errorf("invalid feed scope")
			}
		case "range":
			if s.First == nil || s.Last == nil || !animeEpisode(*s.First) || !animeEpisode(*s.Last) || *s.First > *s.Last || s.Evidence != "" {
				return fmt.Errorf("invalid binding range")
			}
		case "confirmed_chapter":
			if s.First != nil || s.Last != nil || !animeText(s.Evidence, 512, false) || b.Confirmed && strings.TrimSpace(s.Evidence) == "" && b.Review == nil {
				return fmt.Errorf("chapter confirmation needs evidence")
			}
		default:
			return fmt.Errorf("invalid binding scope")
		}
	}
	if c := b.ChapterCorrespondence; c != nil && (!animeEpisode(c.SourceEpisode) || !animeEpisode(c.ChapterEpisode)) {
		return fmt.Errorf("invalid chapter correspondence")
	}
	if s, c := b.Scope, b.ChapterCorrespondence; s != nil && s.Kind == "range" && c != nil {
		if !animeEpisode(*s.First-c.SourceEpisode+c.ChapterEpisode) || !animeEpisode(*s.Last-c.SourceEpisode+c.ChapterEpisode) {
			return fmt.Errorf("chapter correspondence exceeds valid numbering within scope")
		}
	}
	return nil
}

func (r AnimeRules) Validate(complete bool) error {
	if r.SchemaVersion != AnimeSchemaVersion {
		return fmt.Errorf("unsupported rules schema")
	}
	if complete && (r.Resolution == nil || r.MinimumEpisode == nil || r.InitialMode == "") {
		return fmt.Errorf("rules are incomplete")
	}
	if r.SpecMode != "" && r.SpecMode != "any" && r.SpecMode != "profiles" || len(r.SpecProfiles) > 12 || r.SpecMode != "profiles" && len(r.SpecProfiles) != 0 || complete && r.SpecMode == "profiles" && len(r.SpecProfiles) == 0 {
		return fmt.Errorf("invalid specification selection")
	}
	for _, spec := range r.SpecProfiles {
		if err := spec.Validate(); err != nil {
			return err
		}
		if spec.Empty() {
			return fmt.Errorf("empty specification profile")
		}
	}
	if r.Resolution != nil {
		switch *r.Resolution {
		case "720p", "1080p", "2160p", "any":
		default:
			return fmt.Errorf("invalid resolution")
		}
	}
	if r.MinimumEpisode != nil && !animeEpisode(*r.MinimumEpisode) {
		return fmt.Errorf("invalid minimum episode")
	}
	switch r.InitialMode {
	case "", "from_episode", "new_only", "selected_history":
	default:
		return fmt.Errorf("invalid initial mode")
	}
	for _, words := range [][]string{r.Include, r.Exclude} {
		if len(words) > 10 {
			return fmt.Errorf("too many filter words")
		}
		seen := make(map[string]bool, len(words))
		for _, word := range words {
			if !animeText(word, 64, true) || seen[word] {
				return fmt.Errorf("invalid/duplicate filter word")
			}
			seen[word] = true
		}
	}
	return nil
}

func (s AnimeSourceConfig) Validate() error {
	if !ValidAnimeID(s.Ref) || s.ID != "" && !ValidAnimeID(s.ID) || !animeRevision(s.ExpectedRevision) || (s.ID == "") != (s.ExpectedRevision == 0) {
		return fmt.Errorf("invalid source identity/revision")
	}
	if s.Status != "draft" && s.Status != "paused" {
		return fmt.Errorf("P1 source must be draft or paused")
	}
	if s.Role != "main" && s.Role != "standby" || s.Position < 0 || s.Position >= AnimeMaxSources || !animefeed.ValidProvider(s.Provider) {
		return fmt.Errorf("invalid source role/position/provider")
	}
	if s.ItemID != nil && !animefeed.ValidItem(*s.ItemID) || s.GroupID != nil && !animefeed.ValidGroup(s.Provider, *s.GroupID) {
		return fmt.Errorf("invalid source item/group identity")
	}
	if s.GroupName != nil && !animeText(*s.GroupName, 256, true) {
		return fmt.Errorf("invalid group name")
	}
	complete := s.Status == "paused"
	if complete && (s.ItemID == nil || s.GroupID == nil || s.GroupName == nil) {
		return fmt.Errorf("source is incomplete")
	}
	if err := s.Binding.Validate(complete); err != nil {
		return err
	}
	if r := s.Binding.Review; r != nil && (s.ItemID == nil || s.GroupID == nil || *s.ItemID != r.ItemID || *s.GroupID != r.GroupID) {
		return fmt.Errorf("source review belongs to another publisher")
	}
	return s.Rules.Validate(complete)
}

func (c AnimeSubscriptionConfig) Validate() error {
	if c.Naming != nil {
		if err := c.Naming.Validate(); err != nil {
			return err
		}
	}
	if c.ID != "" && !ValidAnimeID(c.ID) || !ValidAnimeID(c.EntryID) || !animeRevision(c.ExpectedRevision) || (c.ID == "") != (c.ExpectedRevision == 0) {
		return fmt.Errorf("invalid subscription identity/revision")
	}
	if c.Status != "draft" && c.Status != "paused" {
		return fmt.Errorf("P1 subscription must be draft or paused")
	}
	if len(c.Sources) > AnimeMaxSources || len(c.RemovedSources) > AnimeMaxSources {
		return fmt.Errorf("too many source changes")
	}
	if c.Form.SchemaVersion != AnimeSchemaVersion || len(c.Form.FormSelections) > AnimeMaxSources || len(c.Form.PreviewRefs) > AnimeMaxSources {
		return fmt.Errorf("invalid saved form")
	}
	refs := make(map[string]bool, len(c.Sources))
	ids := make(map[string]bool, len(c.Sources)+len(c.RemovedSources))
	for _, s := range c.Sources {
		if err := s.Validate(); err != nil {
			return err
		}
		if refs[s.Ref] || s.ID != "" && ids[s.ID] {
			return fmt.Errorf("duplicate source change")
		}
		refs[s.Ref] = true
		if s.ID != "" {
			ids[s.ID] = true
		}
	}
	for _, s := range c.RemovedSources {
		if !ValidAnimeID(s.ID) || s.ExpectedRevision < 1 || !animeRevision(s.ExpectedRevision) || ids[s.ID] {
			return fmt.Errorf("invalid source removal")
		}
		ids[s.ID] = true
	}
	selectionRefs := make(map[string]bool, len(c.Form.FormSelections))
	for _, f := range c.Form.FormSelections {
		if !ValidAnimeID(f.SourceRef) || selectionRefs[f.SourceRef] || len(f.SelectedReleaseIDs)+len(f.UnresolvedReleaseIDs)+len(f.DismissedReleaseIDs) > 2000 {
			return fmt.Errorf("invalid form selection")
		}
		selectionRefs[f.SourceRef] = true
		seen := make(map[string]bool, len(f.SelectedReleaseIDs)+len(f.UnresolvedReleaseIDs))
		for _, list := range [][]string{f.SelectedReleaseIDs, f.UnresolvedReleaseIDs, f.DismissedReleaseIDs} {
			for _, id := range list {
				if !ValidAnimeID(id) || seen[id] {
					return fmt.Errorf("invalid selected release")
				}
				seen[id] = true
			}
		}
	}
	previewSources := make(map[string]bool, len(c.Form.PreviewRefs))
	for _, p := range c.Form.PreviewRefs {
		if !ValidAnimeID(p.BatchID) || !ValidAnimeID(p.SourceRef) || !ValidAnimeID(p.SourcePreviewID) || p.SourceID != "" && !ValidAnimeID(p.SourceID) || p.LoadRevision < 1 || p.LoadRevision > AnimeMaxRevision || previewSources[p.SourceRef] {
			return fmt.Errorf("invalid saved preview reference")
		}
		previewSources[p.SourceRef] = true
	}
	return nil
}

func (p AnimeReleaseParse) Validate() error {
	if p.SchemaVersion != AnimeSchemaVersion {
		return fmt.Errorf("unsupported parse schema")
	}
	switch p.Kind {
	case "unknown", "episode", "special", "batch", "composite":
	default:
		return fmt.Errorf("invalid parsed release kind")
	}
	if p.Episode != nil && (!animeEpisode(*p.Episode) || p.Kind != "episode") {
		return fmt.Errorf("invalid parsed episode")
	}
	if !animeText(p.RawEpisode, 128, false) || !animeText(p.VersionLabel, 64, false) {
		return fmt.Errorf("invalid parse text")
	}
	if p.Resolution != nil {
		switch *p.Resolution {
		case "720p", "1080p", "2160p", "unknown":
		default:
			return fmt.Errorf("invalid parsed resolution")
		}
	}
	return p.Spec.Validate()
}

func (r AnimeRelease) Validate() error {
	if r.ID != "" && !ValidAnimeID(r.ID) || !animefeed.ValidProvider(r.Provider) || !ValidAnimeID(r.PostKey) || !animeText(r.Title, 1024, true) || !animeText(r.ContentFingerprint, 128, true) {
		return fmt.Errorf("invalid release identity/content")
	}
	if r.TimeVersion < 1 || r.TimeVersion > AnimeMaxRevision || r.ParserVersion < 1 || r.ParserVersion > AnimeMaxRevision || r.ObservedAt <= 0 {
		return fmt.Errorf("invalid release observation/version")
	}
	for _, s := range []*string{r.OriginalGUID, r.PublishedRaw} {
		if s != nil && !animeText(*s, 2048, false) {
			return fmt.Errorf("invalid release text")
		}
	}
	return r.Parse.Validate()
}
