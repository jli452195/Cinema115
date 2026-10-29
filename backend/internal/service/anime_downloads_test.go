package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/anime/preview"
	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
	"strm115/backend/internal/storage"
)

type animeClaimTorrentFunc func(context.Context, string, string, string) ([]byte, error)

func (f animeClaimTorrentFunc) ReadTorrent(ctx context.Context, p, key, raw string) ([]byte, error) {
	return f(ctx, p, key, raw)
}

func animeClaimServiceFixture(t *testing.T) (*animeDownloadFixture, *sql.DB, *AnimeDownloadSession, model.AnimeSource, model.AnimeRelease, animefeed.SourceRelease) {
	t.Helper()
	f := newAnimeDownloadFixture(t)
	ctx := context.Background()
	session := f.prepare(t)
	e, err := f.service.Store.SaveAnimeEntry(ctx, model.AnimeEntry{EditionKey: "original", Title: "动画", Metadata: model.AnimeMetadata{SchemaVersion: 1}})
	if err != nil {
		t.Fatal(err)
	}
	c := model.AnimeSubscriptionConfig{EntryID: e.ID, Status: "paused", Form: model.AnimeForm{SchemaVersion: 1}, Sources: []model.AnimeSourceConfig{{Ref: "main", Role: "main", Provider: "mikan", Status: "paused", RunWhenEnabled: true, ItemID: bindingPtr("2259"), GroupID: bindingPtr("45"), GroupName: bindingPtr("字幕组"), Binding: model.AnimeBinding{SchemaVersion: 1, Confirmed: true, Scope: &model.AnimeScope{Kind: "feed"}, Review: &model.AnimeSourceReview{Method: "source_review", ItemID: "2259", GroupID: "45"}}, Rules: model.AnimeRules{SchemaVersion: 1, MinimumEpisode: bindingPtr(1)}}}}
	receipt, err := f.service.Store.SaveAnimeSubscription(ctx, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := f.service.Store.GetAnimeSubscription(ctx, receipt.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	source := sub.Sources[0]
	row := animefeed.SourceRelease{PostKey: strings.Repeat("a", 40), Title: "[字幕组] 测试 - 01 [1080p]", TorrentURL: "https://mikanani.me/Download/20261019/" + strings.Repeat("a", 40) + ".torrent"}
	parsed, err := releaseparse.Title(row.Title)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := btresource.Fingerprint(row)
	if err != nil {
		t.Fatal(err)
	}
	releases, err := f.service.Store.SaveAnimeReleases(ctx, []model.AnimeRelease{{Provider: "mikan", PostKey: row.PostKey, Title: row.Title, Parse: parsed, ParserVersion: releaseparse.ParserVersion, TimeVersion: preview.TimeVersion, ContentFingerprint: fp, ObservedAt: time.Now().UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	release := releases[0]
	db, err := sql.Open("sqlite", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close anime fixture SQL database: %v", err)
		}
	})

	if _, err := db.ExecContext(ctx, `UPDATE anime_subscription_sources SET status='enabled' WHERE id=?`, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE anime_subscriptions SET status='enabled',first_enabled_at=1 WHERE id=?`, sub.ID); err != nil {
		t.Fatal(err)
	}
	snapshot := model.AnimeDecisionSnapshot{SchemaVersion: 1, Title: release.Title, Binding: source.Binding, Rules: source.Rules, Parse: release.Parse, RuleVersion: preview.RuleVersion, ParserVersion: release.ParserVersion, TimeVersion: release.TimeVersion, Selection: &model.AnimeSelectionBasis{SelectedAt: time.Now().UnixMilli(), Target: session.Target(), SourceGeneration: source.RunGeneration, SubscriptionGeneration: sub.RunGeneration}}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO anime_release_decisions(source_id,release_id,disposition,source_revision,release_fingerprint,snapshot_json,generation,updated_at) VALUES(?,?,'selected',1,?,?,1,1)`, source.ID, release.ID, fp, string(raw)); err != nil {
		t.Fatal(err)
	}
	return f, db, session, source, release, row
}

func animeClaimTestTorrent() []byte {
	return []byte("d4:infod6:lengthi1e4:name1:x12:piece lengthi1e6:pieces20:" + strings.Repeat("p", 20) + "ee")
}

func TestAnimeClaimDownloadVerifiesAndReusesCommittedIdentity(t *testing.T) {
	f, db, session, source, release, row := animeClaimServiceFixture(t)
	reads := 0
	f.service.Torrents = animeClaimTorrentFunc(func(ctx context.Context, p, key, url string) ([]byte, error) {
		reads++
		if p != "mikan" || key != row.PostKey || url != row.TorrentURL {
			t.Fatal("wrong source metadata")
		}
		return animeClaimTestTorrent(), nil
	})
	out, err := f.service.ClaimDownload(context.Background(), session, source.ID, release.ID, 1, row)
	if err != nil || !out.Created || out.Job.ParentCID != "100" || out.Job.Snapshot.InfoHash == row.PostKey {
		t.Fatal("did not claim actual torrent", err)
	}
	second, err := f.service.ClaimDownload(context.Background(), session, source.ID, release.ID, 1, row)
	if err != nil || second.Created || second.Job.ID != out.Job.ID || reads != 1 {
		t.Fatal("repeated verification/submission", reads, err)
	}
	var due, operations int
	if err := db.QueryRow(`SELECT count(*) FROM anime_download_jobs WHERE next_run_at IS NOT NULL`).Scan(&due); err != nil || due != 0 {
		t.Fatal("started scheduler early", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM anime_drive_operations`).Scan(&operations); err != nil || operations != 0 {
		t.Fatal("115 write operations created before execution", err)
	}
}

func TestAnimeClaimDownloadInputAndInactiveSourceFailBeforeNetwork(t *testing.T) {
	for _, mode := range []string{"wrong observation", "P1 explanation", "paused", "dismissed"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, source, release, row := animeClaimServiceFixture(t)
			f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
				t.Fatal("unselected/inactive resource fetched")
				return nil, nil
			})
			var query string
			switch mode {
			case "wrong observation":
				row.Title = "different"
			case "P1 explanation":
				query = `UPDATE anime_release_decisions SET disposition='seen'`
			case "paused":
				query = `UPDATE anime_subscriptions SET status='paused'`
			case "dismissed":
				query = `UPDATE anime_release_decisions SET disposition='dismissed'`
			}
			if query != "" {
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.service.ClaimDownload(context.Background(), session, source.ID, release.ID, 1, row); err == nil {
				t.Fatal("invalid authorization accepted")
			}
		})
	}
}

func TestAnimeClaimDownloadMutationDuringVerificationRollsBack(t *testing.T) {
	for _, mode := range []string{"account", "directory", "source"} {
		t.Run(mode, func(t *testing.T) {
			f, db, session, source, release, row := animeClaimServiceFixture(t)
			f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
				switch mode {
				case "account":
					a, err := f.accounts.Primary("")
					if err != nil {
						t.Fatal(err)
					}
					cookie := "UID=123_A; SEID=updated"
					if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
						t.Fatal(err)
					}
				case "directory":
					f.saveSettings(t, "200")
				case "source":
					if _, err := db.Exec(`UPDATE anime_subscription_sources SET status='paused'`); err != nil {
						t.Fatal(err)
					}
				}
				return animeClaimTestTorrent(), nil
			})
			if _, err := f.service.ClaimDownload(context.Background(), session, source.ID, release.ID, 1, row); err == nil {
				t.Fatal("concurrent mutation authorized job")
			}
			var jobs int
			if err := db.QueryRow(`SELECT count(*) FROM anime_download_jobs`).Scan(&jobs); err != nil || jobs != 0 {
				t.Fatal("mutation left task", jobs, err)
			}
		})
	}
}

func TestAnimeClaimDownloadUpstreamErrorsAreSafe(t *testing.T) {
	f, _, session, source, release, row := animeClaimServiceFixture(t)
	f.service.Torrents = animeClaimTorrentFunc(func(context.Context, string, string, string) ([]byte, error) {
		return nil, errors.New("upstream echoed " + animeDownloadTestCookie)
	})
	_, err := f.service.ClaimDownload(context.Background(), session, source.ID, release.ID, 1, row)
	if !errors.Is(err, btresource.ErrRead) || strings.Contains(err.Error(), "SEID") {
		t.Fatal("unsafe upstream error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.service.ClaimDownload(ctx, session, source.ID, release.ID, 1, row); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.service.ClaimDownload(context.Background(), nil, source.ID, release.ID, 1, row); !errors.Is(err, storage.ErrAnimeInvalid) {
		t.Fatal(err)
	}
}
