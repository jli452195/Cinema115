package service

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows"
)

func mediaLocalDirectoryDrives(ctx context.Context) ([]MediaLocalDirectory, error) {
	drives := make([]MediaLocalDirectory, 0, 26)
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return drives, fmt.Errorf("读取服务器盘符: %w", err)
	}
	for index := 0; index < 26; index++ {
		if err := ctx.Err(); err != nil {
			return drives, fmt.Errorf("读取服务器盘符: %w", err)
		}
		if mask&(1<<uint(index)) == 0 {
			continue
		}
		path := string(rune('A'+index)) + ":\\"
		drives = append(drives, MediaLocalDirectory{Name: path, Path: path})
	}
	return drives, nil
}
