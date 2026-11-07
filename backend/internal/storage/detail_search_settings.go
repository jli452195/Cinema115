package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"strm115/backend/internal/model"
)

func (s *Store) DetailSearchSettings(ctx context.Context) (model.DetailSearchSettings, error) {
	settings := model.DetailSearchSettings{Order: model.DefaultDetailSearchOrder()}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT order_json FROM detail_search_settings WHERE id=1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("读取详情页搜索顺序: %w", err)
	}
	if err := json.Unmarshal([]byte(raw), &settings.Order); err != nil {
		return settings, fmt.Errorf("解码详情页搜索顺序: %w", err)
	}
	if err := settings.Validate(); err != nil {
		return settings, fmt.Errorf("读取已保存的详情页搜索顺序: %w", err)
	}
	return settings, nil
}

func (s *Store) SaveDetailSearchSettings(ctx context.Context, settings model.DetailSearchSettings) (model.DetailSearchSettings, error) {
	if err := settings.Validate(); err != nil {
		return model.DetailSearchSettings{}, err
	}
	raw, err := json.Marshal(settings.Order)
	if err != nil {
		return model.DetailSearchSettings{}, fmt.Errorf("编码详情页搜索顺序: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO detail_search_settings(id,order_json) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET order_json=excluded.order_json`, string(raw)); err != nil {
		return model.DetailSearchSettings{}, fmt.Errorf("保存详情页搜索顺序: %w", err)
	}
	return settings, nil
}
