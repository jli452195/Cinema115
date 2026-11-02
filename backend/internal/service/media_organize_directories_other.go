//go:build !windows

package service

import "context"

func mediaLocalDirectoryDrives(context.Context) ([]MediaLocalDirectory, error) {
	return nil, nil
}
