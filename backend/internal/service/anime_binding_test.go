package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func bindingPtr[T any](v T) *T { return &v }

type bindingSubjectsFake struct {
	calls  int
	err    error
	result *bangumi.SubjectResult
}

func (f *bindingSubjectsFake) Subject(ctx context.Context, id string) (bangumi.SubjectResult, error) {
	f.calls++
	if err := ctx.Err(); err != nil {
		return bangumi.SubjectResult{}, err
	}
	if f.result != nil {
		return *f.result, f.err
	}
	return bangumi.SubjectResult{CacheMeta: bangumi.CacheMeta{FetchedAt: time.Now().UnixMilli(), ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}, Subject: bangumi.Subject{Card: bangumi.Card{ID: id, Title: "测试篇章", OriginalTitle: "Original Title"}, Synopsis: "可信简介", TotalEpisodes: bindingPtr(13)}}, f.err
}

type bindingMikanFake struct {
	service         *MikanService
	reads, searches int
	read            func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error)
	search          func(string, string) (mikan.Result, error)
}

func (f *bindingMikanFake) Search(ctx context.Context, keyword, group string) (mikan.Result, error) {
	f.searches++
	f.service.lastRequest = time.Time{} // Called only by the site's gate holder.
	if err := ctx.Err(); err != nil {
		return mikan.Result{}, err
	}
	if f.search != nil {
		return f.search(keyword, group)
	}
	return mikan.Result{Items: []mikan.Item{}, Bangumis: []mikan.Bangumi{{ID: "2259", Title: "测试篇章"}}}, nil
}

func (f *bindingMikanFake) ReadSource(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
	f.reads++
	f.service.lastRequest = time.Time{}
	if err := ctx.Err(); err != nil {
		return mikan.SourcePage{}, err
	}
	if f.read != nil {
		return f.read(ctx, in)
	}
	page := animeMikanFakePage(in)
	if in.Kind == "entry" {
		page.Entry.BangumiIDs = []string{"278826"}
	}
	if in.Kind == "rss" {
		page.Releases = []mikan.SourceRelease{}
		for i, title := range []string{"[组] 动画 [26]", "[组] 动画 [30v2]", "[组] 动画 [38]", "[组] 动画 [39]", "[组] 动画 [SP26]", "[组] 动画 [26-38]"} {
			page.Releases = append(page.Releases, mikan.SourceRelease{PostKey: fmt.Sprintf("%040x", i+1), Title: title})
		}
		page.RawCount = len(page.Releases)
	}
	return page, nil
}

func bindingTestService() (*AnimeBindingService, *bindingMikanFake, *bindingSubjectsFake) {
	f := &bindingMikanFake{}
	m := NewMikanService(f, nil)
	f.service = m
	b := &bindingSubjectsFake{}
	return &AnimeBindingService{Mikan: m, Subjects: b}, f, b
}

func bindingTestInput() AnimeBindingInput {
	return AnimeBindingInput{Entry: AnimeEntryRef{BangumiID: bindingPtr("278826"), EditionKey: "original"}, Source: mikan.Source{ItemID: "2259", GroupID: "45"}, Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: bindingPtr(26), Last: bindingPtr(38)}, ChapterCorrespondence: &model.AnimeChapterCorrespondence{SourceEpisode: 26, ChapterEpisode: 1}}}
}

func TestAnimeBindingExplicitScopeAndVerifiedMetadata(t *testing.T) {
	s, f, bgm := bindingTestService()
	in := bindingTestInput()
	got, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in)
	if err != nil || !got.Ready || got.Source.GroupName != "字幕组甲" || got.Source.Title != "测试番剧" || got.Entry.Title != "测试篇章" || got.Entry.ID != "" || got.Entry.Metadata.Synopsis != "可信简介" || len(got.RangePreview) != 2 || *got.RangePreview[0].ChapterEpisode != 1 || *got.RangePreview[1].ChapterEpisode != 13 {
		t.Fatalf("unexpected check: %+v %v", got, err)
	}
	if f.reads != 1 || bgm.calls != 1 || !strings.HasSuffix(got.Source.RSSURL, "bangumiId=2259&subgroupid=45") {
		t.Fatal("unexpected reads or uncontrolled URL", f.reads, bgm.calls, got.Source.RSSURL)
	}
	*got.Binding.Scope.First = 30
	got.Binding.ChapterCorrespondence.SourceEpisode = 30
	got.Source.BangumiIDs[0] = "modified"
	if *in.Binding.Scope.First != 26 || in.Binding.ChapterCorrespondence.SourceEpisode != 26 {
		t.Fatal("result aliases request")
	}
	again, err := s.Mikan.ReadAnimeSourceEntry(context.Background(), animeMikanTestBudget(t, nil), "2259", false)
	if err != nil || !again.Cached || again.Entry.BangumiIDs[0] != "278826" || f.reads != 1 {
		t.Fatal("result mutated source cache", again, err)
	}
}

func TestAnimeBindingNamesAndSubjectHintsNeverConfirm(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		s, _, _ := bindingTestService()
		in := bindingTestInput()
		in.Binding.Confirmed = confirmed
		got, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in)
		if err != nil || got.Ready != confirmed || got.Binding.Confirmed != confirmed || len(got.Notes) != 1 || got.Notes[0] != "BANGUMI_LINK_IS_HINT" {
			t.Fatalf("subject link changed confirmation: %+v %v", got, err)
		}
	}
	s, f, _ := bindingTestService()
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(in)
		p.Entry.BangumiIDs = []string{"305041"}
		return p, nil
	}
	got, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), bindingTestInput())
	if err != nil || !got.Ready || *got.Entry.BangumiID != "278826" || len(got.Notes) != 2 || got.Notes[1] != "BANGUMI_HINT_DIFFERS" {
		t.Fatalf("hint silently rebound chapter: %+v %v", got, err)
	}
}

func TestAnimeBindingMissingScopeAndIndependentGroups(t *testing.T) {
	s, f, _ := bindingTestService()
	in := bindingTestInput()
	in.Binding = model.AnimeBinding{SchemaVersion: 1}
	got, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in)
	if err != nil || got.Ready || len(got.Issues) != 2 || got.Binding.ChapterCorrespondence != nil {
		t.Fatalf("incomplete binding promoted: %+v %v", got, err)
	}
	main := bindingTestInput()
	if _, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), main); err != nil {
		t.Fatal(err)
	}
	backup := bindingTestInput()
	backup.Source.GroupID = "28"
	backup.Binding = model.AnimeBinding{SchemaVersion: 1}
	got, err = s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), backup)
	if err != nil || got.Ready || got.Source.GroupName != "字幕组乙" || got.Binding.Scope != nil || got.Binding.ChapterCorrespondence != nil || f.reads != 1 {
		t.Fatalf("backup inherited main configuration: %+v %v", got, err)
	}
}

func TestAnimeBindingPreviewExplainsActualPublisherRows(t *testing.T) {
	s, f, _ := bindingTestService()
	budget := animeMikanTestBudget(t, nil)
	got, err := s.PreviewBinding(context.Background(), budget, bindingTestInput())
	if err != nil || !got.Ready || got.RawCount == nil || *got.RawCount != 6 || len(got.Releases) != 6 || got.Completeness != "partial" || f.reads != 2 || budget.Stats().Requests != 2 {
		t.Fatalf("unexpected preview: %+v %v", got, err)
	}
	for i, want := range []string{"in_scope", "in_scope", "in_scope", "out_of_scope", "needs_confirmation", "needs_confirmation"} {
		if got.Releases[i].Projection.Status != want || got.Releases[i].ParserVersion != animeparse.ParserVersion {
			t.Fatalf("row %d: %+v", i, got.Releases[i])
		}
	}
	if *got.Releases[1].Projection.SourceEpisode != 30 || *got.Releases[1].Projection.ChapterEpisode != 5 || got.Releases[1].Parse.VersionLabel != "v2" {
		t.Fatal("lost source numbering or version")
	}
	for _, r := range got.Releases {
		if r.Parse.Kind != "episode" && r.Projection.ChapterEpisode != nil {
			t.Fatal("unsupported title mapped to a chapter")
		}
	}
}

func TestAnimeBindingCheckDoesNotRequireSuccessfulRSS(t *testing.T) {
	s, f, _ := bindingTestService()
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		if in.Kind == "rss" {
			return mikan.SourcePage{}, mikan.ErrNetwork
		}
		return animeMikanFakePage(in), nil
	}
	check, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), bindingTestInput())
	if err != nil || !check.Ready || f.reads != 1 {
		t.Fatalf("check depends on history: %+v %v", check, err)
	}
	preview, err := s.PreviewBinding(context.Background(), animeMikanTestBudget(t, nil), bindingTestInput())
	if !errors.Is(err, mikan.ErrNetwork) || preview.RawCount != nil || preview.ObservedAt != nil {
		t.Fatalf("failure shown as normal zero rows: %+v %v", preview, err)
	}
}

func TestAnimeBindingPreviewZeroRowsAndFixedSource(t *testing.T) {
	s, f, _ := bindingTestService()
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(in)
		if in.Kind == "rss" {
			p.Releases = []mikan.SourceRelease{}
			p.RawCount = 0
		}
		return p, nil
	}
	budget := animeMikanTestBudget(t, nil)
	got, err := s.PreviewBinding(context.Background(), budget, bindingTestInput())
	if err != nil || got.RawCount == nil || *got.RawCount != 0 || got.Releases == nil || got.Completeness != "partial" {
		t.Fatalf("zero rows mistaken for failure/end: %+v %v", got, err)
	}
	in := bindingTestInput()
	in.Source.GroupID = "28"
	before := f.reads
	if _, err := s.CheckBinding(context.Background(), budget, in); !errors.Is(err, mikan.ErrInput) || f.reads != before {
		t.Fatal("operation changed publisher", err)
	}
}

func TestAnimeBindingRejectsInvalidMetadataBeforeCaching(t *testing.T) {
	for _, change := range []func(*mikan.SourcePage){
		func(p *mikan.SourcePage) { p.Entry = nil },
		func(p *mikan.SourcePage) { p.Entry.Groups = append(p.Entry.Groups, p.Entry.Groups[0]) },
		func(p *mikan.SourcePage) { p.Entry.Groups[0].Name = "wrong\x00name" },
		func(p *mikan.SourcePage) { p.Entry.Title = "" },
		func(p *mikan.SourcePage) { p.ObservedAt = time.Time{} },
	} {
		s, f, _ := bindingTestService()
		f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
			p := animeMikanFakePage(in)
			change(&p)
			return p, nil
		}
		if _, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), bindingTestInput()); !errors.Is(err, mikan.ErrParse) || len(s.Mikan.animeCache) != 0 {
			t.Fatal("invalid metadata cached", err)
		}
	}
}

func TestAnimeBindingRejectsWrongGroupAndInvalidInput(t *testing.T) {
	for _, change := range []func(*AnimeBindingInput){
		func(in *AnimeBindingInput) { in.Source.GroupID = "0" },
		func(in *AnimeBindingInput) { in.Entry.BangumiID = bindingPtr("0278826") },
		func(in *AnimeBindingInput) { in.Entry.ID = "local" },
		func(in *AnimeBindingInput) { in.Binding.Scope.Last = bindingPtr(25) },
		func(in *AnimeBindingInput) { in.Binding.ChapterCorrespondence.SourceEpisode = 30 },
		func(in *AnimeBindingInput) { in.Binding.Scope = &model.AnimeScope{Kind: "confirmed_chapter"} },
	} {
		s, f, bgm := bindingTestService()
		in := bindingTestInput()
		change(&in)
		if _, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in); !errors.Is(err, ErrAnimeBindingInput) {
			t.Fatal("invalid input accepted", err)
		}
		if f.reads != 0 || bgm.calls != 0 {
			t.Fatal("invalid input performed upstream reads")
		}
	}
	s, f, _ := bindingTestService()
	in := bindingTestInput()
	in.Source.GroupID = "999"
	if _, err := s.PreviewBinding(context.Background(), animeMikanTestBudget(t, nil), in); !errors.Is(err, mikan.ErrSourceBinding) || f.reads != 1 {
		t.Fatal("unverified group fetched", f.reads, err)
	}
}

func TestAnimeBindingSourceShortcutNeedsNoBangumi(t *testing.T) {
	s, _, bgm := bindingTestService()
	in := bindingTestInput()
	in.Entry.BangumiID = nil
	got, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in)
	if err != nil || !got.Ready || got.Entry.BangumiID != nil || got.Entry.Title != "测试番剧" || got.Entry.EditionKey != "original" || bgm.calls != 0 {
		t.Fatalf("shortcut requires external identity: %+v %v", got, err)
	}
}

func TestAnimeBindingCandidatesAreBoundedAndUnconfirmed(t *testing.T) {
	s, f, _ := bindingTestService()
	f.search = func(keyword, group string) (mikan.Result, error) {
		if keyword != "测试篇章" && keyword != "Original Title" {
			t.Fatal("wrong search title", keyword)
		}
		cards := []mikan.Bangumi{}
		for i := 1; i <= 105; i++ {
			cards = append(cards, mikan.Bangumi{ID: fmt.Sprint(i), Title: "测试篇章", URL: "https://evil.test/"})
		}
		return mikan.Result{Items: []mikan.Item{}, Bangumis: cards}, nil
	}
	ref := bindingTestInput().Entry
	got, err := s.SearchMikanCandidates(context.Background(), AnimeMikanCandidatesInput{Entry: &ref})
	if err != nil || len(got.Items) != 100 || !got.Truncated || got.Entry == nil || got.Entry.ID != "" || len(got.Attempts) != 2 || got.Items[0].URL != mikan.BaseURL+"/Home/Bangumi/1" || f.reads != 0 {
		t.Fatalf("unexpected candidates: %+v %v", got, err)
	}
	// Explicit keywords use a single search and do not silently add title fallbacks.
	got, err = s.SearchMikanCandidates(context.Background(), AnimeMikanCandidatesInput{Keyword: "测试篇章", GroupID: "45"})
	if err != nil || len(got.Attempts) != 1 || got.Entry != nil {
		t.Fatalf("manual search changed chapter: %+v %v", got, err)
	}
}

func TestAnimeBindingCandidatesZeroAndFailureStayDistinct(t *testing.T) {
	s, f, _ := bindingTestService()
	f.search = func(string, string) (mikan.Result, error) {
		return mikan.Result{Items: []mikan.Item{}, Bangumis: []mikan.Bangumi{}}, nil
	}
	zero, err := s.SearchMikanCandidates(context.Background(), AnimeMikanCandidatesInput{Keyword: "zero"})
	if err != nil || zero.Items == nil || len(zero.Items) != 0 {
		t.Fatal(zero, err)
	}
	f.search = func(string, string) (mikan.Result, error) { return mikan.Result{}, mikan.ErrNetwork }
	if _, err := s.SearchMikanCandidates(context.Background(), AnimeMikanCandidatesInput{Keyword: "failure"}); !errors.Is(err, mikan.ErrNetwork) {
		t.Fatal("search failure hidden", err)
	}
	reads := f.searches
	if _, err := s.SearchMikanCandidates(context.Background(), AnimeMikanCandidatesInput{Keyword: "  "}); !errors.Is(err, mikan.ErrInput) || f.searches != reads {
		t.Fatal("empty search performed read", err)
	}
}

func TestAnimeBindingCancellationAndSubjectMismatch(t *testing.T) {
	s, f, bgm := bindingTestService()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.CheckBinding(ctx, animeMikanTestBudget(t, nil), bindingTestInput()); !errors.Is(err, context.Canceled) || f.reads != 0 || bgm.calls != 0 {
		t.Fatal("ignored cancellation", err)
	}
	bgm.result = &bangumi.SubjectResult{Subject: bangumi.Subject{Card: bangumi.Card{ID: "305041", Title: "same title"}}}
	if _, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), bindingTestInput()); !errors.Is(err, bangumi.ErrInvalid) || f.reads != 0 {
		t.Fatal("wrong subject accepted", err)
	}
}

func TestAnimeBindingMetadataFailurePreservesSuccessfulCache(t *testing.T) {
	s, f, _ := bindingTestService()
	first, err := s.Mikan.ReadAnimeSourceEntry(context.Background(), animeMikanTestBudget(t, nil), "2259", false)
	if err != nil {
		t.Fatal(err)
	}
	f.read = func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(in)
		p.Entry.ID = "999"
		return p, nil
	}
	if _, err := s.Mikan.ReadAnimeSourceEntry(context.Background(), animeMikanTestBudget(t, nil), "2259", true); !errors.Is(err, mikan.ErrParse) {
		t.Fatal("wrong entry cached", err)
	}
	last, err := s.Mikan.ReadAnimeSourceEntry(context.Background(), animeMikanTestBudget(t, nil), "2259", false)
	if err != nil || !last.Cached || last.Entry.ID != first.Entry.ID || !last.ObservedAt.Equal(first.ObservedAt) {
		t.Fatal("bad metadata replaced success", last, err)
	}
}

func TestAnimeBindingSQLiteRetainsIndependentSourceConfiguration(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "anime-binding.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	s, _, bgm := bindingTestService()
	s.Entries = db
	mainInput := bindingTestInput()
	main, err := s.CheckBinding(ctx, animeMikanTestBudget(t, nil), mainInput)
	if err != nil {
		t.Fatal(err)
	}
	// Check/preview services are read-only, even for a new external chapter.
	if _, err := db.FindAnimeEntry(ctx, "278826", "original"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("confirmation created local entry", err)
	}
	entry, err := db.SaveAnimeEntry(ctx, main.Entry)
	if err != nil {
		t.Fatal(err)
	}
	backupInput := bindingTestInput()
	backupInput.Entry = AnimeEntryRef{ID: entry.ID}
	backupInput.Source.GroupID = "28"
	backupInput.Binding.Scope.First, backupInput.Binding.Scope.Last = bindingPtr(1), bindingPtr(13)
	backupInput.Binding.ChapterCorrespondence.SourceEpisode = 1
	bgm.err = bangumi.ErrNotFound
	backup, err := s.CheckBinding(ctx, animeMikanTestBudget(t, nil), backupInput)
	if err != nil {
		t.Fatal("existing identity depends on upstream", err)
	}
	config := model.AnimeSubscriptionConfig{EntryID: entry.ID, Status: "paused", Form: model.AnimeForm{SchemaVersion: 1}}
	for i, check := range []AnimeBindingCheck{main, backup} {
		role := "main"
		minimum := 30
		if i == 1 {
			role = "standby"
			minimum = 5
		}
		config.Sources = append(config.Sources, model.AnimeSourceConfig{Ref: role, Role: role, Position: i, Status: "paused", RunWhenEnabled: true, Provider: "mikan", ItemID: bindingPtr(check.Source.ItemID), GroupID: bindingPtr(check.Source.GroupID), GroupName: bindingPtr(check.Source.GroupName), Binding: check.Binding, Rules: model.AnimeRules{SchemaVersion: 1, Resolution: bindingPtr("any"), MinimumEpisode: &minimum}})
	}
	receipt, err := db.SaveAnimeSubscription(ctx, config, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sources) != 2 || got.Sources[0].Binding.ChapterCorrespondence.SourceEpisode != 26 || *got.Sources[0].Rules.MinimumEpisode != 30 || got.Sources[1].Binding.ChapterCorrespondence.SourceEpisode != 1 || *got.Sources[1].Rules.MinimumEpisode != 5 || got.Sources[0].NamespaceID == got.Sources[1].NamespaceID {
		t.Fatalf("source numbering/minimum merged: %+v", got.Sources)
	}
	if got.FirstEnabledAt != nil || got.Sources[0].ActiveBaselineID != nil || got.Sources[0].NextCheckAt != nil {
		t.Fatal("configuration authorized execution")
	}
}

func TestAnimeBindingStructuredReviewAndUnconfirmedSamples(t *testing.T) {
	s, f, _ := bindingTestService()
	in := bindingTestInput()
	in.Binding = model.AnimeBinding{SchemaVersion: 1}
	f.read = func(_ context.Context, req mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(req)
		if req.Kind == "rss" {
			p.Releases = []mikan.SourceRelease{{PostKey: fmt.Sprintf("%040x", 1), Title: "[组] 动画 [25][1080p][简体内嵌]"}, {PostKey: fmt.Sprintf("%040x", 2), Title: "[组] 动画 [25][1080p][繁体内嵌]"}}
			p.RawCount = 2
		}
		return p, nil
	}
	got, err := s.PreviewBinding(context.Background(), animeMikanTestBudget(t, nil), in)
	if err != nil || got.Ready || len(got.Profiles) != 2 || got.SpecVersion != animeparse.SpecVersion || got.Binding.Confirmed {
		t.Fatal("sample read required confirmation or lost profiles", got, err)
	}
	in.Binding = model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "confirmed_chapter"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: in.Source.ItemID, GroupID: in.Source.GroupID}}
	checked, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in)
	if err != nil || !checked.Ready {
		t.Fatal("explicit structured review rejected", checked, err)
	}
	checked.Binding.Review.GroupID = "28"
	if in.Binding.Review.GroupID == "28" {
		t.Fatal("review aliases input")
	}
	before := f.reads
	in.Binding.Review.GroupID = "28"
	if _, err = s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in); !errors.Is(err, ErrAnimeBindingInput) || f.reads != before {
		t.Fatal("review accepted for different publisher", err)
	}
	in.Binding.Review.GroupID = in.Source.GroupID
	in.Binding.Review.Method = "automatic"
	if _, err = s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in); !errors.Is(err, ErrAnimeBindingInput) {
		t.Fatal("automatic confirmation accepted", err)
	}
}

func TestAnimeBindingSilverBulletSpecificationOptions(t *testing.T) {
	data, err := os.ReadFile("../animeparse/testdata/silver_bullet_titles.json")
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	if err = json.Unmarshal(data, &titles); err != nil {
		t.Fatal(err)
	}
	s, f, _ := bindingTestService()
	f.read = func(_ context.Context, req mikan.SourceRequest) (mikan.SourcePage, error) {
		p := animeMikanFakePage(req)
		if req.Kind == "rss" {
			p.Releases = make([]mikan.SourceRelease, 0, len(titles))
			for i, title := range titles {
				p.Releases = append(p.Releases, mikan.SourceRelease{PostKey: fmt.Sprintf("%040x", i+1), Title: title})
			}
			p.RawCount = len(titles)
		}
		return p, nil
	}
	in := bindingTestInput()
	in.Binding = model.AnimeBinding{SchemaVersion: 1}
	got, err := s.PreviewBinding(context.Background(), animeMikanTestBudget(t, nil), in)
	if err != nil || len(got.Profiles) != 3 || got.ProfileReviewCount != 6 || len(got.Releases) != 100 {
		t.Fatal(got, err)
	}
	for _, profile := range got.Profiles {
		if profile.Spec.Container == "" || len(profile.Spec.Languages) < 2 {
			t.Fatal("mixed-resource title broadened a profile", profile)
		}
	}
}
