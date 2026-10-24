package preview

import (
	"sort"
	"strm115/backend/internal/model"
)

type Gap = model.AnimeEpisodeRange

// EpisodeGaps works on distinct numbers and emits compact ranges, without
// allocating an element for every missing episode.
func EpisodeGaps(first, last int, episodes map[int]bool) []Gap {
	numbers := make([]int, 0, len(episodes))
	for n, present := range episodes {
		if present && n >= first && n <= last {
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)
	out := make([]Gap, 0)
	for _, n := range numbers {
		if n > first {
			out = append(out, Gap{First: first, Last: n - 1})
		}
		first = n + 1
	}
	if first <= last {
		out = append(out, Gap{First: first, Last: last})
	}
	return out
}

type Summary struct {
	SourceRef             string `json:"sourceRef"`
	PreviewID             string `json:"sourcePreviewId"`
	LoadRevision          int64  `json:"loadRevision"`
	GeneratedAt           int64  `json:"generatedAt"`
	ExpiresAt             int64  `json:"expiresAt"`
	Expired               bool   `json:"expired"`
	RawCount              int    `json:"rawCount"`
	LoadedCount           int    `json:"loadedCount"`
	MatchedCount          int    `json:"matchedCount"`
	ReviewCount           int    `json:"reviewCount"`
	SelectedCount         int    `json:"selectedCount"`
	PendingSelectionCount int    `json:"pendingSelectionCount"`
	Completeness          string `json:"completeness"`
	StopReason            string `json:"stopReason"`
	LoadedFirst           *int   `json:"loadedFirst"`
	LoadedLast            *int   `json:"loadedLast"`
	Gaps                  []Gap  `json:"gapsWithinLoadedMatches"`
	UnloadedHistory       bool   `json:"unloadedHistory"`
}

func (s Source) Summary(now int64) Summary {
	out := Summary{SourceRef: s.Ref, PreviewID: s.ID, LoadRevision: s.LoadRevision, GeneratedAt: s.GeneratedAt, ExpiresAt: s.ExpiresAt, Expired: !s.ValidAt(now),
		RawCount: s.Page.RawCount, LoadedCount: len(s.Items), SelectedCount: len(s.SelectedIDs), PendingSelectionCount: len(s.Pending),
		Completeness: s.Page.Completeness, StopReason: s.Page.StopReason, Gaps: []Gap{}, UnloadedHistory: s.Page.Completeness != "visible_end"}
	episodes := make(map[int]bool, len(s.Items))
	for _, i := range s.Items {
		if i.Selectable {
			out.MatchedCount++
			if i.Release.Parse.Episode != nil {
				episodes[*i.Release.Parse.Episode] = true
			}
		}
		if i.Decision == "needs_confirmation" {
			out.ReviewCount++
		}
	}
	numbers := make([]int, 0, len(episodes))
	for n := range episodes {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	if len(numbers) > 0 {
		first, last := numbers[0], numbers[len(numbers)-1]
		out.LoadedFirst, out.LoadedLast = &first, &last
		out.Gaps = EpisodeGaps(first, last, episodes)
	}
	return out
}

type Page struct {
	Items       []Item            `json:"items"`
	Total       int               `json:"total"`
	Summary     Summary           `json:"summary"`
	SelectedIDs []string          `json:"selectedIds"`
	Pending     []Pending         `json:"pendingSelections"`
	LocalStates map[string]string `json:"localStates,omitempty"`
}

func (s Source) Releases(now int64) Page {
	return Page{Items: append([]Item{}, s.Items...), Total: len(s.Items), Summary: s.Summary(now), SelectedIDs: append([]string{}, s.SelectedIDs...), Pending: append([]Pending{}, s.Pending...)}
}
