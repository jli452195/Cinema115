//go:build !linux && !windows

package service

import "os"

func mediaRenameLocalNoReplace(source, target string) error {
	// 没有原生不覆盖改名接口的平台只接受硬链接，不回退为覆盖式改名。
	if err := os.Link(source, target); err != nil {
		return err
	}
	return os.Remove(source)
}
