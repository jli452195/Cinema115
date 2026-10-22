package model

import (
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Naming is subscription-owned configuration, never the site's TV numbering.
// Metadata is a server-observed snapshot; source references become stable IDs
// in the same transaction that saves the subscription and its sources.
type AnimeNamingInput struct {
	TMDBID        string               `json:"tmdbId"`
	GroupID       string               `json:"groupId"`
	Season        int                  `json:"season"`
	Fingerprint   string               `json:"fingerprint"`
	LoadedSeasons []int                `json:"loadedSeasons,omitempty"`
	Sources       []AnimeSourceMapping `json:"sources"`
}
type AnimeSourceMapping struct {
	SourceRef string                 `json:"sourceRef"`
	Segments  []AnimeMappingSegment  `json:"segments"`
	Overrides []AnimeMappingOverride `json:"overrides"`
}
type AnimeMappingSegment struct {
	First        int  `json:"first"`
	Last         *int `json:"last"`
	SourceBase   int  `json:"sourceBase"`
	TargetSeason int  `json:"targetSeason"`
	TargetBase   int  `json:"targetBase"`
}
type AnimeMappingOverride struct {
	RawKind       string `json:"rawKind"`
	RawNumber     string `json:"rawNumber"`
	Action        string `json:"action"`
	TargetSeason  int    `json:"targetSeason"`
	TargetEpisode int    `json:"targetEpisode"`
}
type AnimeNamingEpisode struct {
	ID           string `json:"id"`
	Number       int    `json:"number"`
	Name         string `json:"name"`
	Date         string `json:"date,omitempty"`
	SourceSeason int    `json:"sourceSeason"`
	SourceNumber int    `json:"sourceNumber"`
}
type AnimeNamingSeason struct {
	Number       int                  `json:"number"`
	Name         string               `json:"name"`
	Date         string               `json:"date"`
	EpisodeCount int                  `json:"episodeCount"`
	Episodes     []AnimeNamingEpisode `json:"episodes"`
}
type AnimeNamingTarget struct {
	TMDBID      string              `json:"tmdbId"`
	Title       string              `json:"title"`
	Year        string              `json:"year"`
	GroupID     string              `json:"groupId"`
	GroupName   string              `json:"groupName"`
	Season      int                 `json:"season"`
	Seasons     []AnimeNamingSeason `json:"seasons"`
	Fingerprint string              `json:"fingerprint"`
}
type AnimeNamingPlan struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Target        AnimeNamingTarget    `json:"target"`
	Sources       []AnimeSourceMapping `json:"sources"`
}
type AnimeNamingProjection struct {
	Status      string `json:"status"`
	Season      *int   `json:"season"`
	Episode     *int   `json:"episode"`
	EpisodeID   string `json:"episodeId,omitempty"`
	EpisodeName string `json:"episodeName,omitempty"`
	PathStem    string `json:"pathStem,omitempty"`
}

func (in AnimeNamingInput) Validate() error {
	if !ValidAnimeExternalID(in.TMDBID) || in.Season < 0 || in.Season > 1000 || len(in.GroupID) > 64 || len(in.Fingerprint) != 64 || len(in.Sources) > AnimeMaxSources {
		return fmt.Errorf("invalid naming target")
	}
	if _, err := hex.DecodeString(in.Fingerprint); err != nil {
		return fmt.Errorf("invalid naming fingerprint")
	}
	if len(in.LoadedSeasons) > 13 {
		return fmt.Errorf("too many loaded seasons")
	}
	loaded := make(map[int]bool, len(in.LoadedSeasons))
	for _, n := range in.LoadedSeasons {
		if n < 0 || n > 1000 || loaded[n] {
			return fmt.Errorf("invalid loaded season")
		}
		loaded[n] = true
	}
	for _, c := range in.GroupID {
		if !unicode.IsDigit(c) && !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return fmt.Errorf("invalid naming group")
		}
	}
	seen := make(map[string]bool, len(in.Sources))
	for _, s := range in.Sources {
		if !ValidAnimeID(s.SourceRef) || seen[s.SourceRef] {
			return fmt.Errorf("invalid naming source")
		}
		seen[s.SourceRef] = true
		if err := s.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (m AnimeSourceMapping) Validate() error {
	if len(m.Segments) > 12 || len(m.Overrides) > 100 {
		return fmt.Errorf("too many naming rules")
	}
	for i, s := range m.Segments {
		if !animeReleaseNumber(s.First) || !animeReleaseNumber(s.SourceBase) || !animeReleaseNumber(s.TargetBase) || s.TargetSeason < 0 || s.TargetSeason > 1000 || s.Last != nil && (!animeReleaseNumber(*s.Last) || *s.Last < s.First) || s.First-s.SourceBase+s.TargetBase < 0 {
			return fmt.Errorf("invalid naming segment")
		}
		end := 99999
		if s.Last != nil {
			end = *s.Last
			if end-s.SourceBase+s.TargetBase > 99999 {
				return fmt.Errorf("invalid naming end")
			}
		}
		for _, p := range m.Segments[:i] {
			pe := 99999
			if p.Last != nil {
				pe = *p.Last
			}
			if s.First <= pe && p.First <= end {
				return fmt.Errorf("overlapping naming segments")
			}
			if p.TargetSeason == s.TargetSeason && s.First-s.SourceBase+s.TargetBase <= pe-p.SourceBase+p.TargetBase && p.First-p.SourceBase+p.TargetBase <= end-s.SourceBase+s.TargetBase {
				return fmt.Errorf("colliding naming targets")
			}
		}
	}
	seen := make(map[string]bool, len(m.Overrides))
	targets := make(map[string]bool, len(m.Overrides))
	for _, o := range m.Overrides {
		key := o.RawKind + ":" + o.RawNumber
		if (o.RawKind != "episode" && o.RawKind != "special" && o.RawKind != "composite") || !animeText(o.RawNumber, 64, true) || seen[key] || strings.ContainsFunc(o.RawNumber, unicode.IsControl) || (o.Action != "map" && o.Action != "ignore") {
			return fmt.Errorf("invalid naming override")
		}
		seen[key] = true
		if o.RawKind == "episode" {
			n, err := strconv.Atoi(o.RawNumber)
			if err != nil || !animeReleaseNumber(n) || strconv.Itoa(n) != o.RawNumber {
				return fmt.Errorf("invalid ordinary override number")
			}
		}
		if o.Action == "ignore" {
			if o.TargetSeason != 0 || o.TargetEpisode != 0 {
				return fmt.Errorf("ignored target must be empty")
			}
			continue
		}
		if o.TargetSeason < 0 || o.TargetSeason > 1000 || !animeReleaseNumber(o.TargetEpisode) {
			return fmt.Errorf("invalid override target")
		}
		target := fmt.Sprintf("%d:%d", o.TargetSeason, o.TargetEpisode)
		if targets[target] {
			return fmt.Errorf("colliding override targets")
		}
		targets[target] = true
	}
	for _, o := range m.Overrides {
		if o.Action != "map" {
			continue
		}
		for _, s := range m.Segments {
			if o.TargetSeason != s.TargetSeason {
				continue
			}
			n := o.TargetEpisode - s.TargetBase + s.SourceBase
			if n < s.First || s.Last != nil && n > *s.Last || o.RawKind == "episode" && o.RawNumber == strconv.Itoa(n) {
				continue
			}
			replaced := false
			for _, prior := range m.Overrides {
				if prior.RawKind == "episode" && prior.RawNumber == strconv.Itoa(n) {
					replaced = true
					break
				}
			}
			if !replaced {
				return fmt.Errorf("override collides with ordinary episode")
			}
		}
	}
	return nil
}
func (p AnimeNamingPlan) Validate() error {
	if p.SchemaVersion != 1 || !animeText(p.Target.Title, 512, true) || len(p.Target.Year) != 4 || len(p.Target.Seasons) > 1001 {
		return fmt.Errorf("invalid naming snapshot")
	}
	for _, r := range p.Target.Year {
		if r < '0' || r > '9' {
			return fmt.Errorf("invalid naming year")
		}
	}
	seasons := make(map[int]bool, len(p.Target.Seasons))
	total := 0
	for _, s := range p.Target.Seasons {
		if s.Number < 0 || s.Number > 1000 || seasons[s.Number] || !animeText(s.Name, 512, false) {
			return fmt.Errorf("invalid naming season")
		}
		seasons[s.Number] = true
		numbers := make(map[int]bool, len(s.Episodes))
		for _, e := range s.Episodes {
			total++
			if !ValidAnimeExternalID(e.ID) || !animeReleaseNumber(e.Number) || numbers[e.Number] || !animeText(e.Name, 512, false) || !animeText(e.Date, 64, false) || total > 5000 {
				return fmt.Errorf("invalid naming episode")
			}
			numbers[e.Number] = true
		}
	}
	if !seasons[p.Target.Season] {
		return fmt.Errorf("selected season is absent")
	}
	return (AnimeNamingInput{TMDBID: p.Target.TMDBID, GroupID: p.Target.GroupID, Season: p.Target.Season, Fingerprint: p.Target.Fingerprint, Sources: p.Sources}).Validate()
}

// Project never mutates source numbering, download minima, or other versions.
// Specials require an explicit override; TMDB episode details only enrich names.
func (p AnimeNamingPlan) Project(sourceRef string, parsed AnimeReleaseParse, group string) AnimeNamingProjection {
	out := AnimeNamingProjection{Status: "unmapped"}
	var mapping *AnimeSourceMapping
	for i := range p.Sources {
		if p.Sources[i].SourceRef == sourceRef {
			mapping = &p.Sources[i]
			break
		}
	}
	if mapping == nil {
		return out
	}
	season, episode, found := 0, 0, false
	rawNumber := parsed.RawEpisode
	if parsed.Kind == "episode" && parsed.Episode != nil {
		rawNumber = strconv.Itoa(*parsed.Episode)
	}
	for _, o := range mapping.Overrides {
		if o.RawKind == parsed.Kind && o.RawNumber == rawNumber {
			if o.Action == "ignore" {
				out.Status = "ignored"
				return out
			}
			season, episode, found = o.TargetSeason, o.TargetEpisode, true
			break
		}
	}
	if !found && parsed.Kind == "episode" && parsed.Episode != nil {
		for _, s := range mapping.Segments {
			n := *parsed.Episode
			if n >= s.First && (s.Last == nil || n <= *s.Last) {
				season, episode, found = s.TargetSeason, n-s.SourceBase+s.TargetBase, true
				break
			}
		}
	}
	if !found || !animeReleaseNumber(episode) {
		return out
	}
	out.Season = &season
	out.Episode = &episode
	out.Status = "mapped"
	out.PathStem = AnimeNamingPath(p.Target, season, episode, group, "")
	for _, s := range p.Target.Seasons {
		if s.Number != season {
			continue
		}
		for _, e := range s.Episodes {
			if e.Number == episode {
				out.EpisodeID = e.ID
				out.EpisodeName = e.Name
				return out
			}
		}
	}
	return out
}

// AnimeNamingPath is shared by release previews and file organization. A preview
// has no extension yet; the actual file reserves its extension's byte length.
func AnimeNamingPath(target AnimeNamingTarget, season, episode int, group, ext string) string {
	title := AnimeFilenamePart(target.Title)
	folder := AnimeNamingFolder(target)
	suffix := fmt.Sprintf(".S%02dE%02d.%s", season, episode, truncateAnimeFilename(AnimeFilenamePart(group), 80))
	stem := truncateAnimeFilename(title, 200-len(suffix)-len(ext)) + suffix
	return path.Join(folder, fmt.Sprintf("Season %02d", season), stem+ext)
}

func AnimeNamingFolder(target AnimeNamingTarget) string {
	return fmt.Sprintf("%s (%s) {tmdb-%s}", truncateAnimeFilename(AnimeFilenamePart(target.Title), 180), target.Year, target.TMDBID)
}

func truncateAnimeFilename(value string, max int) string {
	for len(value) > max && len(value) > 0 {
		_, n := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-n]
	}
	return strings.TrimRight(value, ". ")
}
func AnimeFilenamePart(v string) string {
	v = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, v)
	v = strings.Trim(strings.TrimSpace(v), ". ")
	if v == "" {
		return "未命名"
	}
	r := []rune(v)
	if len(r) > 120 {
		v = string(r[:120])
	}
	return v
}
