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
	order := model.DetailSearchSettings{Order: []string{"woniu", "garden", "dmhy", "gying", "bot", "mikan", "channels", "re0"}}
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

func TestDetailSearchSettingsIncludeNewSourcesWithoutChangingSavedOrder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.db.ExecContext(ctx, `INSERT INTO detail_search_settings(id,order_json) VALUES(1,?)`, `["woniu","garden","gying","bot","mikan","channels","re0"]`); err != nil {
		t.Fatal(err)
	}
	settings, err := db.DetailSearchSettings(ctx)
	want := []string{"woniu", "garden", "gying", "bot", "mikan", "channels", "re0", "dmhy"}
	if err != nil || !reflect.DeepEqual(settings.Order, want) {
		t.Fatalf("新来源没有补齐或覆盖了原排序: %v %v", settings.Order, err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE detail_search_settings SET order_json=? WHERE id=1`, `["garden","garden"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DetailSearchSettings(ctx); !errors.Is(err, model.ErrDetailSearchSettings) {
		t.Fatalf("重复来源不应被自动补齐掩盖: %v", err)
	}
}
