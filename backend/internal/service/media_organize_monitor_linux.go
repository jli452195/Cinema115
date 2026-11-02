package service

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func mediaLocalFileIdentityKey(_ string, info os.FileInfo) (string, error) {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
	}
	return "", fmt.Errorf("无法读取文件原生身份")
}

func mediaLocalIdentityOf(_ string, info os.FileInfo) (mediaLocalIdentity, error) {
	return mediaLocalIdentity{info: info}, nil
}
func mediaSameLocalIdentity(a, b mediaLocalIdentity) bool {
	return a.info != nil && b.info != nil && os.SameFile(a.info, b.info)
}

func mediaLocalFilesystemReason(path string) string {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return "无法识别挂载类型，实时监听暂不可用"
	}
	switch uint32(stat.Type) {
	case 0x6969, 0x517B, 0xFF534D42, 0xFE534D42, 0x65735546, 0x73757245, 0x564c, 0x01021997:
		return "网络或 FUSE 挂载的远端变更不保证通知，可单独启用定时扫描"
	}
	return ""
}
