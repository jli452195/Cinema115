package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEpisodeGroupOrderingIdentityAndValidation(t *testing.T) {
	const valid = `{"id":"group1","name":"Season","type":6,"group_count":2,"episode_count":3,"groups":[{"id":"regular","order":1,"name":"正片","episodes":[{"id":12,"order":1,"show_id":80462,"season_number":2,"episode_number":7,"name":"后篇"},{"id":11,"order":0,"show_id":80462,"season_number":1,"episode_number":9,"name":"前篇"}]},{"id":"special","order":0,"name":"特别篇","episodes":[{"id":13,"order":0,"show_id":80462,"season_number":0,"episode_number":1}]}]}`
	for _, tc := range []struct {
		name, body string
		fail       bool
	}{
		{"valid", valid, false},
		{"wrong show", strings.Replace(valid, `"show_id":80462`, `"show_id":1`, 1), true},
		{"duplicate episode", strings.Replace(valid, `"id":12`, `"id":11`, 1), true},
		{"duplicate season", strings.Replace(valid, `"order":0,"name":"特别篇"`, `"order":1,"name":"特别篇"`, 1), true},
		{"gap", strings.Replace(valid, `"id":12,"order":1`, `"id":12,"order":2`, 1), true},
		{"count mismatch", strings.Replace(valid, `"episode_count":3`, `"episode_count":4`, 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/tv/80462/episode_groups" {
					_, _ = w.Write([]byte(`{"id":80462,"results":[{"id":"group1","name":"Season","type":6,"group_count":2,"episode_count":3}]}`))
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := New("key")
			client.base = server.URL
			groups, err := client.EpisodeGroups(context.Background(), 80462)
			if err != nil || len(groups) != 1 || groups[0].EpisodeCount != 3 {
				t.Fatal(groups, err)
			}
			group, err := client.EpisodeGroup(context.Background(), 80462, "group1")
			if tc.fail {
				if err == nil {
					t.Fatal("invalid group accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ep := group.Seasons[1].Episodes[0]
			if group.Seasons[0].Number != 0 || ep.Number != 1 || ep.ID != 11 || ep.SourceSeason != 1 || ep.SourceNumber != 9 {
				t.Fatalf("identity/order lost: %+v", group)
			}
			if _, err = json.Marshal(group); err != nil {
				t.Fatal(err)
			}
		})
	}
	client := New("key")
	for _, id := range []string{"", "../bad", "a?key=secret"} {
		if _, err := client.EpisodeGroup(context.Background(), 1, id); !errors.Is(err, ErrInput) {
			t.Fatal(id, err)
		}
	}
}
