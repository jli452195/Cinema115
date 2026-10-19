package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/model"
)

func animePtr[T any](v T) *T { return &v }

func createAnimeEntry(t *testing.T, s *Store, id *string) model.AnimeEntry {
	t.Helper()
	e, err := s.SaveAnimeEntry(context.Background(), model.AnimeEntry{BangumiID: id, EditionKey: "original", Title: "测试篇章", OriginalTitle: "原始标题", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func animeMainSource(ref, group string) model.AnimeSourceConfig {
	return model.AnimeSourceConfig{Ref: ref, Role: "main", Status: "paused", RunWhenEnabled: true, Provider: "mikan", ItemID: animePtr("2259"), GroupID: animePtr(group), GroupName: animePtr("字幕组" + group), Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "range", First: animePtr(25), Last: animePtr(36)}, ChapterCorrespondence: &model.AnimeChapterCorrespondence{SourceEpisode: 25, ChapterEpisode: 1}}, Rules: model.AnimeRules{SchemaVersion: 1, Resolution: animePtr("1080p"), MinimumEpisode: animePtr(30), InitialMode: "from_episode", Include: []string{"HEVC"}, Exclude: []string{}}}
}

func animeConfig(entryID string) model.AnimeSubscriptionConfig {
	return model.AnimeSubscriptionConfig{EntryID: entryID, Status: "paused", Form: model.AnimeForm{SchemaVersion: 1, FormSelections: []model.AnimeFormSelection{}}, Sources: []model.AnimeSourceConfig{animeMainSource("main", "45")}}
}

func saveAnimeConfig(t *testing.T, s *Store, c model.AnimeSubscriptionConfig) model.AnimeSubscription {
	t.Helper()
	r, err := s.SaveAnimeSubscription(context.Background(), c, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAnimeSubscription(context.Background(), r.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func sourceEdit(s model.AnimeSource, ref string) model.AnimeSourceConfig {
	return model.AnimeSourceConfig{ID: s.ID, Ref: ref, ExpectedRevision: s.Revision, Role: s.Role, Position: s.Position, Status: s.Status, RunWhenEnabled: s.RunWhenEnabled, Provider: s.Provider, ItemID: s.ItemID, GroupID: s.GroupID, GroupName: s.GroupName, Binding: s.Binding, Rules: s.Rules}
}

func TestAnimeSchemaUpgradeAndReopen(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	resetAnimeSchemaToV38(t, s)
	if err := s.SaveReshareJob(ctx, sampleJob("before-anime", "completed")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := upgraded.GetAnimeSettings(ctx)
	if err != nil || settings.Revision != 1 || settings.AccountID != nil || settings.UID != nil {
		t.Fatalf("unexpected settings: %+v %v", settings, err)
	}
	e := createAnimeEntry(t, upgraded, animePtr("123"))
	sub := saveAnimeConfig(t, upgraded, animeConfig(e.ID))
	upgraded.Close()
	for i := 0; i < 2; i++ {
		reopened, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		var version int
		if err := reopened.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
			t.Fatalf("version regressed: %d %v", version, err)
		}
		got, err := reopened.GetAnimeSubscription(ctx, sub.ID, false)
		if err != nil || !reflect.DeepEqual(sub, got) {
			t.Fatalf("persistence: %+v %v", got, err)
		}
		job, err := reopened.GetReshareJob(ctx, "before-anime")
		if err != nil || job.Status != "completed" {
			t.Fatal("existing data lost", err)
		}
		var count int
		if err := reopened.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('anime_download_jobs','anime_resources','anime_naming_plans')`).Scan(&count); err != nil || count != 0 {
			t.Fatal("later-phase tables created", count, err)
		}
		if err := reopened.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('anime_releases') WHERE name='resource_id'`).Scan(&count); err != nil || count != 0 {
			t.Fatal("dangling P2 column", err)
		}
		if err := reopened.db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('anime_subscriptions') WHERE name IN ('naming_plan_id','organize_enabled')`).Scan(&count); err != nil || count != 0 {
			t.Fatal("dangling P3 columns", err)
		}
		rows, err := reopened.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Fatal("foreign key violation")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		reopened.Close()
	}
}

func TestAnimeNamingSnapshotReopenAndAtomicReferenceValidation(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	e := createAnimeEntry(t, s, animePtr("234"))
	in := animeConfig(e.ID)
	in.Naming = &model.AnimeNamingPlan{SchemaVersion: 1, Target: model.AnimeNamingTarget{TMDBID: "117061", Title: "作品", Year: "2021", Season: 2, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 2, Episodes: []model.AnimeNamingEpisode{{ID: "201", Number: 1, Name: "开始"}}}}}, Sources: []model.AnimeSourceMapping{{SourceRef: "unknown", Segments: []model.AnimeMappingSegment{{First: 25, SourceBase: 25, TargetSeason: 2, TargetBase: 1}}}}}
	if _, err := s.SaveAnimeSubscription(ctx, in, nil); !errors.Is(err, ErrAnimeInvalid) {
		t.Fatal("unknown naming source accepted", err)
	}
	page, err := s.ListAnimeSubscriptions(ctx, AnimeSubscriptionFilter{})
	if err != nil || page.Total != 0 {
		t.Fatal("failed naming save left a partial subscription", page, err)
	}
	in.Naming.Sources[0].SourceRef = "main"
	saved := saveAnimeConfig(t, s, in)
	if saved.Naming.Sources[0].SourceRef != saved.Sources[0].ID || in.Naming.Sources[0].SourceRef != "main" {
		t.Fatal("unstable naming source reference", saved)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetAnimeSubscription(ctx, saved.ID, false)
	if err != nil || !reflect.DeepEqual(saved, got) {
		t.Fatal("naming snapshot changed after reopen", got, err)
	}
}

func resetAnimeSchemaToV38(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	var tables []string
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'anime_%'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(tables) != 12 {
		t.Fatalf("P1 table count: %d", len(tables))
	}
	// Build a v38 fixture with the existing business schema and no anime tables.
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if _, err := s.db.ExecContext(ctx, `DROP TABLE `+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys=ON; PRAGMA user_version=38`); err != nil {
		t.Fatal(err)
	}
}

func TestAnimeEntryIdentityAndLocalHistory(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	e := createAnimeEntry(t, s, animePtr("123"))
	duplicate := e
	duplicate.ID = ""
	duplicate.Revision = 0
	_, err := s.SaveAnimeEntry(ctx, duplicate)
	var exists *AnimeExistsError
	if !errors.As(err, &exists) || exists.ID != e.ID {
		t.Fatal("missing existing chapter", err)
	}
	unknown := createAnimeEntry(t, s, nil)
	unknown.BangumiID = animePtr("456")
	confirmed, err := s.SaveAnimeEntry(ctx, unknown)
	if err != nil {
		t.Fatal("confirm local entry", err)
	}
	confirmed.BangumiID = animePtr("789")
	if _, err := s.SaveAnimeEntry(ctx, confirmed); !errors.Is(err, ErrAnimeIdentity) {
		t.Fatal("rebound chapter", err)
	}
	e.Title = "新标题"
	updated, err := s.SaveAnimeEntry(ctx, e)
	if err != nil || updated.Revision != 2 || updated.CreatedAt != e.CreatedAt {
		t.Fatal(updated, err)
	}
	if _, err := s.SaveAnimeEntry(ctx, e); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("stale entry accepted", err)
	}
	got, err := s.FindAnimeEntry(ctx, "123", "original")
	if err != nil || got.Title != "新标题" {
		t.Fatal(got, err)
	}
	// There is no foreign metadata cache table whose disappearance can cascade.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM anime_entries WHERE id=?`, updated.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAnimeEntry(ctx, updated.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestAnimeConfigIndependentSourcesAndFormOnlySelections(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	e := createAnimeEntry(t, s, animePtr("123"))
	c := animeConfig(e.ID)
	c.Sources = append(c.Sources, model.AnimeSourceConfig{Ref: "backup", Role: "standby", Position: 1, Status: "draft", Provider: "mikan", Binding: model.AnimeBinding{SchemaVersion: 1}, Rules: model.AnimeRules{SchemaVersion: 1}})
	c.Form.FormSelections = []model.AnimeFormSelection{{SourceRef: "main", SelectedReleaseIDs: []string{"pending-release"}}}
	sub := saveAnimeConfig(t, s, c)
	if c.Form.FormSelections[0].SourceRef != "main" {
		t.Fatal("save mutated caller form")
	}
	if sub.Status != "paused" || sub.FirstEnabledAt != nil || len(sub.Sources) != 2 {
		t.Fatal(sub)
	}
	main, backup := sub.Sources[0], sub.Sources[1]
	if main.SourceKey == nil || *main.SourceKey != "mikan:2259:45" || main.NamespaceID == backup.NamespaceID || *main.Rules.MinimumEpisode != 30 || main.Binding.ChapterCorrespondence.SourceEpisode != 25 || backup.Rules.MinimumEpisode != nil {
		t.Fatal("source identity/rules mixed", sub)
	}
	if sub.Form.FormSelections[0].SourceRef != main.ID {
		t.Fatal("temporary ref not resolved")
	}
	for _, table := range []string{"anime_source_baselines", "anime_baseline_releases", "anime_release_decisions"} {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatal("form selection authorized work", table, count, err)
		}
	}
	_, err := s.SaveAnimeSubscription(ctx, c, nil)
	var exists *AnimeExistsError
	if !errors.As(err, &exists) || exists.ID != sub.ID {
		t.Fatal("duplicate active subscription", err)
	}
	// Omission keeps backup, editing main never borrows its rules or clears it.
	edit := model.AnimeSubscriptionConfig{ID: sub.ID, EntryID: e.ID, ExpectedRevision: sub.Revision, Status: "paused", Form: sub.Form, Sources: []model.AnimeSourceConfig{sourceEdit(main, "main")}}
	edit.Sources[0].Rules.MinimumEpisode = animePtr(31)
	updated := saveAnimeConfig(t, s, edit)
	if len(updated.Sources) != 2 || updated.Sources[1].Revision != backup.Revision || updated.Sources[1].RunWhenEnabled != backup.RunWhenEnabled || updated.Sources[0].Revision != main.Revision+1 {
		t.Fatal(updated)
	}
	unchanged := sourceEdit(updated.Sources[0], "main")
	edit.ExpectedRevision = updated.Revision
	edit.Sources = []model.AnimeSourceConfig{unchanged}
	noOp := saveAnimeConfig(t, s, edit)
	if noOp.Sources[0].Revision != updated.Sources[0].Revision || noOp.Sources[0].RunGeneration != updated.Sources[0].RunGeneration {
		t.Fatal("unchanged source revision advanced")
	}
	page, err := s.ListAnimeSubscriptions(ctx, AnimeSubscriptionFilter{Keyword: "测试", PageSize: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	empty, err := s.ListAnimeSubscriptions(ctx, AnimeSubscriptionFilter{Keyword: "不存在"})
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal(empty, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM anime_entries WHERE id=?`, e.ID); err == nil {
		t.Fatal("entry deletion erased business history")
	}
}

func TestAnimeConfigCASAndIdempotency(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	e := createAnimeEntry(t, s, nil)
	c := animeConfig(e.ID)
	key := &AnimeWriteKey{ActorKey: "admin", Operation: "POST /subscriptions", Key: "request_001", Digest: strings.Repeat("a", 64)}
	first, err := s.SaveAnimeSubscription(ctx, c, key)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveAnimeSubscription(ctx, c, key)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("replay failed", second, err)
	}
	changedKey := *key
	changedKey.Digest = strings.Repeat("b", 64)
	if _, err := s.SaveAnimeSubscription(ctx, c, &changedKey); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("changed key body accepted", err)
	}
	c.ID, c.ExpectedRevision, c.Sources = first.ID, first.Revision, nil
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.SaveAnimeSubscription(ctx, c, nil); results <- err }()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrAnimeConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS winners=%d conflicts=%d", success, conflict)
	}
	replay, err := s.SaveAnimeSubscription(ctx, animeConfig(e.ID), key)
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatal("replay was checked against current revision", err)
	}
}

func TestAnimeConfigRollbackIdentityAndSourceLimit(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	e := createAnimeEntry(t, s, nil)
	c := animeConfig(e.ID)
	for i := 1; i < 5; i++ {
		standby := animeMainSource(fmt.Sprintf("backup%d", i), fmt.Sprint(i))
		standby.Role = "standby"
		standby.Position = i
		c.Sources = append(c.Sources, standby)
	}
	sub := saveAnimeConfig(t, s, c)
	edit := model.AnimeSubscriptionConfig{ID: sub.ID, EntryID: e.ID, ExpectedRevision: 1, Status: "paused", Form: sub.Form}
	for _, tc := range []struct {
		name   string
		change func(*model.AnimeSubscriptionConfig)
		want   error
	}{
		{"max-five-across-existing", func(c *model.AnimeSubscriptionConfig) {
			extra := animeMainSource("extra", "99")
			extra.Role = "standby"
			c.Sources = []model.AnimeSourceConfig{extra}
		}, ErrAnimeInvalid},
		{"identity-rebind", func(c *model.AnimeSubscriptionConfig) {
			x := sourceEdit(sub.Sources[0], "main")
			x.GroupID = animePtr("99")
			c.Sources = []model.AnimeSourceConfig{x}
		}, ErrAnimeIdentity},
		{"stale-source", func(c *model.AnimeSubscriptionConfig) {
			x := sourceEdit(sub.Sources[0], "main")
			x.ExpectedRevision = 2
			c.Sources = []model.AnimeSourceConfig{x}
		}, ErrAnimeConflict},
		{"remove-main", func(c *model.AnimeSubscriptionConfig) {
			c.RemovedSources = []model.AnimeSourceRemoval{{ID: sub.Sources[0].ID, ExpectedRevision: 1}}
		}, ErrAnimeInvalid},
		{"enable", func(c *model.AnimeSubscriptionConfig) { c.Status = "enabled" }, ErrAnimeCapability},
		{"enable-source", func(c *model.AnimeSubscriptionConfig) {
			x := sourceEdit(sub.Sources[0], "main")
			x.Status = "enabled"
			c.Sources = []model.AnimeSourceConfig{x}
		}, ErrAnimeCapability},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := edit
			tc.change(&request)
			if _, err := s.SaveAnimeSubscription(ctx, request, nil); !errors.Is(err, tc.want) {
				t.Fatal("wrong error", err)
			}
			got, err := s.GetAnimeSubscription(ctx, sub.ID, false)
			if err != nil || !reflect.DeepEqual(got, sub) {
				t.Fatal("partial write", err)
			}
		})
	}
	// Force failure after parent/source writes, at receipt insertion. Everything
	// including removals and revision increments must roll back together.
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER anime_test_receipt_failure BEFORE INSERT ON anime_request_keys BEGIN SELECT RAISE(ABORT,'simulated write failure'); END`); err != nil {
		t.Fatal(err)
	}
	edit.RemovedSources = []model.AnimeSourceRemoval{{ID: sub.Sources[4].ID, ExpectedRevision: 1}}
	edit.Sources = []model.AnimeSourceConfig{sourceEdit(sub.Sources[0], "main")}
	edit.Sources[0].Rules.MinimumEpisode = animePtr(32)
	key := &AnimeWriteKey{ActorKey: "admin", Operation: "PATCH /subscriptions/" + sub.ID, Key: "rollback_001", Digest: strings.Repeat("a", 64)}
	if _, err := s.SaveAnimeSubscription(ctx, edit, key); err == nil {
		t.Fatal("forced write succeeded")
	}
	got, err := s.GetAnimeSubscription(ctx, sub.ID, false)
	if err != nil || !reflect.DeepEqual(got, sub) {
		t.Fatal("failed transaction leaked writes", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM anime_request_keys`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed receipt committed", err)
	}
}

func animeObservation(post string) model.AnimeRelease {
	return model.AnimeRelease{Provider: "mikan", PostKey: post, OriginalGUID: animePtr("shared-guid"), Title: "[字幕组] 测试 第30话 1080p", TimeVersion: 1, ParserVersion: 1, Parse: model.AnimeReleaseParse{SchemaVersion: 1, Kind: "episode", RawEpisode: "30", Episode: animePtr(30), Resolution: animePtr("1080p")}, ContentFingerprint: "fingerprint-" + post, ObservedAt: time.Now().UTC().UnixMilli()}
}

func animeDecision(source model.AnimeSource, release model.AnimeRelease, disposition string) model.AnimeReleaseDecision {
	return model.AnimeReleaseDecision{SourceID: source.ID, SourceRevision: source.Revision, ReleaseID: release.ID, Disposition: disposition, ReleaseFingerprint: release.ContentFingerprint, Snapshot: model.AnimeDecisionSnapshot{SchemaVersion: 1, Title: release.Title, Binding: source.Binding, Rules: source.Rules, Parse: release.Parse}}
}

func TestAnimeReleaseVersionsDismissalAndSourceReplacement(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	e := createAnimeEntry(t, s, nil)
	c := animeConfig(e.ID)
	backup := animeMainSource("backup", "46")
	backup.Role = "standby"
	backup.Position = 1
	backup.Rules.MinimumEpisode = animePtr(25)
	c.Sources = append(c.Sources, backup)
	sub := saveAnimeConfig(t, s, c)
	main, standby := sub.Sources[0], sub.Sources[1]
	releases, err := s.SaveAnimeReleases(ctx, []model.AnimeRelease{animeObservation("postA"), animeObservation("postB"), animeObservation("postV2")})
	if err != nil || len(releases) != 3 || releases[0].ID == releases[1].ID {
		t.Fatal("GUID/episode collapsed releases", err)
	}
	for _, source := range []model.AnimeSource{main, standby} {
		decisions := make([]model.AnimeReleaseDecision, 0, len(releases))
		for _, r := range releases {
			decisions = append(decisions, animeDecision(source, r, "seen"))
		}
		if err := s.SaveAnimeReleaseDecisions(ctx, source.ID, source.Revision, decisions); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveAnimeReleaseDecisions(ctx, main.ID, 1, []model.AnimeReleaseDecision{animeDecision(main, releases[0], "dismissed")}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAnimeReleaseDecisions(ctx, main.ID, 1, []model.AnimeReleaseDecision{animeDecision(main, releases[0], "filtered")}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListAnimeReleases(ctx, AnimeReleaseFilter{SourceID: main.ID, Disposition: "dismissed"})
	if err != nil || page.Total != 1 || page.Items[0].Release.ID != releases[0].ID {
		t.Fatal("dismissal overwritten", page, err)
	}
	if err := s.SaveAnimeReleaseDecisions(ctx, main.ID, 1, []model.AnimeReleaseDecision{animeDecision(main, releases[0], "selected")}); !errors.Is(err, ErrAnimeCapability) {
		t.Fatal("P1 authorized download", err)
	}
	// Changing main/standby in reversed input order must not hit main uniqueness.
	edit := model.AnimeSubscriptionConfig{ID: sub.ID, EntryID: e.ID, ExpectedRevision: 1, Status: "paused", Form: sub.Form, Sources: []model.AnimeSourceConfig{sourceEdit(standby, "new-main"), sourceEdit(main, "old-main")}}
	edit.Sources[0].Role = "main"
	edit.Sources[1].Role = "standby"
	sub = saveAnimeConfig(t, s, edit)
	// Remove/re-add the same source identity: old namespace and decisions remain.
	replacement := animeMainSource("replacement", "45")
	replacement.Role = "standby"
	edit.ExpectedRevision = sub.Revision
	edit.Sources = []model.AnimeSourceConfig{replacement}
	edit.RemovedSources = []model.AnimeSourceRemoval{{ID: main.ID, ExpectedRevision: 2}}
	sub = saveAnimeConfig(t, s, edit)
	all, err := s.GetAnimeSubscription(ctx, sub.ID, true)
	if err != nil || len(all.Sources) != 3 {
		t.Fatal(all, err)
	}
	var old, newSource model.AnimeSource
	for _, source := range all.Sources {
		if source.ID == main.ID {
			old = source
		}
		if source.GroupID != nil && *source.GroupID == "45" && source.Status != "removed" {
			newSource = source
		}
	}
	if old.Status != "removed" || newSource.ID == main.ID || newSource.NamespaceID == main.NamespaceID {
		t.Fatal("source history reused", all)
	}
	page, err = s.ListAnimeReleases(ctx, AnimeReleaseFilter{SourceID: main.ID})
	if err != nil || page.Total != 3 {
		t.Fatal("removed source lost history", err)
	}
	page, err = s.ListAnimeReleases(ctx, AnimeReleaseFilter{SourceID: newSource.ID})
	if err != nil || page.Total != 0 || page.Items == nil {
		t.Fatal("new source inherited old history", err)
	}
}

func TestAnimeReleaseObservationAndDecisionRollback(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	e := createAnimeEntry(t, s, nil)
	sub := saveAnimeConfig(t, s, animeConfig(e.ID))
	source := sub.Sources[0]
	original := animeObservation("postA")
	first, err := s.SaveAnimeReleases(ctx, []model.AnimeRelease{original})
	if err != nil {
		t.Fatal(err)
	}
	stale := original
	stale.ObservedAt--
	stale.Title = "stale"
	again, err := s.SaveAnimeReleases(ctx, []model.AnimeRelease{stale})
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("stale observation overwrote release", err)
	}
	newer := original
	newer.ObservedAt++
	newer.Title = "updated"
	newer.ContentFingerprint = "new-fingerprint"
	updated, err := s.SaveAnimeReleases(ctx, []model.AnimeRelease{newer})
	if err != nil || updated[0].ID != first[0].ID {
		t.Fatal("same post gained new identity", err)
	}
	if err := s.SaveAnimeReleaseDecisions(ctx, source.ID, 1, []model.AnimeReleaseDecision{animeDecision(source, first[0], "seen")}); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("stale fingerprint accepted", err)
	}
	valid := animeDecision(source, updated[0], "seen")
	bad := valid
	bad.ReleaseID = "missing"
	if err := s.SaveAnimeReleaseDecisions(ctx, source.ID, 1, []model.AnimeReleaseDecision{valid, bad}); !errors.Is(err, ErrAnimeInvalid) {
		t.Fatal(err)
	}
	page, err := s.ListAnimeReleases(ctx, AnimeReleaseFilter{SourceID: source.ID})
	if err != nil || page.Total != 0 {
		t.Fatal("decision transaction partially saved", err)
	}
	other := createAnimeEntry(t, s, nil)
	otherSub := saveAnimeConfig(t, s, animeConfig(other.ID))
	if err := s.SaveAnimeReleaseDecisions(ctx, otherSub.Sources[0].ID, 1, []model.AnimeReleaseDecision{valid}); !errors.Is(err, ErrAnimeInvalid) {
		t.Fatal("cross-source decision accepted", err)
	}
	crossSource := model.AnimeSubscriptionConfig{ID: sub.ID, EntryID: e.ID, ExpectedRevision: 1, Status: "paused", Form: sub.Form, Sources: []model.AnimeSourceConfig{sourceEdit(otherSub.Sources[0], "foreign")}}
	if _, err := s.SaveAnimeSubscription(ctx, crossSource, nil); !errors.Is(err, ErrAnimeConflict) {
		t.Fatal("cross-subscription source accepted", err)
	}
	forged := original
	forged.ID = "forged-release"
	if _, err := s.SaveAnimeReleases(ctx, []model.AnimeRelease{animeObservation("rolled-back"), forged}); !errors.Is(err, ErrAnimeIdentity) {
		t.Fatal("release identity accepted", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM anime_releases WHERE post_key='rolled-back'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("release batch partially committed", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.SaveAnimeReleases(cancelled, []model.AnimeRelease{original}); !errors.Is(err, context.Canceled) {
		t.Fatal("context cancellation lost", err)
	}
}

func TestAnimeStructuredRulesPersistWithoutRevisionChurn(t *testing.T) {
	s, path := testStore(t)
	ctx := context.Background()
	entry := createAnimeEntry(t, s, animePtr("123"))
	c := animeConfig(entry.ID)
	c.Sources[0].Binding.Scope = &model.AnimeScope{Kind: "confirmed_chapter"}
	c.Sources[0].Binding.Review = &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}
	c.Sources[0].Rules.Resolution = animePtr("any")
	c.Sources[0].Rules.SpecMode = "profiles"
	c.Sources[0].Rules.SpecProfiles = []model.AnimeSpec{{Resolution: "1080p", Languages: []string{"ja", "zh-Hans"}, SubtitleMode: "hard"}, {Resolution: "1080p", Languages: []string{"zh-Hant"}, SubtitleMode: "hard"}}
	saved := saveAnimeConfig(t, s, c)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.GetAnimeSubscription(ctx, saved.ID, false)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatal("structured rules changed on reopen", err)
	}
	edit := model.AnimeSubscriptionConfig{ID: got.ID, EntryID: entry.ID, ExpectedRevision: got.Revision, Status: got.Status, Form: got.Form, Sources: []model.AnimeSourceConfig{sourceEdit(got.Sources[0], "main")}}
	again := saveAnimeConfig(t, reopened, edit)
	if again.Sources[0].Revision != got.Sources[0].Revision {
		t.Fatal("unchanged structured rules bumped source revision")
	}
}
