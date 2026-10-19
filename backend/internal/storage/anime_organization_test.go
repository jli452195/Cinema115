package storage

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/model"
)

func organizationNaming(ref string) *model.AnimeNamingPlan {
	return &model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "335790", Title: "作品", Year: "2026", Season: 1, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 1, Episodes: []model.AnimeNamingEpisode{{ID: "201", Number: 1}}}}}, Sources: []model.AnimeSourceMapping{{SourceRef: ref, Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 1, TargetBase: 1}}}}}
}

func TestAnimeOrganizationClaimFreezesTargetAndLeavesExistingTaskUntouched(t *testing.T) {
	f := newDownloadClaimFixture(t)
	ctx := context.Background()
	naming := organizationNaming(f.sub.Sources[0].ID)
	raw, err := animeJSON(naming)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_subscriptions SET naming_json=?,revision=revision+1 WHERE id=?`, raw, f.sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_settings SET revision=revision+1,organize_cid='900',organize_path='/整理'`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CheckAnimeDownloadSettings(ctx, f.target); err != nil {
		t.Fatal("unchanged download binding rejected", err)
	}
	r, v := f.selectRelease(t, 0, "rel_dmhy_1", strings.Repeat("a", 40))
	first := f.claim(t, 0, r, v)
	if first.Job.Snapshot.Organization == nil || first.Job.Snapshot.Organization.Target.ParentCID != "900" || first.Job.Snapshot.Organization.PlanRevision != 2 || first.Job.Snapshot.Naming.Target.TMDBID != "335790" {
		t.Fatal(first.Job.Snapshot)
	}
	// A duplicate resource still links its original plan even when directory 2
	// has since been cleared. It cannot create another write or new target.
	naming.Target.Title = "新作品"
	raw, _ = animeJSON(naming)
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_subscriptions SET naming_json=?,revision=revision+1 WHERE id=?`, raw, f.sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_settings SET revision=revision+1,organize_cid=NULL,organize_path=NULL`); err != nil {
		t.Fatal(err)
	}
	again := f.claim(t, 0, r, v)
	if again.Created || !reflect.DeepEqual(first.Job, again.Job) {
		t.Fatal("existing job changed")
	}
	r2, v2 := f.selectRelease(t, 0, "rel_dmhy_2", strings.Repeat("b", 40))
	if _, err := f.store.ClaimAnimeDownload(ctx, f.sub.Sources[0].ID, r2.ID, 1, v2, f.target); !errors.Is(err, ErrAnimeDirectoryRequired) {
		t.Fatal("new named task lacks organization target", err)
	}
	assertDownloadCounts(t, f.store, 1, 1, 1, 1)
	if _, err := f.store.db.ExecContext(ctx, `UPDATE anime_settings SET revision=revision+1,organize_cid='901',organize_path='/新整理'`); err != nil {
		t.Fatal(err)
	}
	second := f.claim(t, 0, r2, v2)
	if !second.Created || second.Job.Snapshot.Organization.Target.ParentCID != "901" || second.Job.Snapshot.Naming.Target.Title != "新作品" {
		t.Fatal(second.Job.Snapshot)
	}
	old, err := f.store.GetAnimeDownloadJob(ctx, first.Job.ID)
	if err != nil || !reflect.DeepEqual(old, first.Job) {
		t.Fatal("new plan changed old snapshot", err)
	}
}

func TestAnimeOrganizationDraftSavesBeforeDirectoriesAreReady(t *testing.T) {
	s, _ := testStore(t)
	e := createAnimeEntry(t, s, nil)
	in := animeConfig(e.ID)
	in.Status = "draft"
	in.Naming = organizationNaming("main")
	sub := saveAnimeConfig(t, s, in)
	if sub.Naming == nil || sub.Naming.Sources[0].SourceRef != sub.Sources[0].ID {
		t.Fatal(sub)
	}
}

func TestAnimeOrganizationDirectoryChangePreservesRSSAndResume(t *testing.T) {
	f, now := discoveryFixture(t)
	ctx := context.Background()
	if _, err := f.store.db.Exec(`UPDATE anime_settings SET revision=revision+1,organize_cid='900',organize_path='/整理'`); err != nil {
		t.Fatal(err)
	}
	p := discoveryPoll(t, f.store, now)
	if p.IssueCode != "" {
		t.Fatal("directory 2 blocked unchanged feed", p.IssueCode)
	}
	if _, err := f.store.CompleteAnimeSourcePoll(ctx, p, discoveryPage(p, now, discoveryRow("rel_organize", now)), now); err != nil {
		t.Fatal(err)
	}
	controlAnime(t, f.store, f.sub.ID, "pause")
	controlAnime(t, f.store, f.sub.ID, "resume")
	sub, err := f.store.GetAnimeSubscription(ctx, f.sub.ID, false)
	if err != nil || sub.Status != "enabled" {
		t.Fatal("directory 2 blocked resume", err)
	}
}
