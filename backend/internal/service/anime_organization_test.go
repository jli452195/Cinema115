package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"strm115/backend/internal/client/p115"
)

func organizationCID(v string) *string { return &v }

func TestAnimeOrganizationDirectoryOneReadAndIdempotentReplay(t *testing.T) {
	f := newAnimeDownloadFixture(t)
	ctx := context.Background()
	session := f.prepare(t)
	reads := 0
	f.service.Directories = animeDownloadDirectoryFunc(func(_ context.Context, cid, cookie string) (p115.DriveDirectory, error) {
		reads++
		if cid != "900" || cookie != animeDownloadTestCookie {
			t.Fatal("unexpected directory or credential")
		}
		return p115.DriveDirectory{CID: cid, Path: "/整理"}, nil
	})
	in := AnimeOrganizeDirectoryInput{ExpectedRevision: f.settings.Revision, CID: organizationCID("900")}
	out, err := f.service.SaveOrganizeDirectory(ctx, "admin", "organization_directory", in)
	if err != nil || reads != 1 || out.Revision != in.ExpectedRevision+1 || *out.DownloadCID != "100" || *out.OrganizeCID != "900" {
		t.Fatal(out, reads, err)
	}
	if err := f.service.Store.CheckAnimeDownloadSettings(ctx, session.Target()); err != nil {
		t.Fatal("directory 2 must not invalidate unchanged directory 1", err)
	}
	again, err := f.service.SaveOrganizeDirectory(ctx, "admin", "organization_directory", in)
	if err != nil || reads != 1 || !reflect.DeepEqual(out, again) {
		t.Fatal("replay made another cloud read", reads, err)
	}
	in.CID = organizationCID("901")
	if _, err := f.service.SaveOrganizeDirectory(ctx, "admin", "organization_directory", in); err == nil || reads != 1 {
		t.Fatal("changed request inherited receipt", err)
	}
	if _, err := f.service.SaveOrganizeDirectory(ctx, "admin", "organization_stale", in); err == nil || reads != 1 {
		t.Fatal("stale CAS read upstream", err)
	}
}

func TestAnimeOrganizationDirectoryFailurePreservesOldSettings(t *testing.T) {
	for _, mode := range []string{"same directory", "read failure", "wrong receipt", "concurrent settings", "account change"} {
		t.Run(mode, func(t *testing.T) {
			f := newAnimeDownloadFixture(t)
			ctx := context.Background()
			in := AnimeOrganizeDirectoryInput{ExpectedRevision: f.settings.Revision, CID: organizationCID("900")}
			reads := 0
			f.service.Directories = animeDownloadDirectoryFunc(func(_ context.Context, cid, cookie string) (p115.DriveDirectory, error) {
				reads++
				switch mode {
				case "read failure":
					return p115.DriveDirectory{}, errors.New("synthetic read failure")
				case "wrong receipt":
					return p115.DriveDirectory{CID: "901", Path: "/错误"}, nil
				case "concurrent settings":
					if _, err := f.service.SaveOrganizeDirectory(ctx, "admin", "concurrent_clear", AnimeOrganizeDirectoryInput{ExpectedRevision: f.settings.Revision}); err != nil {
						t.Fatal(err)
					}
				case "account change":
					a, err := f.accounts.Primary("")
					if err != nil {
						t.Fatal(err)
					}
					cookie := "UID=999_A; SEID=changed"
					if err := f.accounts.Update(a.ID, a.Revision, a.Name, &cookie); err != nil {
						t.Fatal(err)
					}
				}
				return p115.DriveDirectory{CID: cid, Path: "/整理"}, nil
			})
			if mode == "same directory" {
				in.CID = organizationCID("100")
			}
			if _, err := f.service.SaveOrganizeDirectory(ctx, "admin", "organization_failed", in); err == nil {
				t.Fatal("invalid update accepted")
			}
			got, err := f.service.Store.GetAnimeSettings(ctx)
			if err != nil || got.OrganizeCID != nil || *got.DownloadCID != "100" {
				t.Fatal(got, err)
			}
			if mode == "same directory" && reads != 0 || reads > 1 {
				t.Fatal("unnecessary upstream request", reads)
			}
		})
	}
}
