package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// LockInstance must be held for the complete server lifetime. Kernel locks
// disappear on crashes; the file itself must never be unlinked while locked.
func LockInstance(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "server.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockInstanceFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("数据目录已被其他服务占用或无法加锁；动态池只支持单实例: %w", err)
	}
	return f, nil
}
