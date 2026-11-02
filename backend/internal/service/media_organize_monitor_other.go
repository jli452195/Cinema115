//go:build !linux && !windows

package service

import "os"

func mediaLocalFilesystemReason(string) string { return "" }
func mediaLocalIdentityOf(_ string, info os.FileInfo) (mediaLocalIdentity, error) {
	return mediaLocalIdentity{info: info}, nil
}
func mediaSameLocalIdentity(a, b mediaLocalIdentity) bool {
	return a.info != nil && b.info != nil && os.SameFile(a.info, b.info)
}
