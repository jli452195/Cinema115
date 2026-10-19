package bangumi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

const testEpisode = `{"id":1388677,"subject_id":510728,"type":0,"sort":59,"ep":1,"name":"混戦都市","name_cn":"混战都市","airdate":"2025-02-05","desc":"not retained"}`

func TestEpisodesKeepsBothNumbersAndCachesBoundedPages(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/v0/episodes" || q.Get("subject_id") != "510728" || q.Get("type") != "0" || q.Get("limit") != "100" {
			t.Error("unexpected episode request", r.URL)
		}
		writeJSON(w, `{"total":1,"limit":100,"offset":0,"data":[`+testEpisode+`]}`)
	})
	ctx := context.Background()
	first, err := c.Episodes(ctx, "510728", 0)
	if err != nil || len(first.Items) != 1 || first.Total != 1 || first.MaxItems != MaxEpisodeItems || first.Items[0].Number != 59 || first.Items[0].ChapterNumber == nil || *first.Items[0].ChapterNumber != 1 || first.Items[0].Title != "混战都市" || first.Items[0].Date != "2025-02-05" {
		t.Fatal(first, err)
	}
	first.Items[0].Title = "caller edit"
	*first.Items[0].ChapterNumber = 99
	second, err := c.Episodes(ctx, "510728", 0)
	if err != nil || second.Items[0].Title != "混战都市" || *second.Items[0].ChapterNumber != 1 || calls.Load() != 1 {
		t.Fatal("cache was mutated or fetched again", second, err, calls.Load())
	}
	for _, offset := range []int{-1, 1, 99, 2000} {
		if _, err := c.Episodes(ctx, "510728", offset); !errors.Is(err, ErrInput) {
			t.Fatal(offset, err)
		}
	}
	if _, err := c.Episodes(ctx, "bad", 0); !errors.Is(err, ErrInput) || calls.Load() != 1 {
		t.Fatal("invalid input reached upstream", err, calls.Load())
	}
}

func TestEpisodeNormalizationRejectsWrongSubjectAndIncompletePages(t *testing.T) {
	valid := `{"total":1,"limit":100,"offset":0,"data":[` + testEpisode + `]}`
	for name, body := range map[string]string{
		"null": "null", "missing data": `{"total":0,"limit":100,"offset":0}`,
		"null data":      `{"total":0,"limit":100,"offset":0,"data":null}`,
		"missing number": strings.Replace(valid, `"sort":59`, `"sort":null`, 1),
		"wrong subject":  strings.Replace(valid, `"subject_id":510728`, `"subject_id":999`, 1),
		"special":        strings.Replace(valid, `"type":0`, `"type":1`, 1),
		"invalid number": strings.Replace(valid, `"sort":59`, `"sort":-1`, 1),
		"wrong offset":   strings.Replace(valid, `"offset":0`, `"offset":100`, 1),
		"short page":     strings.Replace(valid, `"total":1`, `"total":2`, 1),
		"duplicate id":   `{"total":2,"limit":100,"offset":0,"data":[` + testEpisode + `,` + testEpisode + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeEpisodes([]byte(body), "510728", 0); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid episode page accepted", err)
			}
		})
	}
	unknownDate := strings.Replace(valid, "2025-02-05", "0000-00-00", 1)
	if body, err := normalizeEpisodes([]byte(unknownDate), "510728", 0); err != nil || !strings.Contains(string(body), `"date":""`) || strings.Contains(string(body), "not retained") {
		t.Fatal(string(body), err)
	}
	if _, err := normalizeEpisodes([]byte(`{"total":0,"limit":100,"offset":0,"data":[]}`), "510728", 0); err != nil {
		t.Fatal(err)
	}
}

func TestEpisodePaginationDoesNotFetchFurtherPages(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		offset := r.URL.Query().Get("offset")
		if offset == "100" {
			writeJSON(w, `{"total":101,"limit":100,"offset":100,"data":[`+testEpisode+`]}`)
			return
		}
		items := make([]string, 100)
		for i := range items {
			items[i] = strings.Replace(testEpisode, `"id":1388677`, fmt.Sprintf(`"id":%d`, i+1), 1)
		}
		writeJSON(w, `{"total":101,"limit":100,"offset":0,"data":[`+strings.Join(items, ",")+`]}`)
	})
	if first, err := c.Episodes(context.Background(), "510728", 0); err != nil || len(first.Items) != 100 || calls.Load() != 1 {
		t.Fatal(first, err, calls.Load())
	}
	if next, err := c.Episodes(context.Background(), "510728", 100); err != nil || len(next.Items) != 1 || calls.Load() != 2 {
		t.Fatal(next, err, calls.Load())
	}
}
