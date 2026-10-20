package animepreview

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
)

func ptr[T any](v T) *T { return &v }

var observed = time.Date(2026, 10, 18, 12, 15, 48, 987000000, time.UTC)

func config() Config {
	return Config{Chapter: Chapter{BangumiID: ptr("123"), EditionKey: "original", DraftKey: "form-a"}, Settings: model.AnimeSettings{Revision: 1}, Sources: []model.AnimeSourceConfig{
		{Ref: "main", Role: "main", Provider: "mikan", Status: "paused", ItemID: ptr("2259"), GroupID: ptr("45"), GroupName: ptr("字幕组"),
			Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: ptr(25), Last: ptr(40)}, ChapterCorrespondence: &model.AnimeChapterCorrespondence{SourceEpisode: 25, ChapterEpisode: 1}},
			Rules:   model.AnimeRules{SchemaVersion: 1, MinimumEpisode: ptr(30), Resolution: ptr("1080p"), InitialMode: "from_episode"}},
	}}
}

func page(titles ...string) mikan.SourcePage {
	p := mikan.SourcePage{Kind: "history", Source: mikan.Source{ItemID: "2259", GroupID: "45"}, ObservedAt: observed, RequestedTake: 50, RawCount: len(titles), Completeness: "visible_end", Releases: make([]mikan.SourceRelease, 0, len(titles))}
	for i, title := range titles {
		p.Releases = append(p.Releases, mikan.SourceRelease{PostKey: fmt.Sprintf("%040x", i+1), Title: title})
	}
	return p
}

func build(t *testing.T, c Config, p mikan.SourcePage) Source {
	t.Helper()
	c, err := Normalize(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Build(c, "main", p, observed.Add(time.Minute).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	for i := range s.Items {
		s.Items[i].Release.ID = fmt.Sprintf("release-%s", s.Items[i].Release.PostKey)
	}
	s.InitializeSelections()
	return s
}

func TestPreviewNormalizeFingerprintDependencies(t *testing.T) {
	a := config()
	a.Sources[0].Rules.Include = []string{" HEVC ", "hevc", "简体", ""}
	b := config()
	b.Sources[0].Rules.Include = []string{"简体", "HEVC"}
	an, err := Normalize(a)
	if err != nil {
		t.Fatal(err)
	}
	bn, err := Normalize(b)
	if err != nil {
		t.Fatal(err)
	}
	f1, _ := ConfigFingerprint(an)
	f2, _ := ConfigFingerprint(bn)
	if f1 != f2 || !strings.HasPrefix(f1, "v1:") || len(f1) != 67 {
		t.Fatal("canonical digest", f1, f2)
	}
	if a.Sources[0].Rules.Include[0] != " HEVC " {
		t.Fatal("mutated caller")
	}
	standby := bn.Sources[0]
	standby.Ref, standby.Role, standby.Position, standby.GroupID = "standby", "standby", 1, ptr("28")
	bn.Sources = append(bn.Sources, standby)
	s1, _ := SourceFingerprint(an, "main")
	s2, _ := SourceFingerprint(bn, "main")
	if s1 != s2 {
		t.Fatal("unrelated source invalidated preview")
	}
	bn.Settings.Revision++
	s3, _ := SourceFingerprint(bn, "main")
	if s2 == s3 {
		t.Fatal("settings excluded")
	}
	for _, words := range [][]string{{strings.Repeat("词", 65)}, {"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}} {
		r := a.Sources[0].Rules
		r.Include = words
		if _, err := NormalizeRules(r); !errors.Is(err, ErrInput) {
			t.Fatal("accepted oversize", err)
		}
	}
	if FoldASCII("AÄ中") != "aÄ中" {
		t.Fatal("non-ASCII folding")
	}
}

func TestPreviewFilterOrderAndInclusiveMinimum(t *testing.T) {
	c := config()
	c.Sources[0].Rules.Include = []string{"hevc"}
	c.Sources[0].Rules.Exclude = []string{"bad"}
	s := build(t, c, page("[组] 动画 [24][720p]", "[组] 动画 [29][1080p][HEVC]", "[组] 动画 [30][1080p][HEVC]", "[组] 动画 [31][1080p][bad]", "[组] 动画 [32][1080p]", "[组] 动画 [33][1080i][HEVC]", "[组] 动画 [SP34][HEVC]", "[组] 动画 [41][1080p][HEVC]"))
	want := []string{"OUTSIDE_CHAPTER_SCOPE", "BELOW_SOURCE_MINIMUM", "MATCHED_METADATA_ONLY", "EXCLUDED_KEYWORD", "REQUIRED_KEYWORD_MISSING", "TITLE_RESOLUTION_MISMATCH", "EPISODE_NUMBER_UNRESOLVED", "OUTSIDE_CHAPTER_SCOPE"}
	for i, item := range s.Items {
		if item.ReasonCode != want[i] {
			t.Fatalf("%d: %+v", i, item)
		}
	}
	if len(s.SelectedIDs) != 1 || *s.Items[2].Projection.ChapterEpisode != 6 {
		t.Fatal("minimum rebased numbering", s)
	}
}

func TestPreviewLiteralKeywordsAndUnknownResolution(t *testing.T) {
	c := config()
	c.Sources[0].Rules.Include = []string{"a*b", "Ä"}
	c.Sources[0].Rules.Resolution = ptr("any")
	s := build(t, c, page("[组] 动画 [30] A*B Ä", "[组] 动画 [31] axb Ä", "[组] 动画 [32] A*B ä"))
	if len(s.SelectedIDs) != 1 || !s.Items[0].Selectable {
		t.Fatal("literal/ASCII rules", s.Items)
	}
	c.Sources[0].Rules.Include = nil
	c.Sources[0].Rules.Resolution = ptr("1080p")
	s = build(t, c, page("[组] 动画 [30]", "[组] 动画 [31][720p][1080p]"))
	if len(s.SelectedIDs) != 0 {
		t.Fatal("unknown/conflicting resolution accepted")
	}
}

func TestPreviewModesChooseOnlyCurrentHighestVersion(t *testing.T) {
	for _, mode := range []string{"from_episode", "new_only", "selected_history"} {
		t.Run(mode, func(t *testing.T) {
			c := config()
			c.Sources[0].Rules.InitialMode = mode
			s := build(t, c, page("[组] 动画 [30][1080p]", "[组] 动画 [30v2][1080p]"))
			if len(s.Items) != 2 || s.Items[0].Selectable || !s.Items[1].Selectable || s.Items[0].ReasonCode != "RELEASE_VERSION_SUPERSEDED" {
				t.Fatal("highest version not selected", s.Items)
			}
			want := 0
			if mode == "from_episode" {
				want = 1
			}
			if len(s.SelectedIDs) != want {
				t.Fatal("initial mode", s.SelectedIDs)
			}
			if err := s.Select([]string{s.Items[1].Release.ID}, nil, s.GeneratedAt); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPreviewCachedObservedTimeAndAbsoluteExpiry(t *testing.T) {
	p := page("[组] 动画 [30][1080p]")
	p.Releases[0].PublishedRaw = "2026/10/18 20:15"
	s := build(t, config(), p)
	if s.T0 != observed.UnixMilli()/60000*60000 || s.GeneratedAt == s.T0 {
		t.Fatal("T0 does not use upstream precision", s)
	}
	if !s.ValidAt(s.ExpiresAt-1) || s.ValidAt(s.ExpiresAt) || !errors.Is(s.Select(nil, nil, s.ExpiresAt), ErrExpired) {
		t.Fatal("expiry boundary")
	}
	for _, test := range []struct {
		p    animeparse.Publication
		want string
	}{
		{animeparse.Publication{Status: "missing"}, "publication_needs_confirmation"},
		{animeparse.Publication{Status: "known", UnixMillis: ptr(s.T0 - 1)}, "older_history_needs_confirmation"},
		{animeparse.Publication{Status: "known", UnixMillis: ptr(s.T0)}, "post_preview_new"},
		{animeparse.Publication{Status: "known", UnixMillis: ptr(observed.UnixMilli() + 300001)}, "publication_needs_confirmation"},
	} {
		if got := ClassifyTime(test.p, s.T0, observed.UnixMilli()); got != test.want {
			t.Fatal(got, test.want)
		}
	}
}

func TestPreviewReconcileChangedMissingAndReturnedChoices(t *testing.T) {
	old := build(t, config(), page("[组] 动画 [30][1080p]", "[组] 动画 [31][1080p]", "[组] 动画 [32][1080p]"))
	p := page("[组] 动画 [30v2][1080p]", "[组] 动画 [31][1080p]", "[组] 动画 [33][1080p]")
	p.Releases[2].PostKey = fmt.Sprintf("%040x", 4)
	current := build(t, config(), p)
	current.Reconcile(old)
	if len(current.SelectedIDs) != 1 || len(current.Pending) != 2 || len(current.Changes.ChangedIDs) != 1 || len(current.Changes.UnresolvedIDs) != 1 || len(current.Changes.AddedIDs) != 1 {
		t.Fatal("lost choices", current)
	}
	if current.Pending[1].Reason != "out_of_loaded_scope" || current.Pending[1].Snapshot == nil || current.Pending[1].Snapshot.Release.Title != old.Items[2].Release.Title || !errors.Is(current.ValidateSelection(current.GeneratedAt), ErrReview) {
		t.Fatal("missing claimed deleted or silently accepted")
	}
	if err := current.Select([]string{old.Items[2].Release.ID}, nil, current.GeneratedAt); !errors.Is(err, ErrInput) {
		t.Fatal("ghost selection", err)
	}
	restored := build(t, config(), old.Page)
	restored.Reconcile(current)
	if len(restored.Pending) != 0 || len(restored.SelectedIDs) != 3 {
		t.Fatal("returning choice not recovered", restored)
	}
	if err := current.Select(current.SelectedIDs, []string{old.Items[0].Release.ID, old.Items[2].Release.ID}, current.GeneratedAt); err != nil {
		t.Fatal(err)
	}
	refreshed := build(t, config(), old.Page)
	refreshed.Reconcile(current)
	if len(refreshed.SelectedIDs) != 1 || len(refreshed.DismissedIDs) != 2 {
		t.Fatal("dismissed rows selected again", refreshed)
	}
}

func TestPreviewWholeLoadedSelectionAndGaps(t *testing.T) {
	c := config()
	c.Sources[0].Binding.Scope.Last = ptr(200)
	titles := make([]string, 0, 80)
	for i := 30; i < 110; i++ {
		if i == 60 {
			continue
		}
		titles = append(titles, fmt.Sprintf("[组] 动画 [%d][1080p]", i))
	}
	p := page(titles...)
	p.RequestedTake = 100
	s := build(t, c, p)
	second, err := s.Releases(2, 0, s.GeneratedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 29 || len(second.SelectedIDs) != 79 || second.Summary.Gaps[0] != (Gap{60, 60}) || *second.Summary.LoadedFirst != 30 {
		t.Fatal("paging/loaded gaps", second)
	}
	if err := s.Select([]string{s.Items[0].Release.ID, s.Items[78].Release.ID}, nil, s.GeneratedAt); err != nil {
		t.Fatal("cross-page selection", err)
	}
	if _, err := s.Releases(0, 101, s.GeneratedAt); err == nil {
		t.Fatal("oversize page")
	}
	if s.Page.RawCount != 79 || s.Summary(s.GeneratedAt).SelectedCount != 2 {
		t.Fatal("selection truncated")
	}
}

func TestPreviewRawCountBeforeDedupAndReliableEnd(t *testing.T) {
	p := page("[组] 动画 [30][1080p]")
	p.Releases = append(p.Releases, p.Releases[0])
	p.RawCount = 2
	s := build(t, config(), p)
	if len(s.Items) != 1 || s.Summary(s.GeneratedAt).RawCount != 2 {
		t.Fatal("raw rows deduped")
	}
	p.RequestedTake = 2
	if _, err := Build(config(), "main", p, observed.UnixMilli()); err == nil {
		t.Fatal("full response claimed end")
	}
	empty := build(t, config(), page())
	if empty.Summary(empty.GeneratedAt).LoadedFirst != nil || !reflect.DeepEqual(empty.SelectedIDs, []string{}) {
		t.Fatal("empty source")
	}
}

func TestPreviewMultipleSpecificationProfiles(t *testing.T) {
	c := config()
	r := &c.Sources[0].Rules
	r.Resolution = ptr("any")
	r.SpecMode = "profiles"
	r.SpecProfiles = []model.AnimeSpec{{Resolution: "1080p", Languages: []string{"zh-Hans"}, SubtitleMode: "hard"}, {Resolution: "1080p", Languages: []string{"zh-Hant"}, SubtitleMode: "hard"}}
	s := build(t, c, page("[组] 动画 [30][1080p][简体内嵌]", "[组] 动画 [30v2][1080p][繁体内嵌]", "[组] 动画 [30][1080p][简繁内封]", "[组] 动画 [31][1080p]"))
	want := []string{"MATCHED_METADATA_ONLY", "MATCHED_METADATA_ONLY", "TITLE_SPECIFICATION_MISMATCH", "TITLE_SPECIFICATION_UNKNOWN"}
	for i, item := range s.Items {
		if item.ReasonCode != want[i] {
			t.Fatalf("row%d: %+v", i, item)
		}
	}
	if len(s.SelectedIDs) != 2 {
		t.Fatal("same episode versions were lost", s.SelectedIDs)
	}
	r.SpecProfiles = []model.AnimeSpec{{Resolution: "2160p"}}
	s = build(t, c, page("[组] 动画 [30][1080p][简体内嵌]"))
	if len(s.SelectedIDs) != 0 {
		t.Fatal("zero matches broadened profile")
	}
	r.SpecMode = "any"
	r.SpecProfiles = nil
	s = build(t, c, page("[组] 动画 [30][1080p][简体内嵌]"))
	if len(s.SelectedIDs) != 1 {
		t.Fatal("explicit all specifications failed")
	}
}

func TestSpecificationNormalizationAndFingerprint(t *testing.T) {
	c := config()
	c.Sources[0].Rules.SpecMode = "profiles"
	c.Sources[0].Rules.SpecProfiles = []model.AnimeSpec{{Languages: []string{"zh-Hans", "ja"}, Resolution: "1080p"}, {Languages: []string{"ja", "zh-Hans"}, Resolution: "1080p"}}
	n, err := Normalize(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Sources[0].Rules.SpecProfiles) != 1 {
		t.Fatal("profile normalization", n)
	}
	a, _ := ConfigFingerprint(n)
	n.Sources[0].Rules.SpecProfiles[0].Resolution = "2160p"
	b, _ := ConfigFingerprint(n)
	if a == b {
		t.Fatal("spec change did not invalidate preview")
	}
	r, err := NormalizeRules(config().Sources[0].Rules)
	if err != nil || r.SpecProfiles != nil {
		t.Fatal("empty optional profiles must roundtrip as nil", r, err)
	}
}

func TestPreviewSilverBulletLanguageAndContainerMatching(t *testing.T) {
	c := config()
	r := &c.Sources[0].Rules
	r.Resolution = ptr("any")
	r.SpecMode = "profiles"
	r.SpecProfiles = []model.AnimeSpec{{Resolution: "1080p", Languages: []string{"ja", "zh-Hans"}, Container: "mp4"}}
	titles := []string{"[银色子弹字幕组][名侦探柯南][第30集][简日双语MP4][1080P]", "[银色子弹字幕组][名侦探柯南][第30集][繁日双语MP4][1080P]", "[银色子弹字幕组][名侦探柯南][第30集][简繁日多语MKV][1080P]", "[银色子弹字幕组][名侦探柯南][第31集][简日双语MP4/繁日双语MP4/简繁日多语MKV][1080P]"}
	s := build(t, c, page(titles...))
	if len(s.SelectedIDs) != 1 || !s.Items[0].Selectable {
		t.Fatal("simplified MP4 selection mixed other languages/containers", s.Items)
	}
	r.SpecProfiles = append(r.SpecProfiles, model.AnimeSpec{Resolution: "1080p", Languages: []string{"ja", "zh-Hant"}, Container: "mp4"})
	s = build(t, c, page(titles...))
	if len(s.SelectedIDs) != 2 {
		t.Fatal("multiple language versions cannot coexist", s.Items)
	}
	r.SpecProfiles = []model.AnimeSpec{{Resolution: "1080p", Languages: []string{"ja", "zh-Hans", "zh-Hant"}, Container: "mkv"}}
	s = build(t, c, page(titles...))
	if len(s.SelectedIDs) != 1 || s.Items[3].ReasonCode != "TITLE_SPECIFICATION_UNKNOWN" {
		t.Fatal("ambiguous container must need review", s.Items)
	}
}

func TestPreviewANiSeasonNumberIsNotAnEpisodeRange(t *testing.T) {
	c := config()
	source := &c.Sources[0]
	source.Provider, source.ItemID, source.GroupID = "anibt", ptr("393010"), ptr("ani")
	source.Binding = model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "393010", GroupID: "ani"}}
	source.Rules.MinimumEpisode = ptr(1)
	source.Rules.SpecMode = "profiles"
	source.Rules.SpecProfiles = []model.AnimeSpec{{Resolution: "1080p", Languages: []string{"zh-Hant"}, Codec: "h264", Container: "mp4"}}
	p := page(
		"[ANi] Tantei wa Mō Shindeiru / 侦探已经，死了。 Season 2 - 02 [1080P][Baha][WEB-DL][AAC AVC][CHT][MP4]",
		"[ANi] 侦探已经，死了。 Season 2 - 01 [1080P][Baha][WEB-DL][AAC AVC][CHT][MP4]",
		"[ANi] 侦探已经，死了。 Season 2 - 01-12 [1080P][Baha][WEB-DL][AAC AVC][CHT][MP4]",
	)
	p.Source = mikan.Source{Provider: "anibt", ItemID: "393010", GroupID: "ani"}
	for i := range p.Releases {
		p.Releases[i].PostKey = fmt.Sprintf("rel_ani_fixture_%d", i+1)
	}
	s := build(t, c, p)
	if len(s.SelectedIDs) != 2 {
		t.Fatal("ordinary ANi episodes did not match the selected specification", s.Items)
	}
	for i, episode := range []int{2, 1} {
		item := s.Items[i]
		if !item.Selectable || item.Decision != "matched" || item.Release.Parse.Episode == nil || *item.Release.Parse.Episode != episode || item.Release.ParserVersion != animeparse.ParserVersion {
			t.Fatal("season number replaced the publisher episode", item)
		}
	}
	if s.Items[2].Selectable || s.Items[2].Release.Parse.Kind != "batch" || s.Items[2].ReasonCode != "EPISODE_NUMBER_UNRESOLVED" {
		t.Fatal("a genuine episode range became a single episode", s.Items[2])
	}
}
