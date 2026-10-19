package garden

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"strm115/backend/internal/client/animefeed"
)

const GroupScanLimit = 200
const HistoryLimit = 1000

var originNames = map[string]string{"dmhy": "动漫花园", "mikan": "蜜柑计划", "moe": "萌番组", "ani": "ANi"}
var originID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var btih = regexp.MustCompile(`(?i)^urn:btih:([0-9a-f]{40}|[a-z2-7]{32})$`)

type Person struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type Resource struct {
	ID         int64   `json:"id"`
	Provider   string  `json:"provider"`
	ProviderID string  `json:"providerId"`
	Title      string  `json:"title"`
	Href       string  `json:"href"`
	Size       int64   `json:"size"`
	Magnet     string  `json:"magnet"`
	SubjectID  int64   `json:"subjectId"`
	CreatedAt  string  `json:"createdAt"`
	Publisher  Person  `json:"publisher"`
	Fansub     *Person `json:"fansub"`
}
type Resources struct {
	Status     string     `json:"status"`
	Rows       []Resource `json:"resources"`
	Pagination struct {
		Page     int   `json:"page"`
		Size     int   `json:"pageSize"`
		Complete *bool `json:"complete"`
	} `json:"pagination"`
	Filter struct {
		Subjects   []int64  `json:"subjects"`
		Provider   string   `json:"provider"`
		Fansubs    []string `json:"fansubs"`
		Publishers []string `json:"publishers"`
	} `json:"filter"`
}

func validPerson(p Person) bool {
	return p.ID > 0 && p.ID <= 999999999999 && strings.TrimSpace(p.Name) != "" && len([]rune(p.Name)) <= 256 && !strings.ContainsFunc(p.Name, unicode.IsControl)
}

func ResourcesPath(id string, take int, group *animefeed.Group) (string, error) {
	if !animefeed.ValidItem(id) || take < 1 || take > HistoryLimit {
		return "", animefeed.ErrInput
	}
	q := url.Values{"subject": {id}, "page": {"1"}, "pageSize": {strconv.Itoa(take)}}
	if group != nil {
		source := animefeed.NewSource("garden", id, group.ID)
		feed, err := source.RSSURL(group.Name)
		if err != nil {
			return "", err
		}
		u, _ := url.Parse(feed)
		for key, values := range u.Query() {
			q[key] = values
		}
		// Garden's documented query semantics require nonempty duplicate=true.
		// Each original post remains independent; no episode/version dedup here.
		q.Set("duplicate", "true")
	}
	return "/resources?" + q.Encode(), nil
}
func groupFor(r Resource) animefeed.Group {
	p, kind, prefix := r.Publisher, "publisher", "p"
	if r.Fansub != nil {
		p, kind, prefix = *r.Fansub, "fansub", "f"
	}
	return animefeed.Group{ID: r.Provider + "_" + prefix + strconv.FormatInt(p.ID, 10), Name: p.Name, Origin: r.Provider, OriginName: originNames[r.Provider], Kind: kind}
}
func ParseResources(body []byte, id string, take int, group *animefeed.Group) (Resources, error) {
	var v Resources
	if json.Unmarshal(body, &v) != nil || v.Status != "OK" || v.Rows == nil || len(v.Rows) > take || v.Pagination.Page != 1 || v.Pagination.Size != take || v.Pagination.Complete == nil || len(v.Filter.Subjects) != 1 || strconv.FormatInt(v.Filter.Subjects[0], 10) != id {
		return v, ErrInvalid
	}
	if group != nil && (v.Filter.Provider != group.Origin || group.Kind == "fansub" && (len(v.Filter.Fansubs) != 1 || v.Filter.Fansubs[0] != group.Name) || group.Kind == "publisher" && (len(v.Filter.Publishers) != 1 || v.Filter.Publishers[0] != group.Name)) {
		return v, ErrInvalid
	}
	seen := make(map[string]bool, len(v.Rows))
	for _, r := range v.Rows {
		key := r.Provider + ":" + r.ProviderID
		if r.ID < 1 || originNames[r.Provider] == "" || !originID.MatchString(r.ProviderID) || r.SubjectID != v.Filter.Subjects[0] || strings.TrimSpace(r.Title) == "" || len([]rune(r.Title)) > 1024 || r.Size < 0 || seen[key] || !validPerson(r.Publisher) || r.Fansub != nil && !validPerson(*r.Fansub) {
			return v, ErrInvalid
		}
		if _, err := time.Parse(time.RFC3339Nano, r.CreatedAt); err != nil {
			return v, ErrInvalid
		}
		if group != nil {
			person, prefix := r.Publisher, "p"
			if group.Kind == "fansub" {
				if r.Fansub == nil {
					return v, ErrInvalid
				}
				person, prefix = *r.Fansub, "f"
			}
			// A publisher may post both attributed and unattributed releases.
			// Its explicit filter remains valid when a row also has a fansub.
			if r.Provider+"_"+prefix+strconv.FormatInt(person.ID, 10) != group.ID {
				return v, ErrInvalid
			}
		}
		u, err := url.Parse(r.Href)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || !validOriginHost(r.Provider, u.Host) {
			return v, ErrInvalid
		}
		m, err := url.Parse(r.Magnet)
		if err != nil || m.Scheme != "magnet" || !btih.MatchString(m.Query().Get("xt")) || len(m.Query()["xt"]) != 1 || len(r.Magnet) > 16384 {
			return v, ErrInvalid
		}
		seen[key] = true
	}
	return v, nil
}
func validOriginHost(p, host string) bool {
	switch p {
	case "dmhy":
		return host == "share.dmhy.org"
	case "mikan":
		return host == "mikanani.me" || host == "mikanime.tv"
	case "moe":
		return host == "bangumi.moe"
	case "ani":
		return host == "tds.ani.rip"
	}
	return false
}
func Entry(subject Item, resources Resources) animefeed.SourceEntry {
	out := animefeed.SourceEntry{ID: subject.ID, Title: subject.Title, BangumiIDs: []string{subject.BangumiID}, Groups: []animefeed.Group{}, GroupsPartial: !*resources.Pagination.Complete}
	seen := make(map[string]bool, len(resources.Rows))
	for _, r := range resources.Rows {
		g := groupFor(r)
		if !seen[g.ID] {
			seen[g.ID] = true
			out.Groups = append(out.Groups, g)
		}
	}
	sort.SliceStable(out.Groups, func(i, j int) bool {
		a, b := out.Groups[i], out.Groups[j]
		if a.Name == b.Name {
			return a.Origin < b.Origin
		}
		return a.Name < b.Name
	})
	return out
}
func SourcePage(source animefeed.Source, v Resources, take int, observed time.Time) animefeed.SourcePage {
	rows := make([]animefeed.SourceRelease, 0, len(v.Rows))
	for _, r := range v.Rows {
		canonical := "https://animes.garden/detail/" + r.Provider + "/" + r.ProviderID
		// Stable original-site post identity, independent of Garden re-import IDs.
		key := r.ProviderID
		if len(key) > 40 {
			digest := sha256.Sum256([]byte(r.ProviderID))
			key = hex.EncodeToString(digest[:20])
		}
		fansub := ""
		if r.Fansub != nil {
			fansub = r.Fansub.Name
		}
		m, _ := url.Parse(r.Magnet)
		magnet := "magnet:?" + url.Values{"xt": {m.Query().Get("xt")}}.Encode()
		rows = append(rows, animefeed.SourceRelease{PostKey: "rel_" + r.Provider + "_" + key, OriginalGUID: canonical, Title: r.Title, URL: r.Href, MagnetHint: magnet, SizeBytes: r.Size, SizeRaw: strconv.FormatInt(r.Size, 10) + " B", PublishedRaw: r.CreatedAt, Origin: r.Provider, OriginID: r.ProviderID, Publisher: r.Publisher.Name, Fansub: fansub})
	}
	p := animefeed.SourcePage{Kind: "history", Source: source, Releases: rows, RawCount: len(v.Rows), RequestedTake: take, ObservedAt: observed, Completeness: "partial", StopReason: "more_available"}
	if *v.Pagination.Complete {
		p.Completeness, p.StopReason = "visible_end", ""
		total := len(v.Rows)
		p.Total = &total
	} else if take < HistoryLimit {
		p.NextTake = min(HistoryLimit, take+100)
	} else {
		p.StopReason = "source_limit"
	}
	return p
}
