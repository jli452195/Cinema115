package storage

import (
	"context"
	"fmt"
	"reflect"
	"strm115/backend/internal/model"
	"testing"
	"time"
)

func TestAnimeProviderMigrationPreservesReferences(t *testing.T) {
	for _, prior := range []int{42, 43} {
		t.Run(fmt.Sprint(prior), func(t *testing.T) { testAnimeProviderMigration(t, prior) })
	}
}
func testAnimeProviderMigration(t *testing.T, prior int) {
	s, path := testStore(t)
	ctx := context.Background()
	c := previewConfig(t, s)
	sub := saveAnimeConfig(t, s, animeConfig(c.Chapter.ID))
	b := createPreview(t, s, c, previewPage(2, time.Now().UnixMilli()), time.Now().UnixMilli())
	source, release := sub.Sources[0], b.Sources[0].Items[0].Release
	if e := s.SaveAnimeReleaseDecisions(ctx, source.ID, source.Revision, []model.AnimeReleaseDecision{animeDecision(source, release, "seen")}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.ExecContext(ctx, `INSERT INTO anime_source_baselines VALUES('baseline-v42',?,1,1,1,'fingerprint','{}')`, source.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.ExecContext(ctx, `INSERT INTO anime_baseline_releases VALUES('baseline-v42',?)`, release.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.ExecContext(ctx, `UPDATE anime_subscription_sources SET active_baseline_id='baseline-v42' WHERE id=?`, source.ID); e != nil {
		t.Fatal(e)
	}
	sub, e := s.GetAnimeSubscription(ctx, sub.ID, false)
	if e != nil {
		t.Fatal(e)
	}
	// Reproduce the exact old CHECK with real referencing rows, without changing
	// data. This is a disposable fixture database, never an application operation.
	oldCheck := "CHECK(provider='mikan')"
	if prior == 43 {
		oldCheck = "CHECK(provider IN ('mikan','anibt'))"
	}
	if _, e = s.db.ExecContext(ctx, `PRAGMA writable_schema=ON;`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.ExecContext(ctx, `UPDATE sqlite_master SET sql=replace(sql, ?, ?) WHERE name IN ('anime_subscription_sources','anime_releases')`, "CHECK(provider IN ('mikan','anibt','garden'))", oldCheck); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA writable_schema=OFF; PRAGMA user_version=%d;", prior)); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	got, e := reopened.GetAnimeSubscription(ctx, sub.ID, false)
	if e != nil || !reflect.DeepEqual(got, sub) {
		t.Fatal("configuration changed", e)
	}
	preview, e := reopened.GetAnimePreview(ctx, "admin-a", b.ID)
	if e != nil || !reflect.DeepEqual(preview, b) {
		t.Fatal("preview changed", e)
	}
	for _, table := range []string{"anime_source_baselines", "anime_baseline_releases", "anime_release_decisions"} {
		var n int
		if e := reopened.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != 1 {
			t.Fatal("reference lost", table, n, e)
		}
	}
	var fk int
	if e := reopened.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); e != nil || fk != 1 {
		t.Fatal("foreign keys not restored", fk, e)
	}
	if _, e := reopened.db.ExecContext(ctx, `UPDATE anime_subscription_sources SET subscription_id='missing' WHERE id=?`, source.ID); e == nil {
		t.Fatal("FK enforcement disabled")
	}
	next := animeConfig(c.Chapter.ID)
	next.ExpectedRevision = sub.Revision
	next.ID = sub.ID
	next.Sources = []model.AnimeSourceConfig{sourceEdit(source, "main")}
	next.Sources[0].Provider = "anibt"
	if _, e := reopened.SaveAnimeSubscription(ctx, next, nil); e == nil {
		t.Fatal("in-place source identity change accepted")
	}
	r := animeObservation("rel_new")
	r.Provider = "anibt"
	if _, e := reopened.SaveAnimeReleases(ctx, []model.AnimeRelease{r}); e != nil {
		t.Fatal("new provider rejected", e)
	}
	r.Provider = "garden"
	if _, e := reopened.SaveAnimeReleases(ctx, []model.AnimeRelease{r}); e != nil {
		t.Fatal("Garden provider rejected", e)
	}
	r.Provider = "other"
	if _, e := reopened.SaveAnimeReleases(ctx, []model.AnimeRelease{r}); e == nil {
		t.Fatal("unknown provider accepted")
	}
}
