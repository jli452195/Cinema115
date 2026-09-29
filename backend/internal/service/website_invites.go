package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
	"time"
)

type GeneratedWebsiteInvite struct {
	model.WebsiteInvite
	Code string `json:"code"`
}

func inviteHash(code string) (string, error) {
	if len(code) > 64 {
		return "", storage.ErrWebsiteInviteInvalid
	}
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	decoded, err := hex.DecodeString(code)
	if err != nil || len(decoded) != 16 {
		return "", storage.ErrWebsiteInviteInvalid
	}
	hash := sha256.Sum256(decoded)
	return hex.EncodeToString(hash[:]), nil
}

func (a *WebsiteAuth) GenerateInvites(ctx context.Context, creator, note string, count, days int) ([]GeneratedWebsiteInvite, error) {
	note = strings.TrimSpace(note)
	if count < 1 || count > 20 || (days != 1 && days != 7 && days != 30) || len([]rune(note)) > 200 {
		return nil, fmt.Errorf("%w：数量为 1–20，有效期为 1、7 或 30 天，备注不超过 200 字", ErrUserInput)
	}
	for attempt := 0; attempt < 3; attempt++ {
		result := make([]GeneratedWebsiteInvite, 0, count)
		records := make([]model.WebsiteInvite, 0, count)
		now := time.Now()
		for i := 0; i < count; i++ {
			var random [32]byte
			if _, err := rand.Read(random[:]); err != nil {
				return nil, fmt.Errorf("生成邀请随机数: %w", err)
			}
			code := hex.EncodeToString(random[:16])
			hash, err := inviteHash(code)
			if err != nil {
				return nil, err
			}
			invite := model.WebsiteInvite{ID: hex.EncodeToString(random[16:]), CodeHash: hash, CreatedBy: creator, Note: note, CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour).Unix()}
			records = append(records, invite)
			result = append(result, GeneratedWebsiteInvite{WebsiteInvite: invite, Code: code[:8] + "-" + code[8:16] + "-" + code[16:24] + "-" + code[24:]})
		}
		err := a.DB.CreateWebsiteInvites(ctx, records)
		if errors.Is(err, storage.ErrWebsiteInviteCollision) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, fmt.Errorf("生成邀请: %w", storage.ErrWebsiteInviteCollision)
}

func (a *WebsiteAuth) CheckInvite(ctx context.Context, code string) error {
	hash, err := inviteHash(code)
	if err != nil {
		return err
	}
	return a.DB.CheckWebsiteInvite(ctx, hash)
}

func (a *WebsiteAuth) Register(ctx context.Context, name, password, code string) (model.WebsiteUser, error) {
	hash, err := inviteHash(code)
	if err != nil {
		return model.WebsiteUser{}, err
	}
	// Reject invalid invitations before expensive password hashing. The write
	// transaction rechecks validity after hashing, including expiry/revocation.
	if err := a.DB.CheckWebsiteInvite(ctx, hash); err != nil {
		return model.WebsiteUser{}, err
	}
	u, err := a.prepareUser(ctx, name, password)
	if err != nil {
		return model.WebsiteUser{}, err
	}
	if err := a.DB.RegisterWebsiteUser(ctx, hash, u); err != nil {
		return model.WebsiteUser{}, err
	}
	return u, nil
}
