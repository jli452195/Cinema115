package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnglishTitleTranslations(t *testing.T) {
	for _, tc := range []struct {
		name, kind, body, want string
		invalid                bool
	}{
		{"US preferred", "tv", `{"id":42,"translations":[{"iso_639_1":"ja","data":{"name":"原名"}},{"iso_639_1":"en","iso_3166_1":"GB","data":{"name":"British"}},{"iso_639_1":"en","iso_3166_1":"US","data":{"name":" Rising Impact "}}]}`, "Rising Impact", false},
		{"GB fallback", "movie", `{"id":42,"translations":[{"iso_639_1":"en","iso_3166_1":"AU","data":{"title":"Australian"}},{"iso_639_1":"en","iso_3166_1":"GB","data":{"title":"British"}},{"iso_639_1":"en","iso_3166_1":"US","data":{"title":" "}}]}`, "British", false},
		{"no English", "tv", `{"id":42,"translations":[{"iso_639_1":"ja","data":{"name":"原名"}}]}`, "", false},
		{"empty translations", "movie", `{"id":42,"translations":[]}`, "", false},
		{"English original", "tv", `{"id":42,"original_language":"en","original_name":"Breaking Bad","translations":[]}`, "Breaking Bad", false},
		{"wrong identity", "tv", `{"id":43,"translations":[]}`, "", true},
		{"malformed translations", "tv", `{"id":42}`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.body), &body); err != nil {
				t.Fatal(err)
			}
			translations := body["translations"]
			if translations == nil {
				translations = json.RawMessage(`null`)
			}
			body["translations"], _ = json.Marshal(map[string]json.RawMessage{"translations": translations})
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/"+tc.kind+"/42" || r.URL.Query().Get("language") != "en-US" || r.URL.Query().Get("append_to_response") != "translations" {
					t.Errorf("unexpected endpoint %s", r.URL.Path)
				}
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			client := New("test-key")
			client.base = server.URL
			for i := 0; i < 2; i++ {
				got, err := client.EnglishTitle(context.Background(), tc.kind, 42)
				if (err != nil) != tc.invalid || got != tc.want {
					t.Fatalf("got %q, %v; want %q", got, err, tc.want)
				}
			}
			if calls != 1 {
				t.Fatalf("cache missed: %d requests", calls)
			}
		})
	}
	if _, err := New("test-key").EnglishTitle(context.Background(), "other", 42); !errors.Is(err, ErrInput) {
		t.Fatal("invalid kind accepted", err)
	}
}
