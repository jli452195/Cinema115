// Package animefeed contains provider-neutral subscription feed contracts.
package animefeed

import (
	"errors"
	"net/url"
	"regexp"
	"time"
)

var ErrInput = errors.New("invalid anime source identity")
var numericID = regexp.MustCompile(`^[1-9][0-9]{0,11}$`)
var slug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

type Source struct {
	Provider string `json:"provider,omitempty"`
	ItemID   string `json:"itemId"`
	GroupID  string `json:"groupId"`
}

// Mikan's absent provider is its canonical internal form. Provider identities
// are included in every equality/cache/budget key, including numeric slugs.
func NewSource(provider, item, group string) Source {
	if provider == "mikan" {
		provider = ""
	}
	return Source{Provider: provider, ItemID: item, GroupID: group}
}
func (s Source) Site() string {
	if s.Provider == "" {
		return "mikan"
	}
	return s.Provider
}
func ValidProvider(p string) bool { return p == "mikan" || p == "anibt" }
func ValidItem(id string) bool    { return numericID.MatchString(id) }
func ValidGroup(provider, id string) bool {
	if provider == "anibt" {
		return slug.MatchString(id)
	}
	return provider == "mikan" && ValidItem(id)
}
func (s Source) Valid() bool { return ValidItem(s.ItemID) && ValidGroup(s.Site(), s.GroupID) }
func (s Source) URL() string {
	if !ValidItem(s.ItemID) {
		return ""
	}
	if s.Site() == "anibt" {
		return "https://anibt.net/anime/" + s.ItemID
	}
	if s.Site() == "mikan" {
		return "https://mikanani.me/Home/Bangumi/" + s.ItemID
	}
	return ""
}
func (s Source) RSSURL() (string, error) {
	if !s.Valid() {
		return "", ErrInput
	}
	if s.Site() == "anibt" {
		return "https://anibt.net/rss/anime.xml?" + url.Values{"bgmId": {s.ItemID}, "groupSlug": {s.GroupID}}.Encode(), nil
	}
	return "https://mikanani.me/RSS/Bangumi?" + url.Values{"bangumiId": {s.ItemID}, "subgroupid": {s.GroupID}}.Encode(), nil
}

type Group struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	TotalReleases *int   `json:"totalReleases,omitempty"`
}
type SourceEntry struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Groups     []Group  `json:"groups"`
	BangumiIDs []string `json:"bangumiIds"`
}
type SourceRelease struct {
	PostKey               string `json:"postKey"`
	Title                 string `json:"title"`
	OriginalGUID          string `json:"originalGuid"`
	URL                   string `json:"url"`
	TorrentURL            string `json:"torrentUrl"`
	MagnetHint            string `json:"magnetHint"`
	SizeRaw               string `json:"sizeRaw"`
	SizeBytes             int64  `json:"sizeBytes"`
	PublishedRaw          string `json:"publishedRaw"`
	AlternatePublishedRaw string `json:"alternatePublishedRaw"`
}
type SourcePage struct {
	Kind          string          `json:"kind"`
	Source        Source          `json:"source"`
	Entry         *SourceEntry    `json:"entry,omitempty"`
	Releases      []SourceRelease `json:"releases"`
	RawCount      int             `json:"rawCount"`
	RequestedTake int             `json:"requestedTake"`
	NextTake      int             `json:"nextTake"`
	Completeness  string          `json:"completeness"`
	StopReason    string          `json:"stopReason"`
	ObservedAt    time.Time       `json:"observedAt"`
	ReadBytes     int64           `json:"-"`
	Total         *int            `json:"sourceTotal,omitempty"`
}
type SourceRequest struct {
	Kind      string
	Source    Source
	Take      int
	MaxRows   int
	BodyLimit int64
}
