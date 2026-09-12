package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SessionStore 将 Cookie 加密保存，支持后端重启后恢复。
type SessionStore struct {
	mu    sync.RWMutex
	value string
	path  string
	aead  cipher.AEAD
}

func NewSessionStore(path, secret string) (*SessionStore, error) {
	key := sha256.Sum256([]byte("cinema115-session\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	store := &SessionStore{path: path, aead: aead}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取登录会话: %w", err)
	}
	n := aead.NonceSize()
	if len(data) < n {
		return nil, fmt.Errorf("登录会话文件损坏")
	}
	plain, err := aead.Open(nil, data[:n], data[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("登录会话无法解密，请检查 JWT_SECRET 或移除旧会话后重新登录")
	}
	store.value = string(plain)
	return store, nil
}

func (s *SessionStore) Value() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.value }

func (s *SessionStore) Save(value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == "" {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除登录会话: %w", err)
		}
		s.value = ""
		return nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	data := s.aead.Seal(nonce, nonce, []byte(value), nil)
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), s.path); err != nil {
		return fmt.Errorf("保存登录会话: %w", err)
	}
	s.value = value
	return nil
}
