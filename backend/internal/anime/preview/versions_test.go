package preview

import (
	"reflect"
	"testing"
)

func TestReleaseVersionRankingConservative(t *testing.T) {
	for _, example := range []struct {
		label   string
		version int
		known   bool
	}{
		{"", 1, true}, {"V2", 2, true}, {"v003", 3, true}, {"rev3 / 重发", 3, true},
		{"v2 / rev2", 2, true}, {"修正版", 0, false}, {"v2 / v3", 0, false}, {"v0", 0, false},
	} {
		n, known := ReleaseVersion(example.label)
		if n != example.version || known != example.known {
			t.Fatalf("%q: got %d %v; want %d %v", example.label, n, known, example.version, example.known)
		}
	}
}

func TestPreviewVersionRankingOrderRulesSpecificationsAndSnapshot(t *testing.T) {
	c := config()
	c.Sources[0].Rules.Match = nil
	c.Sources[0].Rules.Exclude = []string{"bad"}
	s := build(t, c, page("[组] 动画 [30v2][1080p][HEVC]", "[组] 动画 [30][1080p][HEVC]", "[组] 动画 [30V3][1080p][HEVC]", "[组] 动画 [30v9][1080p][HEVC] bad", "[组] 动画 [30][720p][HEVC]", "[组] 动画 [31][1080p][HEVC] 修正版", "[组] 动画 [30v3][1080p][HEVC]"))
	if len(s.SelectedIDs) != 2 || !s.Items[2].Selectable || !s.Items[4].Selectable || s.Items[3].ReasonCode != "EXCLUDED_PATTERN" || s.Items[5].ReasonCode != "RELEASE_VERSION_UNCLEAR" || s.Items[6].ReasonCode != "RELEASE_VERSION_DUPLICATE" {
		t.Fatalf("wrong winners or rule precedence: %+v", s.Items)
	}
	first := s.Releases(s.GeneratedAt)
	if first.Items[0].Selectable || first.Items[1].Selectable {
		t.Fatal("snapshot promoted a lower version")
	}
	reversed := append([]Item{}, s.Items...)
	// Re-evaluate only metadata-eligible copies before reversing the feed order.
	for i := range reversed {
		if reversed[i].ReasonCode == "RELEASE_VERSION_SUPERSEDED" || reversed[i].ReasonCode == "RELEASE_VERSION_DUPLICATE" {
			reversed[i].Selectable, reversed[i].Decision = true, "matched"
		}
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	PreferCurrentVersions(reversed)
	for _, row := range reversed {
		for _, original := range s.Items {
			if row.Release.PostKey == original.Release.PostKey && row.Selectable != original.Selectable {
				t.Fatal("feed order changed a winner")
			}
		}
	}
	if err := s.Select([]string{s.Items[0].Release.ID, s.Items[2].Release.ID}, nil, s.GeneratedAt); err == nil {
		t.Fatal("lower version selectable through direct API")
	}
	before := append([]string{}, s.SelectedIDs...)
	nextPage := s.Page
	nextPage.Releases = append(nextPage.Releases, nextPage.Releases[0])
	nextPage.Releases[len(nextPage.Releases)-1].PostKey = "0000000000000000000000000000000000000008"
	nextPage.Releases[len(nextPage.Releases)-1].Title = "[组] 动画 [30v4][1080p][HEVC]"
	nextPage.RawCount++
	next := build(t, c, nextPage)
	next.Reconcile(s)
	if len(next.Pending) != 1 || len(next.SelectedIDs) != 1 || reflect.DeepEqual(before, next.SelectedIDs) {
		t.Fatal("new highest must require explicit review, never silently replace", next.SelectedIDs, next.Pending)
	}
}

func TestPreviewAmbiguousSpecificationCannotBypassVersionPolicy(t *testing.T) {
	c := config()
	c.Sources[0].Rules.Match = nil
	s := build(t, c, page("[组] 动画 [30v2][1080p][HEVC][AVC]"))
	if s.Items[0].Selectable || s.Items[0].ReasonCode != "RELEASE_EDITION_UNCLEAR" || len(s.SelectedIDs) != 0 {
		t.Fatal("ambiguous codec bypassed edition grouping", s.Items)
	}
}
