package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"strm115/backend/internal/anime/binding"
	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
)

var ErrAnimeBindingInput = errors.New("动漫篇章或来源确认参数无效")

type animeBindingEntries interface {
	GetAnimeEntry(context.Context, string) (model.AnimeEntry, error)
}

type animeBindingSubjects interface {
	Subject(context.Context, string) (bangumi.SubjectResult, error)
}

// AnimeBindingService has no write/download dependencies. P1-07 will use these
// checks at the authenticated boundary before passing verified metadata to
// storage; a browser-supplied group name or title must not be saved directly.
type AnimeBindingService struct {
	Entries  animeBindingEntries
	Subjects animeBindingSubjects
	Mikan    *MikanService
	Anibt    *AnibtService
	Garden   *GardenService
}

type AnimeEntryRef struct {
	ID         string  `json:"id"`
	BangumiID  *string `json:"bangumiId"`
	EditionKey string  `json:"editionKey"`
}

func (r AnimeEntryRef) validate() error {
	if r.ID != "" {
		if !model.ValidAnimeID(r.ID) || r.BangumiID != nil || r.EditionKey != "" {
			return ErrAnimeBindingInput
		}
		return nil
	}
	if !validAnimeSourceText(r.EditionKey, 64) || r.BangumiID != nil && !model.ValidAnimeExternalID(*r.BangumiID) {
		return ErrAnimeBindingInput
	}
	return nil
}

type AnimeMikanCandidatesInput struct {
	Entry   *AnimeEntryRef `json:"entry"`
	Keyword string         `json:"keyword"`
	GroupID string         `json:"groupId"`
}

type AnimeMikanCandidates struct {
	Entry       *model.AnimeEntry  `json:"entry"`
	BangumiMeta *bangumi.CacheMeta `json:"bangumiMeta"`
	Items       []mikan.Bangumi    `json:"items"`
	Attempts    []MikanAttempt     `json:"attempts"`
	FetchedAt   int64              `json:"fetchedAt"`
	ExpiresAt   int64              `json:"expiresAt"`
	Truncated   bool               `json:"truncated"`
}

// SearchMikanCandidates returns entry cards, never a confirmed match. Search
// results cannot set a binding, infer a numbering offset or create local rows.
func (s *AnimeBindingService) SearchMikanCandidates(ctx context.Context, in AnimeMikanCandidatesInput) (AnimeMikanCandidates, error) {
	out := AnimeMikanCandidates{Items: []mikan.Bangumi{}, Attempts: []MikanAttempt{}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if s.Mikan == nil || in.GroupID != "" && !mikan.ValidID(in.GroupID) {
		return out, ErrAnimeBindingInput
	}
	query := strings.TrimSpace(in.Keyword)
	if query != "" {
		if !validAnimeSourceText(query, 200) {
			return out, ErrAnimeBindingInput
		}
		if _, err := mikan.NormalizeQuery(query); err != nil {
			return out, err
		}
	}
	fallback := ""
	if in.Entry != nil {
		entry, meta, err := s.resolveEntry(ctx, *in.Entry, "")
		if err != nil {
			return out, err
		}
		out.Entry, out.BangumiMeta = &entry, meta
		if query == "" {
			query, fallback = entry.Title, entry.OriginalTitle
		}
	}
	if _, err := mikan.NormalizeQuery(query); err != nil {
		return out, err
	}
	result, err := s.Mikan.Search(ctx, MikanInput{Query: query, Fallback: fallback, Group: in.GroupID})
	if err != nil {
		return out, fmt.Errorf("search anime source candidates: %w", err)
	}
	out.Attempts, out.FetchedAt, out.ExpiresAt = result.Attempts, result.FetchedAt.UnixMilli(), result.ExpiresAt.UnixMilli()
	cards := append([]mikan.Bangumi{}, result.Bangumis...)
	if result.Bangumi != nil {
		cards = append(cards, *result.Bangumi)
	}
	seen := make(map[string]bool, min(len(cards), 100))
	for _, card := range cards {
		if !mikan.ValidID(card.ID) || !validAnimeSourceText(card.Title, 1024) {
			return out, mikan.ErrParse
		}
		if seen[card.ID] {
			continue
		}
		if len(out.Items) == 100 {
			out.Truncated = true
			break
		}
		seen[card.ID] = true
		card.URL = mikan.BaseURL + "/Home/Bangumi/" + card.ID
		out.Items = append(out.Items, card)
	}
	return out, nil
}

func (s *AnimeBindingService) resolveEntry(ctx context.Context, ref AnimeEntryRef, mikanTitle string) (model.AnimeEntry, *bangumi.CacheMeta, error) {
	var entry model.AnimeEntry
	if err := ref.validate(); err != nil {
		return entry, nil, err
	}
	if ref.ID != "" {
		if s.Entries == nil {
			return entry, nil, ErrAnimeBindingInput
		}
		local, err := s.Entries.GetAnimeEntry(ctx, ref.ID)
		if err != nil {
			return entry, nil, fmt.Errorf("read anime binding entry: %w", err)
		}
		if local.ID != ref.ID || local.Validate() != nil {
			return entry, nil, ErrAnimeBindingInput
		}
		// Existing chapter identity and history survive external deletion.
		return local, nil, nil
	}
	entry.EditionKey, entry.Metadata.SchemaVersion = ref.EditionKey, model.AnimeSchemaVersion
	if ref.BangumiID == nil {
		entry.Title = mikanTitle // Only a freshly verified Mikan title is accepted.
	} else {
		if s.Subjects == nil {
			return entry, nil, ErrAnimeBindingInput
		}
		result, err := s.Subjects.Subject(ctx, *ref.BangumiID)
		if err != nil {
			return entry, nil, fmt.Errorf("read anime binding subject: %w", err)
		}
		if result.Subject.ID != *ref.BangumiID {
			return entry, nil, bangumi.ErrInvalid
		}
		id := result.Subject.ID
		entry.BangumiID, entry.Title, entry.OriginalTitle = &id, result.Subject.Title, result.Subject.OriginalTitle
		entry.Metadata.Synopsis, entry.Metadata.CoverURL, entry.Metadata.AirDate = result.Subject.Synopsis, result.Subject.CoverURL, result.Subject.Date
		// Bangumi total_episodes also counts OP/ED and other non-regular
		// chapters. Only eps may define the subscription's completion target.
		if result.Subject.Episodes != nil && *result.Subject.Episodes > 0 {
			n := *result.Subject.Episodes
			entry.Metadata.TotalEpisodes = &n
		}
		if err := entry.Validate(); err != nil {
			return entry, nil, fmt.Errorf("%w: %v", bangumi.ErrInvalid, err)
		}
		meta := result.CacheMeta
		return entry, &meta, nil
	}
	if err := entry.Validate(); err != nil {
		return entry, nil, fmt.Errorf("%w: %v", ErrAnimeBindingInput, err)
	}
	return entry, nil, nil
}

// Only server-read source metadata may replace a missing external subject.
// Keeping its matching external hint preserves the local uniqueness key;
// it does not claim that Bangumi still serves metadata for this identity.
func animeEntryFromSource(ref AnimeEntryRef, source mikan.SourceEntry, missing bool) (model.AnimeEntry, error) {
	if ref.validate() != nil || ref.ID != "" {
		return model.AnimeEntry{}, ErrAnimeBindingInput
	}
	if ref.BangumiID != nil {
		matched := false
		for _, id := range source.BangumiIDs {
			matched = matched || id == *ref.BangumiID
		}
		if !missing || !matched {
			return model.AnimeEntry{}, ErrAnimeBindingInput
		}
	}
	entry := model.AnimeEntry{BangumiID: ref.BangumiID, EditionKey: ref.EditionKey, Title: source.Title,
		Metadata: model.AnimeMetadata{SchemaVersion: model.AnimeSchemaVersion, BangumiUnavailable: missing, CoverURL: source.CoverURL, AirDate: source.AirDate}}
	if err := entry.Validate(); err != nil {
		return model.AnimeEntry{}, fmt.Errorf("%w: %v", ErrAnimeBindingInput, err)
	}
	return entry, nil
}

type AnimeBindingInput struct {
	Entry   AnimeEntryRef      `json:"entry"`
	Source  mikan.Source       `json:"source"`
	Binding model.AnimeBinding `json:"binding"`
}

type AnimeVerifiedSource struct {
	mikan.Source
	Title      string   `json:"title"`
	GroupName  string   `json:"groupName"`
	URL        string   `json:"url"`
	RSSURL     string   `json:"rssUrl"`
	BangumiIDs []string `json:"bangumiIds"`
	ObservedAt int64    `json:"observedAt"`
	ExpiresAt  int64    `json:"expiresAt"`
}

type AnimeBindingCheck struct {
	Entry        model.AnimeEntry     `json:"entry"`
	BangumiMeta  *bangumi.CacheMeta   `json:"bangumiMeta"`
	Source       AnimeVerifiedSource  `json:"source"`
	Binding      model.AnimeBinding   `json:"binding"`
	Ready        bool                 `json:"ready"`
	Issues       []string             `json:"issues"`
	Notes        []string             `json:"notes"`
	RangePreview []binding.Projection `json:"rangePreview"`
}

// CheckBinding verifies actual item/group membership and explicit scope. It
// intentionally needs no RSS success, download directory, or TMDB metadata.
// The returned confirmation is input restoration, not an activation token.
func (s *AnimeBindingService) CheckBinding(ctx context.Context, budget *AnimeSourceBudget, in AnimeBindingInput) (AnimeBindingCheck, error) {
	out := AnimeBindingCheck{Issues: []string{}, Notes: []string{}, RangePreview: []binding.Projection{}}
	if budget == nil || !in.Source.Valid() || in.Entry.validate() != nil {
		return out, ErrAnimeBindingInput
	}
	if err := in.Binding.Validate(false); err != nil {
		return out, fmt.Errorf("%w: %v", ErrAnimeBindingInput, err)
	}
	if review := in.Binding.Review; review != nil && (review.ItemID != in.Source.ItemID || review.GroupID != in.Source.GroupID) {
		return out, ErrAnimeBindingInput
	}
	ctx, cancel := context.WithDeadline(ctx, budget.deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := budget.bindSource(in.Source); err != nil {
		return out, err
	}
	missingSubject := false
	if in.Entry.ID != "" || in.Entry.BangumiID != nil {
		entry, meta, err := s.resolveEntry(ctx, in.Entry, "")
		if err != nil && !(in.Entry.ID == "" && errors.Is(err, bangumi.ErrNotFound)) {
			return out, err
		}
		missingSubject = errors.Is(err, bangumi.ErrNotFound)
		out.Entry, out.BangumiMeta = entry, meta
	}
	read, err := s.ReadEntry(ctx, budget, in.Source.Site(), in.Source.ItemID, false)
	if err != nil {
		return out, fmt.Errorf("verify anime source item: %w", err)
	}
	groupName := ""
	for _, group := range read.Entry.Groups {
		if group.ID == in.Source.GroupID {
			groupName = group.Name
			break
		}
	}
	if groupName == "" {
		return out, mikan.ErrSourceBinding
	}
	if in.Entry.ID == "" && (in.Entry.BangumiID == nil || missingSubject) {
		entry, err := animeEntryFromSource(in.Entry, read.Entry, missingSubject)
		if err != nil {
			return out, err
		}
		out.Entry = entry
	}
	rssURL, err := in.Source.RSSURL(groupName)
	if err != nil {
		return out, err
	}
	out.Source = AnimeVerifiedSource{Source: in.Source, Title: read.Entry.Title, GroupName: groupName, URL: in.Source.URL(), RSSURL: rssURL, BangumiIDs: append([]string{}, read.Entry.BangumiIDs...), ObservedAt: read.ObservedAt.UnixMilli(), ExpiresAt: read.ExpiresAt.UnixMilli()}
	out.Binding = cloneAnimeBinding(in.Binding)
	if !in.Binding.Confirmed {
		out.Issues = append(out.Issues, "SOURCE_BINDING_UNCONFIRMED")
	}
	if in.Binding.Scope == nil {
		out.Issues = append(out.Issues, "CHAPTER_SCOPE_REQUIRED")
	}
	out.Ready = len(out.Issues) == 0
	if len(out.Source.BangumiIDs) > 0 {
		out.Notes = append(out.Notes, "BANGUMI_LINK_IS_HINT")
		if out.Entry.BangumiID != nil {
			matches := false
			for _, id := range out.Source.BangumiIDs {
				if id == *out.Entry.BangumiID {
					matches = true
					break
				}
			}
			if !matches {
				out.Notes = append(out.Notes, "BANGUMI_HINT_DIFFERS")
			}
		}
	}
	if scope := out.Binding.Scope; scope != nil && scope.Kind == "range" {
		for _, n := range []int{*scope.First, *scope.Last} {
			if len(out.RangePreview) > 0 && *out.RangePreview[0].SourceEpisode == n {
				continue
			}
			projection, err := binding.Project(out.Binding, model.AnimeReleaseParse{SchemaVersion: model.AnimeSchemaVersion, Kind: "episode", Episode: &n})
			if err != nil {
				return out, fmt.Errorf("project anime source range: %w", err)
			}
			out.RangePreview = append(out.RangePreview, projection)
		}
	}
	return out, nil
}

type AnimeBindingRelease struct {
	PostKey       string                  `json:"postKey"`
	Title         string                  `json:"title"`
	Parse         model.AnimeReleaseParse `json:"parse"`
	ParserVersion int64                   `json:"parserVersion"`
	Projection    binding.Projection      `json:"projection"`
	SpecKey       string                  `json:"specKey"`
	SizeRaw       string                  `json:"sizeRaw"`
	PublishedRaw  string                  `json:"publishedRaw"`
}

type AnimeSpecOption struct {
	Match []string        `json:"match"`
	Key   string          `json:"key"`
	Label string          `json:"label"`
	Spec  model.AnimeSpec `json:"spec"`
	Count int             `json:"count"`
}

type AnimeBindingPreview struct {
	AnimeBindingCheck
	Releases           []AnimeBindingRelease `json:"releases"`
	RawCount           *int                  `json:"rawCount"`
	Completeness       string                `json:"completeness"`
	ObservedAt         *int64                `json:"observedAt"`
	SpecVersion        int64                 `json:"specVersion"`
	Profiles           []AnimeSpecOption     `json:"profiles"`
	ProfilesTruncated  bool                  `json:"profilesTruncated"`
	ProfileReviewCount int                   `json:"profileReviewCount"`
}

// PreviewBinding optionally follows metadata confirmation with one bounded
// single-group RSS read. It explains scope/numbering only, never selects rows.
func (s *AnimeBindingService) PreviewBinding(ctx context.Context, budget *AnimeSourceBudget, in AnimeBindingInput) (AnimeBindingPreview, error) {
	out := AnimeBindingPreview{Releases: []AnimeBindingRelease{}, Profiles: []AnimeSpecOption{}, SpecVersion: animeparse.SpecVersion}
	check, err := s.CheckBinding(ctx, budget, in)
	out.AnimeBindingCheck = check
	if err != nil {
		return out, err
	}
	read, err := s.ReadSource(ctx, budget, AnimeSourceInput{Source: in.Source, Kind: "rss"})
	if err != nil {
		return out, fmt.Errorf("read anime binding examples: %w", err)
	}
	observed := read.ObservedAt.UnixMilli()
	out.RawCount, out.ObservedAt, out.Completeness = &read.RawCount, &observed, read.Completeness
	out.Releases = make([]AnimeBindingRelease, 0, len(read.Releases))
	profileIndexes := make(map[string]int, min(12, len(read.Releases)))
	for _, release := range read.Releases {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		parsed, err := animeparse.Title(release.Title)
		if err != nil {
			return out, fmt.Errorf("parse anime binding example: %w", err)
		}
		projection, err := binding.Project(check.Binding, parsed)
		if err != nil {
			return out, fmt.Errorf("project anime binding example: %w", err)
		}
		key := animeparse.SpecKey(parsed.Spec)
		if parsed.SpecAmbiguous {
			out.ProfileReviewCount++
		}
		if !parsed.Spec.Empty() && !parsed.SpecAmbiguous {
			if index, ok := profileIndexes[key]; ok {
				out.Profiles[index].Count++
			} else if len(out.Profiles) < 12 {
				profileIndexes[key] = len(out.Profiles)
				out.Profiles = append(out.Profiles, AnimeSpecOption{Match: animeparse.SpecPatterns(parsed.Spec), Key: key, Label: animeparse.SpecLabel(parsed.Spec), Spec: parsed.Spec, Count: 1})
			} else {
				out.ProfilesTruncated = true
			}
		}
		out.Releases = append(out.Releases, AnimeBindingRelease{PostKey: release.PostKey, Title: release.Title, Parse: parsed, ParserVersion: animeparse.ParserVersion, Projection: projection, SpecKey: key, SizeRaw: release.SizeRaw, PublishedRaw: release.PublishedRaw})
	}
	return out, nil
}

func cloneAnimeBinding(in model.AnimeBinding) model.AnimeBinding {
	if in.Review != nil {
		review := *in.Review
		in.Review = &review
	}
	if in.Scope != nil {
		scope := *in.Scope
		if scope.First != nil {
			n := *scope.First
			scope.First = &n
		}
		if scope.Last != nil {
			n := *scope.Last
			scope.Last = &n
		}
		in.Scope = &scope
	}
	if in.ChapterCorrespondence != nil {
		copy := *in.ChapterCorrespondence
		in.ChapterCorrespondence = &copy
	}
	return in
}
