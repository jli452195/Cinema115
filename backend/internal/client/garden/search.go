package garden

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"strm115/backend/internal/client/animefeed"
)

const SearchPageSize = 100

type SearchOptions struct {
	Query     string `json:"query"`
	GroupID   string `json:"group"`
	GroupName string `json:"groupName"`
	Page      int    `json:"page"`
}

func (in SearchOptions) group() (*animefeed.Group, error) {
	if in.GroupID == "" {
		if in.GroupName != "" {
			return nil, animefeed.ErrInput
		}
		return nil, nil
	}
	if !animefeed.ValidGroup("garden", in.GroupID) || strings.TrimSpace(in.GroupName) == "" || len([]rune(in.GroupName)) > 256 || strings.ContainsFunc(in.GroupName, unicode.IsControl) {
		return nil, animefeed.ErrInput
	}
	parts := strings.SplitN(in.GroupID, "_", 2)
	kind := "fansub"
	if parts[1][0] == 'p' {
		kind = "publisher"
	}
	return &animefeed.Group{ID: in.GroupID, Name: in.GroupName, Origin: parts[0], Kind: kind}, nil
}

func SearchPath(in SearchOptions) (string, error) {
	if !ValidQuery(in.Query) || in.Page < 1 || in.Page > 100 {
		return "", animefeed.ErrInput
	}
	g, err := in.group()
	if err != nil {
		return "", err
	}
	q := url.Values{"search": {in.Query}, "page": {strconv.Itoa(in.Page)}, "pageSize": {strconv.Itoa(SearchPageSize)}}
	if g != nil {
		q.Set(g.Kind, g.Name)
		q.Set("provider", g.Origin)
		q.Set("duplicate", "true")
	}
	return "/resources?" + q.Encode(), nil
}

// Keyword results may have no subjectId. No metadata or per-resource reads are needed.
func ParseSearchResources(body []byte, in SearchOptions) (Resources, error) {
	if _, err := SearchPath(in); err != nil {
		return Resources{}, err
	}
	v, err := parseResources(body, in.Page, SearchPageSize)
	if err != nil {
		return v, err
	}
	g, err := in.group()
	if err != nil {
		return v, err
	}
	provider := ""
	if g != nil {
		provider = g.Origin
	}
	// Garden simplifies traditional characters and replaces punctuation in its
	// echoed search filter. It is not an exact copy of the submitted keyword.
	if len(v.Filter.Search) != 1 || !ValidQuery(v.Filter.Search[0]) || len(v.Filter.Subjects) != 0 || v.Filter.Provider != provider {
		return v, ErrInvalid
	}
	if err := validateResourceGroup(v, g); err != nil {
		return v, err
	}
	if g == nil && (len(v.Filter.Fansubs) != 0 || len(v.Filter.Publishers) != 0) {
		return v, ErrInvalid
	}
	for _, r := range v.Rows {
		if provider != "" && r.Provider != provider {
			return v, ErrInvalid
		}
	}
	return v, nil
}

func ResourceGroup(r Resource) animefeed.Group { return groupFor(r) }
