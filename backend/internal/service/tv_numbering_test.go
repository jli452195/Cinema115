package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

type numberingSourceFake struct {
	cacheTMDBFake
	group      *tmdb.EpisodeGroup
	groupError error
}

func (f *numberingSourceFake) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{ID: id, Kind: kind, Title: "千绪的上学路", Date: "2018-07-06"}, Status: "Ended", EpisodeCount: 29, Seasons: []tmdb.SeasonSummary{{ID: 1, Number: 1, Name: "第 1 季", EpisodeCount: 29}}}, nil
}
func (f *numberingSourceFake) EpisodeGroups(context.Context, int) ([]tmdb.EpisodeGroupSummary, error) {
	if f.groupError != nil {
		return nil, f.groupError
	}
	return []tmdb.EpisodeGroupSummary{f.group.EpisodeGroupSummary}, nil
}
func (f *numberingSourceFake) EpisodeGroup(context.Context, int, string) (*tmdb.EpisodeGroup, error) {
	if f.groupError != nil {
		return nil, f.groupError
	}
	return f.group, nil
}
func numberingFixture(t *testing.T) (*TMDBService, *numberingSourceFake, *storage.Store) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "numbering.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	episodes := make([]tmdb.Episode, 29)
	for i := range episodes {
		episodes[i] = tmdb.Episode{ID: i + 1, Number: i + 1, Name: fmt.Sprint("第", i+1, "集"), SourceSeason: 1, SourceNumber: i + 1, Date: "2018-07-06"}
	}
	source := &numberingSourceFake{cacheTMDBFake: cacheTMDBFake{episodes: episodes}, group: &tmdb.EpisodeGroup{EpisodeGroupSummary: tmdb.EpisodeGroupSummary{ID: "group12", Name: "Season", Type: 6, GroupCount: 1, EpisodeCount: 12}, Seasons: []tmdb.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: append([]tmdb.Episode{}, episodes[:12]...)}}}}
	return NewTMDBService(source, db), source, db
}
func saveGroup(t *testing.T, s *TMDBService, id int, revision int64) *tmdb.NumberingInfo {
	t.Helper()
	preview, err := s.PreviewNumbering(context.Background(), id, "group12")
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.SaveNumbering(context.Background(), id, SaveNumberingInput{GroupID: "group12", Revision: revision, Fingerprint: preview.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	return info
}
func TestNumberingSnapshotOverridesAllReadsAndSurvivesUpstreamChanges(t *testing.T) {
	s, source, db := numberingFixture(t)
	ctx := context.Background()
	old, _ := s.PinNumbering(ctx, 80462)
	info := saveGroup(t, s, 80462, 0)
	if info.Revision != 1 {
		t.Fatal(info)
	}
	source.group.Seasons[0].Episodes[0].Name = "上游乱改"
	source.groupError = errors.New("offline")
	restarted := NewTMDBService(source, db)
	detail, err := restarted.Details(tmdb.WithFresh(ctx), "tv", 80462)
	if err != nil || detail.EpisodeCount != 12 || detail.Numbering.Revision != 1 {
		t.Fatal(detail, err)
	}
	season, err := restarted.Season(tmdb.WithFresh(ctx), 80462, 1)
	if err != nil || len(season.Episodes) != 12 || season.Episodes[0].Name == "上游乱改" || season.CacheStale {
		t.Fatal(season, err)
	}
	schedule, _ := restarted.Schedule(ctx, 80462, 1)
	schedules, _ := restarted.Schedules(ctx, 80462)
	if len(schedule.Episodes) != 12 || schedule.NumberingRevision != 1 || len(schedules) != 1 || len(schedules[0].Episodes) != 12 {
		t.Fatal(schedule, schedules)
	}
	original, err := s.Season(old, 80462, 1)
	if err != nil || len(original.Episodes) != 29 || original.NumberingRevision != 0 {
		t.Fatal("pinned operation changed", original, err)
	}
	if _, err = s.SaveNumbering(ctx, 80462, SaveNumberingInput{Revision: 0}); !errors.Is(err, storage.ErrNumberingConflict) {
		t.Fatal("stale save accepted", err)
	}
	if _, err = s.SaveNumbering(ctx, 80462, SaveNumberingInput{Revision: 1}); err != nil {
		t.Fatal(err)
	}
	restored, _ := s.Details(ctx, "tv", 80462)
	if restored.EpisodeCount != 29 || restored.Numbering.Revision != 2 || restored.Numbering.GroupID != "" {
		t.Fatal(restored)
	}
}
func TestNumberingRejectsChangedPreviewAndWrongMembership(t *testing.T) {
	s, source, _ := numberingFixture(t)
	ctx := context.Background()
	preview, err := s.PreviewNumbering(ctx, 80462, "group12")
	if err != nil {
		t.Fatal(err)
	}
	source.group.Seasons[0].Episodes[0].Name = "changed"
	if _, err = s.SaveNumbering(ctx, 80462, SaveNumberingInput{GroupID: "group12", Fingerprint: preview.Fingerprint}); !errors.Is(err, storage.ErrNumberingConflict) {
		t.Fatal(err)
	}
	if _, err = s.PreviewNumbering(ctx, 80462, "anotherGroup"); !errors.Is(err, tmdb.ErrNotFound) {
		t.Fatal(err)
	}
	info, _ := s.numbering(ctx, 80462)
	if info.Info.Revision != 0 {
		t.Fatal("failed save mutated selection")
	}
}
func TestNumberingChoicesRetainsSnapshotWhenGroupListIsOffline(t *testing.T) {
	s, source, _ := numberingFixture(t)
	ctx := context.Background()
	saveGroup(t, s, 80462, 0)
	source.groupError = errors.New("offline")
	choices, err := s.NumberingChoices(ctx, 80462)
	if err != nil || choices.Snapshot == nil || choices.Snapshot.EpisodeCount != 12 || choices.GroupsError == "" || choices.DefaultEpisodeCount != 29 {
		t.Fatal(choices, err)
	}
	if _, err := s.SaveNumbering(ctx, 80462, SaveNumberingInput{Revision: choices.Current.Revision}); err != nil {
		t.Fatal("could not reset offline group", err)
	}
}

func TestNumberingSnapshotPreservesEmptySeason(t *testing.T) {
	s, source, db := numberingFixture(t)
	ctx := context.Background()
	before := source.group.Seasons[0]
	before.ID, before.Number = 12, 12
	after := tmdb.SeasonDetail{ID: 14, Number: 14, Name: "After", Episodes: []tmdb.Episode{{ID: 13, Number: 1, SourceSeason: 2, SourceNumber: 7}}}
	source.group.Seasons = []tmdb.SeasonDetail{before, {ID: 13, Number: 13, Name: "Empty", Episodes: []tmdb.Episode{}}, after}
	source.group.GroupCount, source.group.EpisodeCount = 3, 13
	saveGroup(t, s, 80462, 0)
	source.groupError = errors.New("offline")
	restarted := NewTMDBService(source, db)
	detail, err := restarted.Details(ctx, "tv", 80462)
	if err != nil {
		t.Fatal(err)
	}
	if detail.EpisodeCount != 13 || len(detail.Seasons) != 3 || detail.Seasons[1].Number != 13 || detail.Seasons[1].Name != "Empty" || detail.Seasons[1].EpisodeCount != 0 || detail.Seasons[2].Number != 14 {
		t.Fatalf("empty season lost in detail: %+v", detail)
	}
	season, err := restarted.Season(ctx, 80462, 13)
	if err != nil {
		t.Fatal(err)
	}
	if season.Number != 13 || season.Name != "Empty" || season.Episodes == nil || len(season.Episodes) != 0 || season.NumberingRevision != 1 {
		t.Fatalf("empty season lost in snapshot: %+v", season)
	}
	schedules, err := restarted.Schedules(ctx, 80462)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 3 || schedules[1].Number != 13 || schedules[1].Episodes == nil || len(schedules[1].Episodes) != 0 || schedules[2].Number != 14 || len(schedules[2].Episodes) != 1 {
		t.Fatalf("empty season changed schedules: %+v", schedules)
	}
}

func TestNumberingRejectsSavingAllEmptySeasons(t *testing.T) {
	for _, count := range []int{0, 12} {
		t.Run(fmt.Sprint("summary count ", count), func(t *testing.T) {
			s, source, _ := numberingFixture(t)
			ctx := context.Background()
			saveGroup(t, s, 80462, 0)
			source.group.Seasons = []tmdb.SeasonDetail{{ID: 13, Number: 13, Name: "Empty", Episodes: []tmdb.Episode{}}}
			source.group.EpisodeCount = count
			preview, err := s.PreviewNumbering(ctx, 80462, "group12")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.SaveNumbering(ctx, 80462, SaveNumberingInput{GroupID: "group12", Revision: 1, Fingerprint: preview.Fingerprint}); !errors.Is(err, tmdb.ErrInput) {
				t.Fatal("all-empty group was not rejected", err)
			}
			current, err := s.numbering(ctx, 80462)
			if err != nil {
				t.Fatal(err)
			}
			if current.Info.Revision != 1 || current.Group.EpisodeCount != 12 || len(current.Group.Seasons) != 1 || len(current.Group.Seasons[0].Episodes) != 12 {
				t.Fatalf("rejected save changed current snapshot: %+v", current)
			}
		})
	}
}

func TestNumberingVotesDoNotConflictButOversizedSnapshotIsRejected(t *testing.T) {
	s, source, _ := numberingFixture(t)
	ctx := context.Background()
	preview, err := s.PreviewNumbering(ctx, 80462, "group12")
	if err != nil {
		t.Fatal(err)
	}
	source.group.Seasons[0].Episodes[0].Votes = 999
	source.group.Seasons[0].Episodes[0].Rating = 8.5
	if _, err = s.SaveNumbering(ctx, 80462, SaveNumberingInput{GroupID: "group12", Fingerprint: preview.Fingerprint}); err != nil {
		t.Fatal("votes invalidated preview", err)
	}
	next, err := s.PreviewNumbering(ctx, 80462, "group12")
	if err != nil || next.Changed {
		t.Fatal(next, err)
	}
	source.group.Seasons[0].Episodes[0].Overview = strings.Repeat("a", 8<<20)
	oversized, err := s.PreviewNumbering(ctx, 80462, "group12")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveNumbering(ctx, 80462, SaveNumberingInput{GroupID: "group12", Revision: 1, Fingerprint: oversized.Fingerprint}); err == nil {
		t.Fatal("unreadable snapshot saved")
	}
	info, err := s.NumberingRevision(ctx, 80462)
	if err != nil || info != 1 {
		t.Fatal(info, err)
	}
}
func TestNumberingFollowAndResourceMatchUseTwelveEpisodes(t *testing.T) {
	s, _, _ := numberingFixture(t)
	ctx := context.Background()
	saveGroup(t, s, 80462, 0)
	library := &resourceLibraryFake{match: model.EmbyMatch{Kind: "tv", TMDB: "80462", State: "present", Items: []model.EmbyItem{{Type: "Episode", Season: 1, Episode: 1, EndEpisode: 12}}}}
	follows := &FollowService{TMDB: s, Library: library}
	p, err := follows.EvaluateFollow(ctx, model.FollowCheck{Kind: "tv", TMDBID: 80462}, time.Now())
	if err != nil || p.State != "completed" || p.Aired != 12 || p.Owned != 12 {
		t.Fatal(p, err)
	}
	matcher := NewResourceMatchService(s, library)
	batch, err := matcher.Match(ctx, "tv", 80462, []ResourceCandidate{{ID: "resource", Title: "千绪的上学路 (2018) S01E13-E29"}})
	if err != nil || len(batch.Missing) != 0 || batch.Results[0].Matched {
		t.Fatal(batch, err)
	}
	library.match.Items[0].EndEpisode = 11
	batch, err = matcher.Match(ctx, "tv", 80462, []ResourceCandidate{{ID: "resource", Title: "千绪的上学路 (2018) S01E12"}})
	if err != nil || len(batch.Missing) != 1 || !batch.Results[0].Matched || batch.Missing[0].Episode != 12 {
		t.Fatal(batch, err)
	}
}
func TestNumberingOrganizeAllowsUnlistedEpisodesAndInvalidatesOldPreview(t *testing.T) {
	metadata, _, _ := numberingFixture(t)
	s, _, account, _ := newOrganizeTest(t)
	s.metadata = metadata
	saveGroup(t, metadata, 42, 0)
	plan := organizeTestPreview(t, s, account, []OrganizeSelection{{ID: "101", Season: 1, Episode: 12}})
	if plan.Numbering == nil || plan.Numbering.Revision != 1 {
		t.Fatal(plan.Numbering)
	}
	scan, err := s.Scan(context.Background(), OrganizeScanInput{AccountID: account, ParentID: "10", ParentPath: "/来源", EntryID: "30"})
	if err != nil {
		t.Fatal(err)
	}
	unlisted, err := s.Preview(context.Background(), account, OrganizePreviewInput{ScanID: scan.ID, Kind: "tv", TMDBID: 42, Files: []OrganizeSelection{{ID: "101", Season: 1, Episode: 13, EndEpisode: 14}}})
	if err != nil || len(unlisted.Items) != 1 || !strings.Contains(unlisted.Items[0].Name, "S01E13-E14") {
		t.Fatal("TMDB numbering must remain advisory", unlisted, err)
	}
	if _, err = metadata.SaveNumbering(context.Background(), 42, SaveNumberingInput{Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Start(account, plan.ID); !errors.Is(err, ErrOrganizeConflict) {
		t.Fatal("old preview accepted", err)
	}
}
