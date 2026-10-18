package service

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/client/mikan"
)

type AnimeMikanEntryResult struct {
	Entry      mikan.SourceEntry `json:"entry"`
	ObservedAt time.Time         `json:"observedAt"`
	ExpiresAt  time.Time         `json:"expiresAt"`
	Cached     bool              `json:"cached"`
}

// ReadAnimeMikanEntry reads one actual item/group list without fetching any
// publisher history. It shares the existing anime budget, cache and site gate.
func (s *MikanService) ReadAnimeMikanEntry(ctx context.Context, budget *AnimeMikanBudget, itemID string, refresh bool) (AnimeMikanEntryResult, error) {
	var out AnimeMikanEntryResult
	if budget == nil || !mikan.ValidID(itemID) {
		return out, mikan.ErrInput
	}
	reader, ok := s.Client.(animeMikanReader)
	if !ok {
		return out, mikan.ErrInput
	}
	ctx, cancel := context.WithDeadline(ctx, budget.deadline)
	defer cancel()
	if err := budget.acquire(ctx); err != nil {
		return out, err
	}
	defer func() { <-budget.gate }()
	entry, cached, err := s.animeMikanRead(ctx, budget, reader, mikan.SourceRequest{Kind: "entry", Source: mikan.Source{ItemID: itemID}, MaxRows: mikan.AnimeMaxRows}, refresh)
	if err != nil {
		return out, err
	}
	if !validAnimeMikanEntry(entry.page, itemID) {
		return out, mikan.ErrParse
	}
	copy := cloneAnimeMikanPage(entry.page)
	out.Entry, out.ObservedAt, out.ExpiresAt, out.Cached = *copy.Entry, copy.ObservedAt, entry.expires, cached
	return out, nil
}

func validAnimeSourceText(text string, max int) bool {
	return len(text) <= max*utf8.UTFMax && utf8.ValidString(text) && utf8.RuneCountInString(text) <= max && strings.TrimSpace(text) != "" && !strings.ContainsFunc(text, unicode.IsControl)
}

func validAnimeMikanEntry(page mikan.SourcePage, itemID string) bool {
	if page.Entry == nil || page.Entry.ID != itemID || !validAnimeSourceText(page.Entry.Title, 1024) || page.ObservedAt.IsZero() || len(page.Entry.Groups) > mikan.AnimeMaxRows {
		return false
	}
	seen := make(map[string]bool, len(page.Entry.Groups))
	for _, group := range page.Entry.Groups {
		if !mikan.ValidID(group.ID) || !validAnimeSourceText(group.Name, 256) || seen[group.ID] {
			return false
		}
		seen[group.ID] = true
	}
	return true
}
