package service

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func mediaLocalFilesystemReason(string) string { return "" }

func mediaLocalFileIdentityKey(full string, info os.FileInfo) (string, error) {
	identity, err := mediaLocalIdentityOf(full, info)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", identity.volume, identity.index), nil
}

// os.SameFile loads Windows IDs lazily via an exclusive path handle. Capture
// IDs immediately with sharing enabled so directory watches and active copies
// neither change the identity being compared nor conflict with the probe.
func mediaLocalIdentityOf(path string, info os.FileInfo) (mediaLocalIdentity, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return mediaLocalIdentity{}, fmt.Errorf("检查文件身份: %w", err)
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return mediaLocalIdentity{}, fmt.Errorf("检查文件身份: %w", err)
	}
	defer windows.CloseHandle(handle)
	var attributes windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &attributes); err != nil {
		return mediaLocalIdentity{}, fmt.Errorf("读取文件身份: %w", err)
	}
	return mediaLocalIdentity{info: info, volume: uint64(attributes.VolumeSerialNumber), index: uint64(attributes.FileIndexHigh)<<32 | uint64(attributes.FileIndexLow)}, nil
}
func mediaSameLocalIdentity(a, b mediaLocalIdentity) bool {
	return a.info != nil && b.info != nil && a.volume == b.volume && a.index == b.index
}
