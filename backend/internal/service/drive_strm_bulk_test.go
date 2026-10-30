package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
)

type driveSTRMBulkTestClient struct {
	*driveSTRMTestClient
	batches   [][]p115.DriveTreeEntry
	err       error
	bulkCalls int
	bulkCID   string
	cookie    string
}

func (c *driveSTRMBulkTestClient) WalkDriveFiles(ctx context.Context, cid, cookie string, visit func([]p115.DriveTreeEntry) error) error {
	c.bulkCalls++
	c.bulkCID, c.cookie = cid, cookie
	for _, batch := range c.batches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(batch); err != nil {
			return err
		}
	}
	return c.err
}

func driveSTRMBulkDirectory(id, parent, name, relative string) p115.DriveTreeEntry {
	return p115.DriveTreeEntry{ShareFile: p115.ShareFile{ID: id, Name: name, Directory: true}, ParentCID: parent, RelativePath: relative}
}

func driveSTRMBulkVideo(id, parent, name, relative string) p115.DriveTreeEntry {
	return p115.DriveTreeEntry{ShareFile: driveSTRMTestVideo(id, name), ParentCID: parent, RelativePath: relative}
}

func requireNoDriveSTRMDirectoryLists(t *testing.T, client *driveSTRMTestClient) {
	t.Helper()
	for _, call := range client.calls {
		if strings.HasPrefix(call, "list:") {
			t.Fatalf("bulk scan made a per-directory listing: %v", client.calls)
		}
	}
}

func TestDriveSTRMBulkScanUsesBatchesAndPinnedPrimary(t *testing.T) {
	s, db, basic, value, root := driveSTRMTestSetup(t)
	client := &driveSTRMBulkTestClient{driveSTRMTestClient: basic, batches: [][]p115.DriveTreeEntry{
		{driveSTRMBulkDirectory("200", "100", "Season 01", "Season 01"), driveSTRMBulkDirectory("300", "200", "Extras", "Season 01/Extras")},
		{driveSTRMBulkVideo("1", "100", "movie.mkv", "movie.mkv"), driveSTRMBulkVideo("2", "200", "episode.mkv", "Season 01/episode.mkv")},
		{driveSTRMBulkVideo("3", "300", "bonus.mp4", "Season 01/Extras/bonus.mp4")},
	}}
	s.Client = client
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Written != 3 || stats.Directories != 3 || client.bulkCalls != 1 || client.bulkCID != value.SourceCID || client.cookie != "UID=42_A" {
		t.Fatalf("stats=%+v bulk=%d root=%s cookie=%q err=%v", stats, client.bulkCalls, client.bulkCID, client.cookie, err)
	}
	requireNoDriveSTRMDirectoryLists(t, basic)
	data, err := os.ReadFile(filepath.Join(root, "strm", "Season 01", "Extras", "bonus.strm"))
	if err != nil || !strings.Contains(string(data), "/3/") {
		t.Fatalf("nested output=%q err=%v", data, err)
	}
	indexed, err := db.ListDriveSTRMFiles(context.Background(), value.ID)
	if err != nil || len(indexed) != 5 {
		t.Fatalf("index=%+v err=%v", indexed, err)
	}
}

func TestDriveSTRMBulkScanOnlyFallsBackBeforeUnsupportedReaderStarts(t *testing.T) {
	for _, mode := range []string{"unsupported", "network", "partial-unsupported", "filtered-partial-unsupported"} {
		t.Run(mode, func(t *testing.T) {
			s, _, basic, _, root := driveSTRMTestSetup(t)
			basic.files["100"] = []p115.ShareFile{driveSTRMTestVideo("1", "recursive.mkv")}
			client := &driveSTRMBulkTestClient{driveSTRMTestClient: basic, err: p115.ErrDriveTreeUnsupported}
			switch mode {
			case "network":
				client.err = errors.New("temporary network failure")
			case "partial-unsupported":
				client.batches = [][]p115.DriveTreeEntry{{driveSTRMBulkVideo("2", "100", "partial.mkv", "partial.mkv")}}
			case "filtered-partial-unsupported":
				client.batches = [][]p115.DriveTreeEntry{{{ShareFile: p115.ShareFile{ID: "3", Name: "ignored.zip"}, ParentCID: "100", RelativePath: "ignored.zip"}}}
			}
			s.Client = client
			stats, err := driveSTRMTestAttempt(t, s)
			if client.bulkCalls != 1 {
				t.Fatalf("bulk calls=%d", client.bulkCalls)
			}
			if mode == "unsupported" {
				if err != nil || stats.Written != 1 {
					t.Fatalf("fallback stats=%+v err=%v", stats, err)
				}
				if _, err := os.Stat(filepath.Join(root, "strm", "recursive.strm")); err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, client.err) {
				t.Fatalf("bulk failure lost: %v", err)
			}
			requireNoDriveSTRMDirectoryLists(t, basic)
			if _, err := os.Stat(filepath.Join(root, "strm", "recursive.strm")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failure silently retried recursively: %v", err)
			}
			if mode == "partial-unsupported" && stats.Written != 1 {
				t.Fatalf("partial progress lost: %+v", stats)
			}
		})
	}
}

func TestDriveSTRMBulkScanIsolatesSanitizedDirectoryCollisions(t *testing.T) {
	s, db, basic, value, root := driveSTRMTestSetup(t)
	client := &driveSTRMBulkTestClient{driveSTRMTestClient: basic, batches: [][]p115.DriveTreeEntry{
		// The real bulk reader returns parent directories before descendants.
		{driveSTRMBulkDirectory("200", "100", "same*", "same*"), driveSTRMBulkDirectory("300", "100", "same?", "same?")},
		{driveSTRMBulkDirectory("400", "200", "child", "same*/child"), driveSTRMBulkDirectory("500", "300", "foreign-child", "same?/foreign-child")},
		{driveSTRMBulkVideo("1", "100", "root.mkv", "root.mkv"), driveSTRMBulkVideo("2", "200", "accepted.mkv", "same*/accepted.mkv"), driveSTRMBulkVideo("3", "400", "accepted-child.mkv", "same*/child/accepted-child.mkv")},
		{driveSTRMBulkVideo("4", "300", "rejected.mkv", "same?/rejected.mkv"), driveSTRMBulkVideo("5", "500", "rejected-child.mkv", "same?/foreign-child/rejected-child.mkv"), {ShareFile: p115.ShareFile{ID: "6", Name: "rejected.nfo", Size: 3, SHA1: strings.Repeat("B", 40)}, ParentCID: "500", RelativePath: "same?/foreign-child/rejected.nfo"}},
	}}
	basic.metadata["6"] = []byte("nfo")
	s.Client = client
	stats, err := driveSTRMTestAttempt(t, s)
	if err == nil || stats.Failed == 0 || stats.Written != 3 || stats.MetadataWritten != 0 || basic.metadataCalls != 0 {
		t.Fatalf("collision stats=%+v attachment reads=%d err=%v", stats, basic.metadataCalls, err)
	}
	requireNoDriveSTRMDirectoryLists(t, basic)
	for _, relative := range []string{"same_/rejected.strm", "same_/foreign-child/rejected-child.strm", "same_/foreign-child/rejected.nfo"} {
		if _, err := os.Stat(filepath.Join(root, "strm", filepath.FromSlash(relative))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected branch leaked into %s: %v", relative, err)
		}
	}
	indexed, err := db.ListDriveSTRMFiles(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range indexed {
		if file.FileID == "300" || file.FileID == "500" || file.FileID == "4" || file.FileID == "5" || file.FileID == "6" {
			t.Fatalf("rejected source remained indexed: %+v", file)
		}
	}
}
