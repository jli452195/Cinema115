package service

import (
	"errors"

	"golang.org/x/sys/unix"
)

func mediaRenameLocalNoReplace(source, target string) error {
	err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
	if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.EOPNOTSUPP) {
		return err
	}
	// 旧内核或挂载不支持不覆盖改名时，仅用硬链接发布完整文件。
	if err := unix.Link(source, target); err != nil {
		return err
	}
	return unix.Unlink(source)
}
