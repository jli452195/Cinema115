package service

import (
	"os"

	"golang.org/x/sys/unix"
)

func mediaLocalIdentityOf(_ string, info os.FileInfo) (mediaLocalIdentity, error) {
	return mediaLocalIdentity{info: info}, nil
}
func mediaSameLocalIdentity(a, b mediaLocalIdentity) bool {
	return a.info != nil && b.info != nil && os.SameFile(a.info, b.info)
}

func mediaLocalFilesystemReason(path string) string {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return "无法识别挂载类型，使用定时扫描"
	}
	switch uint32(stat.Type) {
	case 0x6969, 0x517B, 0xFF534D42, 0xFE534D42, 0x65735546, 0x73757245, 0x564c, 0x01021997:
		return "网络或 FUSE 挂载的远端变更不保证通知，使用定时扫描"
	}
	return ""
}
