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

type GeneratedPortalInvite struct {
	model.PortalInvite
	Code string `json:"code"`
}

func inviteHash(code string) (string, error) {
	if len(code) > 64 {
		return "", storage.ErrPortalInviteInvalid
	}
	code = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	decoded, err := hex.DecodeString(code)
	if err != nil || len(decoded) != 16 {
		return "", storage.ErrPortalInviteInvalid
	}
	hash := sha256.Sum256(decoded)
	return hex.EncodeToString(hash[:]), nil
}

func (a *PortalAuth) GenerateInvites(ctx context.Context, creator, note string, count, days int, sources ...string) ([]GeneratedPortalInvite, error) {
	source := "personal"
	if len(sources) > 0 && sources[0] != "" {
		source = sources[0]
	}
	if source != "personal" && source != "assigned" {
		return nil, fmt.Errorf("%w：邀请播放方式无效", ErrUserInput)
	}
	note = strings.TrimSpace(note)
	if count < 1 || count > 20 || (days != 1 && days != 7 && days != 30) || len([]rune(note)) > 200 {
		return nil, fmt.Errorf("%w：数量为 1–20，有效期为 1、7 或 30 天，备注不超过 200 字", ErrUserInput)
	}
	for attempt := 0; attempt < 3; attempt++ {
		result := make([]GeneratedPortalInvite, 0, count)
		records := make([]model.PortalInvite, 0, count)
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
			invite := model.PortalInvite{ID: hex.EncodeToString(random[16:]), CodeHash: hash, CreatedBy: creator, Note: note, CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour).Unix()}
			records = append(records, invite)
			records[len(records)-1].PlaybackSource = source
			invite.PlaybackSource = source
			result = append(result, GeneratedPortalInvite{PortalInvite: invite, Code: code[:8] + "-" + code[8:16] + "-" + code[16:24] + "-" + code[24:]})
		}
		err := a.DB.CreatePortalInvites(ctx, records)
		if errors.Is(err, storage.ErrPortalInviteCollision) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, fmt.Errorf("生成邀请: %w", storage.ErrPortalInviteCollision)
}

func (a *PortalAuth) CheckInvite(ctx context.Context, code string) error {
	hash, err := inviteHash(code)
	if err != nil {
		return err
	}
	return a.DB.CheckPortalInvite(ctx, hash)
}

func (a *PortalAuth) InviteSource(ctx context.Context, code string) (string, error) {
	hash, err := inviteHash(code)
	if err != nil {
		return "", err
	}
	return a.DB.PortalInviteSource(ctx, hash)
}
