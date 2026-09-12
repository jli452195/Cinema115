package service

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.enc")
	store, err := NewSessionStore(path, "key")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Save("UID=test-cookie"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("test-cookie")) {
		t.Fatal("会话以明文保存")
	}
	restored, err := NewSessionStore(path, "key")
	if err != nil || restored.Value() != store.Value() {
		t.Fatalf("恢复失败: %v", err)
	}
	if _, err = NewSessionStore(path, "other-key"); err == nil {
		t.Fatal("错误密钥被接受")
	}
	if err = store.Save("UID=replaced"); err != nil {
		t.Fatal(err)
	}
	if err = store.Save(""); err != nil {
		t.Fatal(err)
	}
	restored, err = NewSessionStore(path, "key")
	if err != nil || restored.Value() != "" {
		t.Fatal("退出后未清除会话")
	}
}
