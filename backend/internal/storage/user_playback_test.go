package storage

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"strm115/backend/internal/model"
)

func TestUserPlaybackSQLiteRestartAndCanceledWrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "playback.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	doc := model.UserPlaybackDocument{
		Generation: 7,
		Leases:     map[string]model.UserPlaybackLease{"key": {Key: "key", ServerID: "server", UserID: "user", SessionDigest: "digest", EventKey: "event", Generation: 7, Issued: true, ExpiresAt: 123456}},
		Stopped:    map[string]int64{"admin-released": 654321},
	}
	if err := db.SaveUserPlayback(ctx, doc); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := db.SaveUserPlayback(canceled, model.UserPlaybackDocument{}); err == nil {
		t.Fatal("canceled write succeeded")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.UserPlayback(ctx)
	if err != nil || !reflect.DeepEqual(got, doc) {
		t.Fatal("playback state lost", got, err)
	}
}
