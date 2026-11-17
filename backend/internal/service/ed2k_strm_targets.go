package service

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

func (s *ED2KSTRMService) Targets() []model.ED2KSTRMTarget {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.ED2KSTRMTarget, 0, len(s.targets))
	for _, v := range s.targets {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *ED2KSTRMService) Target(id string) (model.ED2KSTRMTarget, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.targets[id]
	return v, ok
}

func (s *ED2KSTRMService) SaveTarget(ctx context.Context, v model.ED2KSTRMTarget) (model.ED2KSTRMTarget, error) {
	v.Name, v.OutputDir = strings.TrimSpace(v.Name), strings.TrimSpace(v.OutputDir)
	if !utf8.ValidString(v.Name) || v.Name == "" || len([]rune(v.Name)) > 80 || strings.IndexFunc(v.Name, unicode.IsControl) >= 0 {
		return v, generationInputError("目录名称应为 1 到 80 个字符")
	}
	output, err := ResolveOutputDirectory(s.Root, v.OutputDir)
	if err != nil {
		return v, err
	}
	if v.OutputDir == "." {
		return v, generationInputError("请为 ED2K STRM 设置独立的相对输出目录")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if reader, ok := s.Store.(interface {
		ListDriveSTRM(context.Context) ([]model.DriveSTRM, error)
	}); ok {
		mappings, err := reader.ListDriveSTRM(ctx)
		if err != nil {
			return v, err
		}
		for _, m := range mappings {
			other, err := ResolveOutputDirectory(s.Root, m.OutputDir)
			if err != nil {
				return v, err
			}
			if mediaWithin(output, other) || mediaWithin(other, output) {
				return v, generationInputError("ED2K STRM 输出目录不能与网盘目录 STRM 重叠")
			}
		}
	}
	for _, other := range s.targets {
		if other.ID != v.ID && strings.EqualFold(other.OutputDir, v.OutputDir) {
			return v, generationInputError("该输出目录已配置，请直接使用已有目录")
		}
	}
	if v.ID != "" {
		old, ok := s.targets[v.ID]
		if !ok {
			return v, storage.ErrNotFound
		}
		v.AssignedBotID = old.AssignedBotID
	} else {
		v.ID, err = organizeToken()
		if err != nil {
			return v, err
		}
		v.AssignedBotID = ""
	}
	if err := s.Store.SaveED2KSTRMTarget(ctx, v); err != nil {
		return v, err
	}
	s.targets[v.ID] = v
	return v, nil
}

func (s *ED2KSTRMService) DeleteTarget(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.targets[id]; !ok {
		return storage.ErrNotFound
	}
	if err := s.Store.DeleteED2KSTRMTarget(ctx, id); err != nil {
		return err
	}
	delete(s.targets, id)
	return nil
}

func (s *ED2KSTRMService) AssignTarget(ctx context.Context, id, botID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.targets[id]
	if !ok {
		return storage.ErrNotFound
	}
	v.AssignedBotID = botID
	if err := s.Store.SaveED2KSTRMTarget(ctx, v); err != nil {
		return err
	}
	s.targets[id] = v
	return nil
}
