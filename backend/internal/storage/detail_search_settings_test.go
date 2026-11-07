package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"strm115/backend/internal/model"
)

func TestDetailSearchSettingsReopenAndCancelledSave(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "search.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	order := model.DetailSearchSettings{Order: []string{"woniu", "garden", "gying", "bot", "mikan", "channels", "re0"}}
	if _, err := db.SaveDetailSearchSettings(ctx, order); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.DetailSearchSettings(ctx)
	if err != nil || !reflect.DeepEqual(got, order) {
		t.Fatalf("重新打开数据库后排序丢失: %+v %v", got, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.SaveDetailSearchSettings(cancelled, model.DetailSearchSettings{Order: model.DefaultDetailSearchOrder()}); !errors.Is(err, context.Canceled) {
		t.Fatal("已取消的保存没有返回取消错误", err)
	}
	got, err = db.DetailSearchSettings(ctx)
	if err != nil || !reflect.DeepEqual(got, order) {
		t.Fatal("已取消的保存改变了原排序", err)
	}
}
