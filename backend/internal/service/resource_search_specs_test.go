package service

import (
	"context"
	"testing"

	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/mikan"
)

func TestResourceSearchSpecsUseExplicitMetadata(t *testing.T) {
	for _, tc := range []struct{ title, rate string }{
		{"[字幕组] Show [01][1080P][HEVC 10bit][简繁内封][5 Mbps]", "5 Mbps"},
		{"[Show][720p][H.264][GB][5000kbps]", "5 Mbps"},
		{"[Show][5Mb/s][5000kbps]", "5 Mbps"},
		{"[Show][5Mbps 10Mbps]", ""},
		{"[Show][5MB/s][1080p] 400MB", ""},
		{"[Show][1080p][10bit]", ""},
		{"[Show][5MbpsFake]", ""},
	} {
		spec := resourceSearchSpec(tc.title)
		if spec.Bitrate != tc.rate {
			t.Fatal("inferred an unmarked or ambiguous bitrate", tc.title, spec)
		}
	}
	spec := resourceSearchSpec("[星光字幕组] Show [01][1080P][HEVC 10bit][简繁内封]")
	if spec.Resolution != "1080p" || spec.Codec != "h265" || spec.BitDepth != "10" || spec.SubtitleMode != "soft" || len(spec.Languages) != 2 {
		t.Fatal("search diverged from shared metadata parser", spec)
	}
}

func TestResourceSearchSpecsDoNotAddUpstreamRequests(t *testing.T) {
	calls := 0
	s := NewMikanService(mikanReaderFunc(func(context.Context, string, string) (mikan.Result, error) {
		calls++
		return mikan.Result{Items: []mikan.Item{{ID: "one", Title: "[Show][01][HEVC 10bit][CHT][5Mbps]"}}, Groups: []mikan.Group{}}, nil
	}), nil)
	for i := 0; i < 2; i++ {
		result, err := s.Search(context.Background(), MikanInput{Query: "Show"})
		if err != nil || result.Specs["one"].Codec != "h265" || result.Specs["one"].Bitrate != "5 Mbps" || len(result.Specs["one"].Languages) != 1 || calls != 1 {
			t.Fatal("search hints triggered new reads or lost cached metadata", err, result.Specs, calls)
		}
		result.Specs["one"] = ResourceSearchSpec{}
	}
	g := NewGardenService(&gardenSearchFake{})
	g.spacing = 0
	result, err := g.SearchResources(context.Background(), GardenSearchInput{})
	if err == nil {
		t.Fatal("empty search unexpectedly succeeded", result)
	}
	result, err = g.SearchResources(context.Background(), GardenSearchInput{SearchOptions: garden.SearchOptions{Query: "Show"}})
	if err != nil || result.Specs["dmhy:123"].Resolution != "1080p" {
		t.Fatal("Garden search did not return local hints", err, result.Specs)
	}
}
