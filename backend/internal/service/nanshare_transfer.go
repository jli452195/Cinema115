package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/client/nanshare"
)

type NanShareTransferInput struct {
	RequestID string `json:"requestId"`
	SearchID  string `json:"searchId"`
	Slug      string `json:"slug"`
	AccountID string `json:"accountId"`
	FolderID  string `json:"folderId"`
}

func (s *NanShareService) TransferOptions(ctx context.Context, searchID string) (nanshare.TransferOptions, error) {
	cached, err := s.searchSnapshot(searchID)
	if err != nil {
		return nanshare.TransferOptions{}, err
	}
	client, err := s.currentSearchClient(cached)
	if err != nil {
		return nanshare.TransferOptions{}, err
	}
	return client.TransferOptions(ctx)
}

// The remote transfer helper owns its saved account and directories. Do not
// forward local 115 cookies or invent an account fallback for that service.
func (s *NanShareService) Transfer(ctx context.Context, input NanShareTransferInput) (NanShareSubmission, error) {
	id, err := hex.DecodeString(input.RequestID)
	if err != nil || len(id) != 16 || len(input.RequestID) != 32 || input.AccountID == "" || input.FolderID == "" {
		return NanShareSubmission{}, nanshare.ErrInput
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	cached, err := s.searchSnapshot(input.SearchID)
	if err != nil {
		return NanShareSubmission{}, err
	}
	client, err := s.currentSearchClient(cached)
	if err != nil {
		return NanShareSubmission{}, err
	}
	s.searchMu.Lock()
	previous, exists := cached.attempts[input.RequestID]
	current := cached.transfers[input.Slug]
	s.searchMu.Unlock()
	if exists {
		if previous.mode != "transfer" || previous.slug != input.Slug || previous.account != input.AccountID || previous.version != input.FolderID {
			return NanShareSubmission{}, nanshare.ErrInput
		}
		return previous.receipt, nil
	}
	if current.State == "accepted" || current.State == "already_saved" || current.State == "unknown" || current.State == "partial" || current.State == "submitting" {
		return current, nil
	}
	known := false
	for _, item := range cached.items {
		if item.Slug == input.Slug && item.Source == "hdhive" && item.PanType == "115" {
			known = true
			break
		}
	}
	if !known {
		return NanShareSubmission{}, nanshare.ErrInput
	}
	account, err := client.RE0Account(ctx)
	if err != nil {
		return NanShareSubmission{}, err
	}
	if account != cached.account {
		return NanShareSubmission{}, nanshare.ErrChanged
	}
	options, err := client.TransferOptions(ctx)
	if err != nil {
		return NanShareSubmission{}, err
	}
	if options.AccountID != input.AccountID {
		return NanShareSubmission{}, nanshare.ErrChanged
	}
	found := false
	for _, folder := range options.Folders {
		if folder.ID == input.FolderID {
			found = true
			break
		}
	}
	if !found {
		return NanShareSubmission{}, fmt.Errorf("%w：转存目录已变化，请重新选择", nanshare.ErrChanged)
	}
	if err := ctx.Err(); err != nil {
		return NanShareSubmission{}, err
	}
	s.searchMu.Lock()
	if s.searches[input.SearchID] == nil {
		s.searchMu.Unlock()
		return NanShareSubmission{}, nanshare.ErrChanged
	}
	if len(cached.attempts) >= 256 {
		s.searchMu.Unlock()
		return NanShareSubmission{}, nanshare.ErrInput
	}
	if cached.transfers == nil {
		cached.transfers = make(map[string]NanShareSubmission)
		s.searches[input.SearchID].transfers = cached.transfers
	}
	pending := NanShareSubmission{RequestID: input.RequestID, State: "submitting", Message: "正在转存到 NanShare 配置的 115 目录"}
	attempt := nanShareAttempt{slug: input.Slug, account: input.AccountID, mode: "transfer", version: input.FolderID, receipt: pending}
	cached.attempts[input.RequestID], cached.transfers[input.Slug] = attempt, pending
	s.searches[input.SearchID].expires = time.Now().Add(15 * time.Minute)
	s.searchMu.Unlock()
	// No retries of this write, including authentication renewal. Record an
	// uncertain receipt if the response is lost; a browser retry reuses it.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 35*time.Second)
	defer cancel()
	result, err := client.TransferResource(writeCtx, "hdhive", "115", input.FolderID, []string{}, input.Slug)
	receipt := NanShareSubmission{RequestID: input.RequestID, State: "unknown", Message: "转存结果未确认，请先在 NanShare 和网盘核查；本次不会自动重试"}
	if err == nil {
		receipt.State, receipt.Message = result.Code, result.Message
		if result.Success {
			receipt.State = "accepted"
		}
	} else if errors.Is(err, nanshare.ErrAuth) {
		receipt.State, receipt.Message = "failed", err.Error()
	}
	s.searchMu.Lock()
	attempt.receipt = receipt
	cached.attempts[input.RequestID], cached.transfers[input.Slug] = attempt, receipt
	s.searchMu.Unlock()
	return receipt, nil
}
