package animepreview

import "sort"

type Gap struct {
	First int `json:"first"`
	Last  int `json:"last"`
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
		for i := 1; i < len(numbers); i++ {
			if numbers[i] > numbers[i-1]+1 {
				out.Gaps = append(out.Gaps, Gap{numbers[i-1] + 1, numbers[i] - 1})
			}
		}
	}
	return out
}

type Page struct {
	Items       []Item    `json:"items"`
	Total       int       `json:"total"`
	Page        int       `json:"page"`
	PageSize    int       `json:"pageSize"`
	Summary     Summary   `json:"summary"`
	SelectedIDs []string  `json:"selectedIds"`
	Pending     []Pending `json:"pendingSelections"`
}

func (s Source) Releases(page, size int, now int64) (Page, error) {
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 50
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return Page{}, ErrInput
	}
	start := min((page-1)*size, len(s.Items))
	end := min(start+size, len(s.Items))
	return Page{append([]Item{}, s.Items[start:end]...), len(s.Items), page, size, s.Summary(now), append([]string{}, s.SelectedIDs...), append([]Pending{}, s.Pending...)}, nil
}
