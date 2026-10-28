package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestAlternativeNamesAndClassificationFacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("append_to_response") == "alternative_titles,translations" {
			w.Write([]byte(`{"id":42,"alternative_titles":{"titles":[{"title":"别名一"}],"results":[{"title":"别名二"}]},"translations":{"translations":[{"data":{"name":"Translated name","title":"Translated title"}}]}}`))
			return
		}
		w.Write([]byte(`{"id":42,"name":"作品","original_language":"ja","origin_country":["JP"],"genres":[{"id":16,"name":"Animation"}]}`))
	}))
	defer server.Close()
	c := New("key")
	c.base = server.URL
	names, err := c.AlternativeNames(context.Background(), "tv", 42)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"别名一", "别名二", "Translated name", "Translated title"} {
		if !slices.Contains(names, want) {
			t.Fatal("missing alias", want, names)
		}
	}
	detail, err := c.Details(context.Background(), "tv", 42)
	if err != nil || detail.OriginalLanguage != "ja" || !slices.Equal(detail.OriginCountryCodes, []string{"JP"}) {
		t.Fatal(detail, err)
	}
	if _, err = c.AlternativeNames(context.Background(), "person", 42); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	if _, err = c.AlternativeNames(context.Background(), "tv", 43); !errors.Is(err, ErrInput) {
		t.Fatal("mismatched identity accepted", err)
	}
}
