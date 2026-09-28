package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"strm115/backend/internal/storage"
)

type sqliteAccountStore struct {
	db       *storage.Store
	aead     cipher.AEAD
	value    string
	revision uint64
}

func (s *sqliteAccountStore) Value() string { return s.value }

func OpenP115AccountsSQLite(ctx context.Context, db *storage.Store, dir, secret string) (*P115Accounts, error) {
	key := sha256.Sum256([]byte("cinema115-sqlite-p115\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	state, err := db.LoadP115Accounts(ctx)
	if err != nil {
		return nil, err
	}
	store := &sqliteAccountStore{db: db, aead: aead, revision: state.Revision}
	if state.Revision != 0 {
		doc := accountDocument{Version: 1, Accounts: make([]P115Account, 0, len(state.Accounts))}
		for _, row := range state.Accounts {
			n := aead.NonceSize()
			if len(row.Cookie) < n {
				return nil, errors.New("115 账号密文损坏")
			}
			cookie, err := aead.Open(nil, row.Cookie[:n], row.Cookie[n:], []byte(row.ID))
			if err != nil {
				return nil, errors.New("115 账号无法解密，请检查原 JWT_SECRET；数据库未修改")
			}
			doc.Accounts = append(doc.Accounts, P115Account{Primary: row.Primary, UID: row.UID, ID: row.ID, Name: row.Name, Cookie: string(cookie), Revision: row.Revision, PlaybackParentCID: row.PlaybackParentCID})
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		store.value = string(raw)
		return newP115Accounts(store, "")
	}
	// 仅在数据库尚未初始化账号时读取旧文件；不修改、删除或创建旧会话文件。
	old, err := NewSessionStore(filepath.Join(dir, "p115-accounts.enc"), secret)
	if err != nil {
		return nil, err
	}
	legacy := ""
	if old.Value() != "" {
		if _, err := newP115Accounts(old, ""); err != nil {
			return nil, err
		}
		if err := store.save(ctx, old.Value()); err != nil {
			return nil, err
		}
	} else {
		single, err := NewSessionStore(filepath.Join(dir, "p115-session.enc"), secret)
		if err != nil {
			return nil, err
		}
		legacy = single.Value()
		doc := accountDocument{Version: 1, Accounts: []P115Account{}}
		if legacy != "" {
			doc.LegacyActiveID = "default"
			doc.Accounts = append(doc.Accounts, P115Account{ID: "default", Name: "账号 1", Cookie: legacy, Revision: 1})
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		if err := store.save(ctx, string(raw)); err != nil {
			return nil, err
		}
	}
	return newP115Accounts(store, "")
}

func (s *sqliteAccountStore) Save(value string) error {
	// 兼容现有同步账号接口，数据库等待有独立上限，不持有无限期写锁。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.save(ctx, value)
}
func (s *sqliteAccountStore) save(ctx context.Context, value string) error {
	var doc accountDocument
	if err := json.Unmarshal([]byte(value), &doc); err != nil {
		return fmt.Errorf("编码账号数据: %w", err)
	}
	state := storage.P115AccountState{LegacyShareAccountID: doc.LegacyActiveID, Revision: s.revision, Accounts: make([]storage.P115AccountRow, 0, len(doc.Accounts))}
	for i, account := range doc.Accounts {
		if s.revision == 0 && account.ID == "default" && account.Name == "默认账号" {
			account.Name = "账号 1"
			doc.Accounts[i] = account
		}
		nonce := make([]byte, s.aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		// 将密文绑定配置编号，防止两行密文被交换后仍能解密。
		sealed := s.aead.Seal(nonce, nonce, []byte(account.Cookie), []byte(account.ID))
		state.Accounts = append(state.Accounts, storage.P115AccountRow{Primary: account.Primary, UID: account.UID, ID: account.ID, Name: account.Name, Cookie: sealed, Revision: account.Revision, PlaybackParentCID: account.PlaybackParentCID})
	}
	doc.LegacyActiveID = ""
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := s.db.SaveP115Accounts(ctx, state); err != nil {
		if errors.Is(err, storage.ErrAccountRevision) {
			return fmt.Errorf("%w，请重启后端重新载入数据库", ErrAccountChanged)
		}
		return err
	}
	s.value, s.revision = string(raw), s.revision+1
	return nil
}

// 升级时把旧的全局选择一次性固化为分享功能绑定；新安装和后续功能都必须明确选择。
func MigrateP115ShareSelection(ctx context.Context, db *storage.Store, settings *AutomationStore) error {
	id, err := db.PendingP115ShareAccount(ctx)
	if err != nil || id == "" {
		return err
	}
	value := settings.Value()
	// 尚未配置分享功能的新环境没有可继承的业务设置，仍要求用户明确选择。
	if value.Generation.BaseURL == "" {
		return db.FinishP115ShareAccountMigration(ctx)
	}
	if value.AccountID == "" {
		value.AccountID = id
		if err := settings.Save(value); err != nil {
			return fmt.Errorf("迁移分享使用账号: %w", err)
		}
	}
	return db.FinishP115ShareAccountMigration(ctx)
}
