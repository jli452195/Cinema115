//go:build !linux && !windows

package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMediaOrganizerLocalNativeIdentityIgnoresMutableTimestamps(t *testing.T) {
	full := filepath.Join(t.TempDir(), "identity")
	if err := os.Mkdir(full, 0755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	first, err := mediaLocalFileIdentityKey(full, before)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(full, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mediaLocalFileIdentityKey(full, after)
	if err != nil || first != second {
		t.Fatal("文件时间变化不能改变原生身份", first, second, err)
	}
}
