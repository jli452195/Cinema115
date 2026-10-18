package animebinding

import (
	"testing"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/model"
)

func bindingValue[T any](v T) *T { return &v }

func testBinding() model.AnimeBinding {
	return model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: bindingValue(26), Last: bindingValue(38)}, ChapterCorrespondence: &model.AnimeChapterCorrespondence{SourceEpisode: 26, ChapterEpisode: 1}}
}

func TestProjectConfirmedRange(t *testing.T) {
	for _, tc := range []struct {
		title, status, reason string
		source, chapter       int
	}{
		{"[组] 动画 [25]", "out_of_scope", "OUTSIDE_CHAPTER_SCOPE", 25, 0},
		{"[组] 动画 [26]", "in_scope", "", 26, 1},
		{"[组] 动画 [30]", "in_scope", "", 30, 5},
		{"[组] 动画 [38v2]", "in_scope", "", 38, 13},
		{"[组] 动画 [39]", "out_of_scope", "OUTSIDE_CHAPTER_SCOPE", 39, 0},
		{"[组] 动画 [12.5]", "needs_confirmation", "EPISODE_NUMBER_UNRESOLVED", 0, 0},
		{"[组] 动画 - 07A", "needs_confirmation", "EPISODE_NUMBER_UNRESOLVED", 0, 0},
		{"[组] 动画 [SP26]", "needs_confirmation", "EPISODE_NUMBER_UNRESOLVED", 0, 0},
		{"[组] 动画 [26-38]", "needs_confirmation", "EPISODE_NUMBER_UNRESOLVED", 0, 0},
		{"[组] 动画 [59][3rd - 09]", "needs_confirmation", "EPISODE_NUMBER_UNRESOLVED", 0, 0},
		{"[组] 动画 [2026][1080p]", "needs_confirmation", "EPISODE_NUMBER_UNRESOLVED", 0, 0},
	} {
		t.Run(tc.title, func(t *testing.T) {
			parsed, err := animeparse.Title(tc.title)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Project(testBinding(), parsed)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.status || got.ReasonCode != tc.reason || (got.SourceEpisode == nil) != (tc.source == 0) || got.SourceEpisode != nil && *got.SourceEpisode != tc.source || (got.ChapterEpisode == nil) != (tc.chapter == 0) || got.ChapterEpisode != nil && *got.ChapterEpisode != tc.chapter {
				t.Fatalf("unexpected projection: %+v", got)
			}
		})
	}
}

func TestProjectNeverInfersCorrespondence(t *testing.T) {
	parsed, err := animeparse.Title("[组] 动画 [30]")
	if err != nil {
		t.Fatal(err)
	}
	b := testBinding()
	b.Confirmed = false
	got, err := Project(b, parsed)
	if err != nil || got.Status != "needs_confirmation" || got.ChapterEpisode != nil {
		t.Fatalf("unconfirmed input projected: %+v %v", got, err)
	}
	b.Confirmed, b.ChapterCorrespondence = true, nil
	got, err = Project(b, parsed)
	if err != nil || got.Status != "in_scope" || got.ChapterEpisode != nil || *got.SourceEpisode != 30 {
		t.Fatalf("guessed an offset: %+v %v", got, err)
	}
	*got.SourceEpisode = 99
	if *parsed.Episode != 30 {
		t.Fatal("projection mutated publisher numbering")
	}
}

func TestProjectPerPublisherNumbering(t *testing.T) {
	a := testBinding()
	b := testBinding()
	b.Scope.First, b.Scope.Last = bindingValue(1), bindingValue(13)
	b.ChapterCorrespondence.SourceEpisode = 1
	for i, tc := range []struct {
		binding model.AnimeBinding
		title   string
	}{{a, "[组甲] 动画 [30]"}, {b, "[组乙] 动画 [05v2]"}, {b, "[组乙] 动画 [05]"}} {
		p, err := animeparse.Title(tc.title)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Project(tc.binding, p)
		if err != nil || got.Status != "in_scope" || got.ChapterEpisode == nil || *got.ChapterEpisode != 5 {
			t.Fatalf("publisher %d: %+v %v", i, got, err)
		}
	}
}

func TestProjectManualChapterScope(t *testing.T) {
	b := testBinding()
	b.Scope = &model.AnimeScope{Kind: "confirmed_chapter", Evidence: "已人工核对发行版本及篇章"}
	p, err := animeparse.Title("[组] 动画 [01]")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Project(b, p)
	if err != nil || got.Status != "needs_confirmation" || got.ReasonCode != "CHAPTER_NUMBER_UNRESOLVED" {
		t.Fatalf("invalid chapter number accepted: %+v %v", got, err)
	}
	b.ChapterCorrespondence = nil
	got, err = Project(b, p)
	if err != nil || got.Status != "in_scope" || got.ChapterEpisode != nil {
		t.Fatalf("optional correspondence required: %+v %v", got, err)
	}
}

func TestProjectRejectsInvalidData(t *testing.T) {
	p := model.AnimeReleaseParse{SchemaVersion: 1, Kind: "episode", Episode: bindingValue(26)}
	for _, change := range []func(*model.AnimeBinding){
		func(b *model.AnimeBinding) { b.Scope.First = nil },
		func(b *model.AnimeBinding) { b.Scope.Last = bindingValue(0) },
		func(b *model.AnimeBinding) { b.ChapterCorrespondence.SourceEpisode = 30 },
		func(b *model.AnimeBinding) { b.ChapterCorrespondence.ChapterEpisode = 99999 },
		func(b *model.AnimeBinding) { b.Scope = &model.AnimeScope{Kind: "confirmed_chapter"} },
	} {
		b := testBinding()
		change(&b)
		if _, err := Project(b, p); err == nil {
			t.Fatal("invalid binding accepted")
		}
	}
	if _, err := Project(testBinding(), model.AnimeReleaseParse{SchemaVersion: 1, Kind: "special", Episode: bindingValue(26)}); err == nil {
		t.Fatal("invalid parse accepted")
	}
	// The stored DTO currently allows this incomplete parse. Never dereference
	// it or promote it into ordinary selectable numbering.
	p.Episode = nil
	got, err := Project(testBinding(), p)
	if err != nil || got.Status != "needs_confirmation" {
		t.Fatalf("incomplete parse: %+v %v", got, err)
	}
}
