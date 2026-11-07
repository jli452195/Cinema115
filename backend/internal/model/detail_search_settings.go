package model

import (
	"errors"
	"fmt"
	"slices"
)

var ErrDetailSearchSettings = errors.New("详情页搜索顺序无效")

// DetailSearchSettings 只控制手动搜索按钮展示，不参与后台搜索调度。
type DetailSearchSettings struct {
	Order []string `json:"order"`
}

func DefaultDetailSearchOrder() []string {
	return []string{"re0", "bot", "channels", "gying", "mikan", "garden", "woniu"}
}

func (s DetailSearchSettings) Validate() error {
	known := DefaultDetailSearchOrder()
	if len(s.Order) != len(known) {
		return fmt.Errorf("%w：必须包含全部 %d 个搜索来源", ErrDetailSearchSettings, len(known))
	}
	seen := make(map[string]bool, len(known))
	for _, id := range s.Order {
		if !slices.Contains(known, id) || seen[id] {
			return fmt.Errorf("%w：来源未知或重复", ErrDetailSearchSettings)
		}
		seen[id] = true
	}
	return nil
}
