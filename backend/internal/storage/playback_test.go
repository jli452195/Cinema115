package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPlaybackTransactionRollbackAndChildDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m := PlaybackMaterialRow{ID: "m", SHA1: "hash", Size: 100, Record: "metadata", Origins: []string{"origin"}, Ranges: []PlaybackRangeRow{{Start: 1, End: 3, Record: "range"}}}
	if err := db.SavePlayback(ctx, PlaybackBatch{Materials: []PlaybackMaterialRow{m}, Copies: []PlaybackCopyRow{{ID: "copy", AccountID: "account", SHA1: "hash", Record: "copy-data"}}}); err != nil {
		t.Fatal(err)
	}
	bad := m
	bad.ID = "bad"
	bad.Size = 0
	if err := db.SavePlayback(ctx, PlaybackBatch{DeleteMaterials: []string{"m"}, Materials: []PlaybackMaterialRow{bad}}); err == nil {
		t.Fatal("invalid transaction accepted")
	}
	rows, err := db.LoadPlayback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Materials) != 1 || len(rows.Materials[0].Origins) != 1 || len(rows.Materials[0].Ranges) != 1 {
		t.Fatal("failed transaction removed data")
	}
	if err := db.SavePlayback(ctx, PlaybackBatch{DeleteMaterials: []string{"m"}}); err != nil {
		t.Fatal(err)
	}
	rows, err = db.LoadPlayback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Materials) != 0 || len(rows.Copies) != 1 {
		t.Fatal("material removal deleted copies")
	}
	for _, table := range []string{"playback_origins", "playback_ranges"} {
		var n int
		if err := db.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal("orphaned child rows", err)
		}
	}
	var mode string
	if err := db.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatal("WAL disabled", err)
	}
}
