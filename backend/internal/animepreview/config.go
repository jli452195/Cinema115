// Package animepreview implements non-executing, immutable release previews.
package animepreview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/model"
)

const RuleVersion int64 = 1
const LifetimeMillis int64 = 15 * 60 * 1000

var (
	ErrInput    = errors.New("invalid anime preview input")
	ErrExpired  = errors.New("anime preview expired")
	ErrConflict = errors.New("anime preview revision conflict")
	ErrReview   = errors.New("anime preview selections require review")
)

// Chapter identifies a local chapter or an unsaved form's confirmed edition.
// DraftKey separates independent unsaved forms, including Mikan-only entries.
type Chapter struct {
	ID         string  `json:"id"`
	BangumiID  *string `json:"bangumiId"`
	EditionKey string  `json:"editionKey"`
	Revision   int64   `json:"revision"`
	DraftKey   string  `json:"draftKey"`
}

type Config struct {
	Chapter        Chapter                   `json:"chapter"`
	SubscriptionID string                    `json:"subscriptionId"`
	Revision       int64                     `json:"revision"`
	Settings       model.AnimeSettings       `json:"settings"`
	Sources        []model.AnimeSourceConfig `json:"sources"`
}

func Digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return "v1:" + hex.EncodeToString(h[:]), nil
}

// FoldASCII deliberately leaves non-ASCII characters unchanged.
func FoldASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func NormalizeRules(r model.AnimeRules) (model.AnimeRules, error) {
	normalize := func(words []string) []string {
		seen := make(map[string]bool, len(words))
		out := make([]string, 0, len(words))
		for _, w := range words {
			w = FoldASCII(strings.TrimSpace(w))
			if w != "" && !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
		sort.Strings(out)
		return out
	}
	r.Include, r.Exclude = normalize(r.Include), normalize(r.Exclude)
	if err := r.Validate(false); err != nil {
		return r, ErrInput
	}
	return r, nil
}

func Normalize(c Config) (Config, error) {
	// Own the input: normalization must never mutate a caller's form.
	b, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	var owned Config
	if err := json.Unmarshal(b, &owned); err != nil {
		return c, err
	}
	c = owned
	if len(c.Sources) < 1 || len(c.Sources) > model.AnimeMaxSources ||
		c.Revision < 0 || c.Revision > model.AnimeMaxRevision || (c.SubscriptionID == "") != (c.Revision == 0) ||
		c.SubscriptionID != "" && !model.ValidAnimeID(c.SubscriptionID) || c.Settings.Revision < 1 || c.Settings.Revision > model.AnimeMaxRevision {
		return c, ErrInput
	}
	ch := c.Chapter
	if ch.ID != "" {
		if !model.ValidAnimeID(ch.ID) || ch.Revision < 1 || ch.Revision >= model.AnimeMaxRevision || ch.DraftKey != "" {
			return c, ErrInput
		}
	} else if !model.ValidAnimeID(ch.DraftKey) || ch.Revision != 0 {
		return c, ErrInput
	}
	if strings.TrimSpace(ch.EditionKey) == "" || !utf8.ValidString(ch.EditionKey) || utf8.RuneCountInString(ch.EditionKey) > 64 || ch.BangumiID != nil && !model.ValidAnimeExternalID(*ch.BangumiID) {
		return c, ErrInput
	}
	refs, feeds, ids := make(map[string]bool, len(c.Sources)), make(map[string]bool, len(c.Sources)), make(map[string]bool, len(c.Sources))
	main := 0
	for i := range c.Sources {
		s := &c.Sources[i]
		s.Rules, err = NormalizeRules(s.Rules)
		if err != nil || s.Validate() != nil || refs[s.Ref] || s.ID != "" && ids[s.ID] {
			return c, ErrInput
		}
		refs[s.Ref], ids[s.ID] = true, true
		if s.Role == "main" {
			main++
		}
		if s.ItemID != nil && s.GroupID != nil {
			key := *s.ItemID + ":" + *s.GroupID
			if feeds[key] {
				return c, ErrInput
			}
			feeds[key] = true
		}
	}
	if main != 1 {
		return c, ErrInput
	}
	// Position and role retain semantic order; client array order is immaterial.
	sort.Slice(c.Sources, func(i, j int) bool {
		if c.Sources[i].Position == c.Sources[j].Position {
			return c.Sources[i].Ref < c.Sources[j].Ref
		}
		return c.Sources[i].Position < c.Sources[j].Position
	})
	return c, nil
}

type engineVersions struct{ Parser, Time, Rule int64 }

var engines = engineVersions{animeparse.ParserVersion, animeparse.TimeVersion, RuleVersion}

func ConfigFingerprint(c Config) (string, error) {
	var err error
	c, err = Normalize(c)
	if err != nil {
		return "", err
	}
	for i := range c.Sources {
		c.Sources[i].GroupName = nil
	}
	return Digest(struct {
		Config  Config
		Engines engineVersions
	}{c, engines})
}

func SourceFingerprint(c Config, ref string) (string, error) {
	var err error
	c, err = Normalize(c)
	if err != nil {
		return "", err
	}
	for _, s := range c.Sources {
		if s.Ref == ref {
			return Digest(struct {
				Chapter           Chapter
				SubscriptionID    string
				Settings          model.AnimeSettings
				Ref, ID, Provider string
				Revision          int64
				ItemID, GroupID   *string
				Binding           model.AnimeBinding
				Rules             model.AnimeRules
				Engines           engineVersions
			}{c.Chapter, c.SubscriptionID, c.Settings, s.Ref, s.ID, s.Provider, s.ExpectedRevision, s.ItemID, s.GroupID, s.Binding, s.Rules, engines})
		}
	}
	return "", ErrInput
}

func SameScope(a, b Config) bool {
	return a.SubscriptionID == b.SubscriptionID && a.Chapter.ID == b.Chapter.ID &&
		a.Chapter.EditionKey == b.Chapter.EditionKey && a.Chapter.DraftKey == b.Chapter.DraftKey &&
		((a.Chapter.BangumiID == nil && b.Chapter.BangumiID == nil) ||
			(a.Chapter.BangumiID != nil && b.Chapter.BangumiID != nil && *a.Chapter.BangumiID == *b.Chapter.BangumiID))
}
