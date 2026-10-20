package service

import (
	"context"
	"strm115/backend/internal/model"
)

// Legacy configurations are retained only as fixtures for lifecycle, parser
// and naming regression tests. Production has no save-unstarted entry point.
func (s *AnimeAdminService) SaveConfiguration(ctx context.Context, actor, key, id string, in AnimeSaveInput) (model.AnimeConfigReceipt, error) {
	return s.saveConfiguration(ctx, actor, key, id, in, nil)
}
