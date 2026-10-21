package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strm115/backend/internal/client/garden"
	"strm115/backend/internal/client/mikan"
)

type archiveMikanReader struct {
	calls atomic.Int32
}

func (f *archiveMikanReader) Search(context.Context, string, string) (mikan.Result, error) {
	return mikan.Result{}, errors.New("unexpected search")
}
func (f *archiveMikanReader) CatalogSeasons(context.Context) ([]string, error) {
	f.calls.Add(1)
	return []string{"2026-10", "2012-04"}, nil
}

type archiveTransport struct {
	calls atomic.Int32
	body  []byte
	fail  atomic.Bool
}

func (f *archiveTransport) Fetch(ctx context.Context, _ string, _ int64) ([]byte, error) {
	f.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.fail.Load() {
		return nil, garden.ErrInvalid
	}
	return append([]byte{}, f.body...), nil
}

func TestCatalogSeasonsSharedCacheIsolationAndFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("mikan", func(t *testing.T) {
		f := &archiveMikanReader{}
		s := NewMikanService(f, nil)
		first, err := s.AnimeCatalogSeasons(ctx, false)
		if err != nil || len(first.Seasons) != 2 || first.ExpiresAt-first.FetchedAt != time.Hour.Milliseconds() {
			t.Fatal(first, err)
		}
		first.Seasons[0] = "changed"
		again, err := s.AnimeCatalogSeasons(ctx, false)
		if err != nil || again.Seasons[0] != "2026-10" || f.calls.Load() != 1 {
			t.Fatal(again, err)
		}
	})
	t.Run("garden", func(t *testing.T) {
		f := &archiveTransport{body: []byte(`{"ok":true,"data":[{"season":"2022-01","count":45},{"season":"2026-10","count":78}]}`)}
		s := NewGardenService(f)
		s.spacing = 0
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				v, err := s.CatalogSeasons(ctx, false)
				if err != nil || len(v.Seasons) != 2 || v.Seasons[0] != "2026-10" || v.ExpiresAt-v.FetchedAt != time.Hour.Milliseconds() {
					t.Errorf("%+v %v", v, err)
				}
			}()
		}
		wg.Wait()
		if f.calls.Load() != 1 {
			t.Fatal("concurrent index reads must coalesce", f.calls.Load())
		}
		f.fail.Store(true)
		if _, err := s.CatalogSeasons(ctx, true); !errors.Is(err, garden.ErrInvalid) {
			t.Fatal(err)
		}
		again, err := s.CatalogSeasons(ctx, false)
		if err != nil || len(again.Seasons) != 2 || f.calls.Load() != 2 {
			t.Fatal("failed refresh erased cache", again, err)
		}
	})
	t.Run("anibt shares current catalog", func(t *testing.T) {
		body, err := os.ReadFile("../client/anibt/testdata/calendar.json")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().In(animeCatalogTimeZone)
		quarter := (int(now.Month()) - 1) / 3
		season := fmt.Sprintf("%d-%s", now.Year(), []string{"01", "04", "07", "10"}[quarter])
		label := fmt.Sprintf("%d%s", now.Year(), []string{"冬", "春", "夏", "秋"}[quarter])
		f := &archiveTransport{body: []byte(strings.ReplaceAll(string(body), "2026秋", label))}
		s := NewAnibtService(f)
		s.spacing = 0
		metadata, err := s.CatalogSeasons(ctx, false)
		if err != nil || len(metadata.Seasons) == 0 || metadata.ExpiresAt-metadata.FetchedAt != time.Hour.Milliseconds() {
			t.Fatal(metadata, err)
		}
		metadata.Seasons[0] = "changed"
		catalog, err := s.Catalog(ctx, season, "", false)
		if err != nil || len(catalog.Items) != 8 || f.calls.Load() != 1 {
			t.Fatal(catalog, err)
		}
		again, err := s.CatalogSeasons(ctx, false)
		if err != nil || again.Seasons[0] == "changed" || f.calls.Load() != 1 {
			t.Fatal(again, err)
		}
	})
}
