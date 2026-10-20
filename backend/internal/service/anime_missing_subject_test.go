package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/client/bangumi"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/storage"
)

func missingSubjectSource(read func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error)) func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error) {
	return func(ctx context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
		page, err := read(ctx, in)
		if err == nil && page.Entry != nil {
			page.Entry.BangumiIDs = []string{"583313"}
			page.Entry.Title = "草莓哀歌"
			page.Entry.CoverURL = "/api/mikan/image/202601/poster.jpg"
		}
		return page, err
	}
}

func TestAnimeBindingMissingSubjectUsesVerifiedSourceOnly(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		match bool
		want  error
	}{
		{"missing", bangumi.ErrNotFound, true, nil},
		{"unrelated source", bangumi.ErrNotFound, false, ErrAnimeBindingInput},
		{"timeout", bangumi.ErrTimeout, true, bangumi.ErrTimeout},
		{"cooldown", bangumi.ErrCooldown, true, bangumi.ErrCooldown},
		{"access", bangumi.ErrAccess, true, bangumi.ErrAccess},
		{"invalid", bangumi.ErrInvalid, true, bangumi.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, source, bgm := bindingTestService()
			bgm.err = tc.err
			source.read = missingSubjectSource(func(_ context.Context, in mikan.SourceRequest) (mikan.SourcePage, error) {
				return animeMikanFakePage(in), nil
			})
			in := bindingTestInput()
			in.Entry.BangumiID = bindingPtr("583313")
			if !tc.match {
				in.Entry.BangumiID = bindingPtr("999999")
			}
			got, err := s.CheckBinding(context.Background(), animeMikanTestBudget(t, nil), in)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("got %v, want %v", err, tc.want)
				}
				if !errors.Is(tc.err, bangumi.ErrNotFound) && source.reads != 0 {
					t.Fatal("non-missing subject error read fallback source")
				}
				return
			}
			if err != nil || !got.Ready || got.Entry.Title != "草莓哀歌" || got.Entry.BangumiID == nil || *got.Entry.BangumiID != "583313" || !got.Entry.Metadata.BangumiUnavailable || got.Entry.Metadata.CoverURL == "" {
				t.Fatalf("missing subject blocked verified source: %+v %v", got, err)
			}
		})
	}
}

func TestAnimeMissingSubjectSaveRetainsIdentityAndLocalEdits(t *testing.T) {
	s, source, dirs, in := animeAdminFixture(t)
	ctx := context.Background()
	in.SettingsRevision = animeSaveReadySettings(t, s).Revision
	in.Config.Entry = AnimeEntryRef{BangumiID: bindingPtr("583313"), EditionKey: "original"}
	bgm := s.Bindings.Subjects.(*bindingSubjectsFake)
	bgm.err = bangumi.ErrNotFound
	source.read = missingSubjectSource(source.read)
	reads := dirs.calls
	receipt, err := s.SaveDefinition(ctx, "admin", "missing_subject_save", "", in)
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.Subscription(ctx, receipt.ID)
	if err != nil || view.Entry.BangumiID == nil || *view.Entry.BangumiID != "583313" || view.Entry.Title != "草莓哀歌" || !view.Entry.Metadata.BangumiUnavailable || view.Entry.Metadata.CoverURL == "" || dirs.calls != reads {
		t.Fatalf("lost fallback metadata or read 115: %+v %v", view, err)
	}
	if _, err := s.SaveDefinition(ctx, "admin", "missing_subject_duplicate", "", in); !errors.Is(err, storage.ErrAnimeExists) {
		var exists *storage.AnimeExistsError
		if !errors.As(err, &exists) || exists.ID != receipt.ID {
			t.Fatalf("duplicate external hint created another subscription: %v", err)
		}
	}
	bgm.calls = 0
	source.read = func(context.Context, mikan.SourceRequest) (mikan.SourcePage, error) {
		t.Fatal("local edit read source again")
		return mikan.SourcePage{}, mikan.ErrNetwork
	}
	in.Config.Entry = AnimeEntryRef{ID: receipt.EntryID}
	in.ExpectedRevision = receipt.Revision
	in.Config.Sources = nil
	for _, saved := range view.Sources {
		in.Config.Sources = append(in.Config.Sources, animeSourceConfig(saved.AnimeSource))
	}
	for i := range in.Config.Sources {
		in.Config.Sources[i].Status = "paused"
	}
	for i := range in.Config.Naming.Sources {
		in.Config.Naming.Sources[i].SourceRef = receipt.SourceIDs["main"]
	}
	if _, err := s.SaveDefinition(ctx, "admin", "missing_subject_edit", receipt.ID, in); err != nil || bgm.calls != 0 {
		t.Fatalf("local edit depends on external subject: %v (%d reads)", err, bgm.calls)
	}
}

func TestAnimeMissingSubjectReleasePreview(t *testing.T) {
	s, source, _, config, _ := previewService(t)
	s.Bindings.Subjects.(*bindingSubjectsFake).err = bangumi.ErrNotFound
	source.read = missingSubjectSource(source.read)
	config.Chapter = animepreview.Chapter{BangumiID: bindingPtr("583313"), EditionKey: "original", DraftKey: strings.Repeat("a", 32)}
	result, err := s.Generate(context.Background(), "admin", AnimePreviewInput{Config: config, SourceRef: "main"})
	if err != nil || result.Batch == nil || len(result.Sources) != 1 || result.Sources[0].MatchedCount != 2 || source.reads != 2 || result.Batch.Config.Chapter.BangumiID == nil || *result.Batch.Config.Chapter.BangumiID != "583313" {
		t.Fatalf("missing subject blocked preview or added requests: %+v %v (%d reads)", result, err, source.reads)
	}
}
