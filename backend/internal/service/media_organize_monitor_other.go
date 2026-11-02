//go:build !linux && !windows

package service

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
)

func mediaLocalFileIdentityKey(_ string, info os.FileInfo) (string, error) {
	native := reflect.Indirect(reflect.ValueOf(info.Sys()))
	if native.IsValid() && native.Kind() == reflect.Struct {
		device, hasDevice := mediaLocalNativeIdentityField(native, "Dev")
		inode, hasInode := mediaLocalNativeIdentityField(native, "Ino")
		if hasDevice && hasInode {
			return device + ":" + inode, nil
		}
		if qid := reflect.Indirect(native.FieldByName("Qid")); qid.IsValid() && qid.Kind() == reflect.Struct {
			if value, ok := mediaLocalNativeIdentityField(qid, "Path"); ok {
				return value, nil
			}
		}
	}
	return "", fmt.Errorf("当前系统无法读取稳定的文件原生身份")
}

func mediaLocalNativeIdentityField(native reflect.Value, name string) (string, bool) {
	field := native.FieldByName(name)
	if !field.IsValid() {
		return "", false
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(field.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(field.Uint(), 10), true
	default:
		return "", false
	}
}

func mediaLocalFilesystemReason(string) string { return "" }
func mediaLocalIdentityOf(_ string, info os.FileInfo) (mediaLocalIdentity, error) {
	return mediaLocalIdentity{info: info}, nil
}
func mediaSameLocalIdentity(a, b mediaLocalIdentity) bool {
	return a.info != nil && b.info != nil && os.SameFile(a.info, b.info)
}
