package gying

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// 做种数是辅助信息；未知值用 -1 占位，不能删除元素导致后续磁力错位。
type seedCounts []int

func (counts *seedCounts) UnmarshalJSON(data []byte) error {
	var entries []json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("解析做种数数组: %w", err)
	}
	if entries == nil {
		*counts = nil
		return nil
	}
	values := make(seedCounts, len(entries))
	for i, raw := range entries {
		values[i] = -1
		value := strings.TrimSpace(string(raw))
		if bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				continue
			}
			value = strings.TrimSpace(text)
		}
		// 只接受非负十进制整数，不猜测小数、指数、布尔值或对象的含义。
		if value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			continue
		}
		parsed, err := strconv.Atoi(value)
		if err == nil {
			values[i] = parsed
		}
	}
	*counts = values
	return nil
}
