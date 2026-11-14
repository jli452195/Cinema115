package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"strm115/backend/internal/recognition"
)

// 历史整理规格测试显式加载文档中的可选示例；生产初始化始终为空，不读取这些名单。
func TestMain(m *testing.M) {
	directory := filepath.Join("..", "..", "..", "docs", "examples", "recognition")
	groups, err := os.ReadFile(filepath.Join(directory, "release-groups.txt"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取测试字幕组示例:", err)
		os.Exit(1)
	}
	platforms, err := os.ReadFile(filepath.Join(directory, "platforms.txt"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取测试平台示例:", err)
		os.Exit(1)
	}
	snapshot, err := recognition.Compile(string(groups), string(platforms))
	if err == nil {
		err = recognition.Activate(snapshot)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "载入测试识别示例:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
