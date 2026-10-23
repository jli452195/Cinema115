package garden

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestResourceSearchWithoutSubject(t *testing.T) {
	var v Resources
	if err := json.Unmarshal(fixture(t, "resources.json"), &v); err != nil {
		t.Fatal(err)
	}
	v.Pagination.Page, v.Pagination.Size = 2, SearchPageSize
	v.Filter.Subjects, v.Filter.Search = nil, []string{"百变之星"}
	for i := range v.Rows {
		v.Rows[i].SubjectID = 0
	}
	in := SearchOptions{Query: "百变之星", Page: 2}
	body, _ := json.Marshal(v)
	if _, err := ParseSearchResources(body, in); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseResources(body, "623249", SearchPageSize, nil); err == nil {
		t.Fatal("keyword response accepted as a subscription feed")
	}
	for _, mutate := range []func(*Resources){
		func(v *Resources) { v.Filter.Search = nil },
		func(v *Resources) { v.Filter.Search = []string{" "} },
		func(v *Resources) { v.Filter.Search = []string{"a", "b"} },
		func(v *Resources) { v.Pagination.Page = 1 },
		func(v *Resources) { v.Rows[0].Href = "https://untrusted.test/post" },
		func(v *Resources) { v.Rows[0].Magnet = "magnet:?xt=invalid" },
		func(v *Resources) { v.Rows = append(v.Rows, v.Rows[0]) },
	} {
		var copy Resources
		if err := json.Unmarshal(body, &copy); err != nil {
			t.Fatal(err)
		}
		mutate(&copy)
		changed, _ := json.Marshal(copy)
		if _, err := ParseSearchResources(changed, in); err == nil {
			t.Fatal("mismatched or unsafe search response accepted")
		}
	}
}

func TestResourceSearchNormalizedKeywords(t *testing.T) {
	for _, example := range []struct{ input, echoed string }{
		{"无职转生～到了异世界就拿出真本事～", "无职转生 到了异世界就拿出真本事 "},
		{"蒼き伝承 ウェルシュ＆シェダー", "苍き伝承 ウェルシュ シェダー"},
		{"Kaleido Star: New Wings", "kaleido star  new wings"},
	} {
		var v Resources
		if err := json.Unmarshal(fixture(t, "resources.json"), &v); err != nil {
			t.Fatal(err)
		}
		v.Pagination.Size = SearchPageSize
		v.Filter.Subjects, v.Filter.Search = nil, []string{example.echoed}
		body, _ := json.Marshal(v)
		if _, err := ParseSearchResources(body, SearchOptions{Query: example.input, Page: 1}); err != nil {
			t.Fatalf("normalized keyword %q: %v", example.input, err)
		}
	}
}

func TestResourceSearchFiltersAndBounds(t *testing.T) {
	in := SearchOptions{Query: "old & new", GroupID: "dmhy_f776", GroupName: "天月動漫發佈組", Page: 1}
	path, err := SearchPath(in)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(path)
	if u.Query().Get("search") != in.Query || u.Query().Get("fansub") != in.GroupName || u.Query().Get("provider") != "dmhy" || u.Query().Get("duplicate") != "true" || u.Query().Get("subject") != "" {
		t.Fatal(path)
	}
	for _, bad := range []SearchOptions{
		{Query: "x", Page: 101}, {Query: "x", Page: 0}, {Query: strings.Repeat("x", 129), Page: 1},
		{Query: "x", GroupID: "unknown_f1", GroupName: "test", Page: 1}, {Query: "x", GroupID: "dmhy_f776", Page: 1},
		{Query: "x", GroupID: "dmhy_p776", GroupName: "x\n", Page: 1},
	} {
		if _, err := SearchPath(bad); err == nil {
			t.Fatal("invalid search", bad)
		}
	}
}
