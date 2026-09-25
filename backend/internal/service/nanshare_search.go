package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"strm115/backend/internal/client/nanshare"
)

type NanShareSubmission struct {
	RequestID string `json:"requestId"`
	State     string `json:"state"`
	Message   string `json:"message"`
}

type NanShareSearchView struct {
	ID          string                        `json:"id"`
	Items       []nanshare.Resource           `json:"items"`
	Submissions map[string]NanShareSubmission `json:"submissions"`
	ExpiresAt   string                        `json:"expiresAt"`
}

type nanShareSearch struct {
	id          string
	key         string
	doc         nanShareDocument
	account     string
	items       []nanshare.Resource
	submissions map[string]NanShareSubmission
	attempts    map[string]nanShareAttempt
	expires     time.Time
}

type nanShareAttempt struct {
	slug, account, mode, version string
	folder                       int
	receipt                      NanShareSubmission
}

func searchView(s *nanShareSearch) NanShareSearchView {
	states := make(map[string]NanShareSubmission, len(s.submissions))
	for key, state := range s.submissions {
		states[key] = state
	}
	return NanShareSearchView{ID: s.id, Items: s.items, Submissions: states, ExpiresAt: s.expires.UTC().Format(time.RFC3339)}
}

func (s *NanShareService) Search(ctx context.Context, kind string, id int64, refresh bool) (NanShareSearchView, error) {
	if (kind != "movie" && kind != "tv") || id < 1 || id > 9007199254740991 {
		return NanShareSearchView{}, nanshare.ErrInput
	}
	s.mu.RLock()
	doc := s.doc
	s.mu.RUnlock()
	if doc.Token == "" {
		return NanShareSearchView{}, fmt.Errorf("%w：请先登录 NanShare", nanshare.ErrInput)
	}
	key := kind + ":" + strconv.FormatInt(id, 10)
	s.searchMu.Lock()
	if s.searches == nil {
		s.searches = make(map[string]*nanShareSearch)
	}
	for searchID, cached := range s.searches {
		if time.Now().After(cached.expires) {
			delete(s.searches, searchID)
			continue
		}
		if !refresh && cached.key == key && sameNanShareConfig(cached.doc, doc) {
			result := searchView(cached)
			s.searchMu.Unlock()
			return result, nil
		}
	}
	s.searchMu.Unlock()
	client, err := s.clientFor(doc)
	if err != nil {
		return NanShareSearchView{}, err
	}
	account, err := client.RE0Account(ctx)
	if err != nil {
		return NanShareSearchView{}, err
	}
	if account == "" {
		return NanShareSearchView{}, fmt.Errorf("%w：请先在 NanShare 选择 RE0 搜索账号", nanshare.ErrInput)
	}
	items, err := client.Search(ctx, account, kind, id)
	if err != nil {
		return NanShareSearchView{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return NanShareSearchView{}, fmt.Errorf("创建搜索会话: %w", err)
	}
	s.mu.RLock()
	unchanged := sameNanShareConfig(s.doc, doc)
	s.mu.RUnlock()
	if !unchanged {
		return NanShareSearchView{}, nanshare.ErrChanged
	}
	cached := &nanShareSearch{id: hex.EncodeToString(random[:]), key: key, doc: doc, account: account, items: items, submissions: make(map[string]NanShareSubmission), attempts: make(map[string]nanShareAttempt), expires: time.Now().Add(15 * time.Minute)}
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	// Preserve receipts across refreshes and concurrent searches for this media.
	for oldID, old := range s.searches {
		if old.key == key && sameNanShareConfig(old.doc, doc) {
			if !refresh {
				return searchView(old), nil
			}
			for _, state := range old.submissions {
				if state.State == "submitting" {
					return NanShareSearchView{}, fmt.Errorf("%w：请等待当前资源提交完成", nanshare.ErrInput)
				}
			}
			// Keep the session and attempt receipts so a lost response can still
			// be retried with the same request ID after a resource refresh.
			if old.account != account {
				delete(s.searches, oldID)
				continue
			}
			old.items = items
			old.expires = cached.expires
			return searchView(old), nil
		}
	}
	if len(s.searches) >= 64 {
		return NanShareSearchView{}, fmt.Errorf("搜索会话过多，请稍后重试")
	}
	s.searches[cached.id] = cached
	return searchView(cached), nil
}

func (s *NanShareService) searchSnapshot(id string) (nanShareSearch, error) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	cached := s.searches[id]
	if cached == nil || time.Now().After(cached.expires) {
		return nanShareSearch{}, fmt.Errorf("%w：搜索已过期，请刷新搜索", nanshare.ErrInput)
	}
	return *cached, nil
}

func (s *NanShareService) currentSearchClient(cached nanShareSearch) (*nanshare.Client, error) {
	s.mu.RLock()
	current := s.doc
	same := sameNanShareConfig(current, cached.doc)
	s.mu.RUnlock()
	if !same {
		return nil, nanshare.ErrChanged
	}
	return s.clientFor(current)
}

func (s *NanShareService) ShareOptions(ctx context.Context, searchID string) (nanshare.ShareOptions, error) {
	cached, err := s.searchSnapshot(searchID)
	if err != nil {
		return nanshare.ShareOptions{}, err
	}
	client, err := s.currentSearchClient(cached)
	if err != nil {
		return nanshare.ShareOptions{}, err
	}
	return client.ShareOptions(ctx, cached.account)
}

type NanShareSubmitInput struct {
	RequestID   string `json:"requestId"`
	SearchID    string `json:"searchId"`
	Slug        string `json:"slug"`
	AccountID   string `json:"accountId"`
	Mode        string `json:"mode"`
	FolderIndex *int   `json:"folderIndex"`
	Version     string `json:"version"`
}

func (s *NanShareService) Submit(ctx context.Context, input NanShareSubmitInput) (NanShareSubmission, error) {
	requestBytes, requestErr := hex.DecodeString(input.RequestID)
	if requestErr != nil || len(requestBytes) != 16 || len(input.RequestID) != 32 {
		return NanShareSubmission{}, nanshare.ErrInput
	}
	if input.SearchID == "" || input.Slug == "" || input.AccountID == "" || input.Version == "" || input.FolderIndex == nil {
		return NanShareSubmission{}, nanshare.ErrInput
	}
	// Local credentials cannot be switched until submission has finished.
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
	s.searchMu.Unlock()
	if exists {
		if previous.slug != input.Slug || previous.account != input.AccountID || previous.mode != input.Mode || previous.folder != *input.FolderIndex || previous.version != input.Version {
			return NanShareSubmission{}, fmt.Errorf("%w：提交编号不能用于不同的任务", nanshare.ErrInput)
		}
		return previous.receipt, nil
	}
	known := false
	for _, item := range cached.items {
		if item.Slug == input.Slug && item.PanType == "115" {
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
	options, err := client.ShareOptions(ctx, account)
	if err != nil {
		return NanShareSubmission{}, err
	}
	if options.Version != input.Version || options.AccountID != input.AccountID {
		return NanShareSubmission{}, nanshare.ErrChanged
	}
	modeOK, folderOK := false, false
	for _, mode := range options.Modes {
		if mode == input.Mode {
			modeOK = true
		}
	}
	for _, folder := range options.Folders {
		if folder.Index == *input.FolderIndex {
			folderOK = true
		}
	}
	if !modeOK || !folderOK {
		return NanShareSubmission{}, nanshare.ErrInput
	}
	if err := ctx.Err(); err != nil {
		return NanShareSubmission{}, err
	}
	s.searchMu.Lock()
	// A refresh may have replaced the search while options were being checked.
	if s.searches[input.SearchID] == nil {
		s.searchMu.Unlock()
		return NanShareSubmission{}, nanshare.ErrChanged
	}
	if len(cached.attempts) >= 256 {
		s.searchMu.Unlock()
		return NanShareSubmission{}, fmt.Errorf("%w：当前搜索的提交次数过多，请稍后重新搜索", nanshare.ErrInput)
	}
	pending := NanShareSubmission{RequestID: input.RequestID, State: "submitting", Message: "正在提交给 NanShare"}
	attempt := nanShareAttempt{slug: input.Slug, account: input.AccountID, mode: input.Mode, folder: *input.FolderIndex, version: input.Version, receipt: pending}
	cached.attempts[input.RequestID] = attempt
	cached.submissions[input.Slug] = pending
	s.searches[input.SearchID].expires = time.Now().Add(15 * time.Minute)
	s.searchMu.Unlock()
	// Finish recording the receipt even if the browser closes after the click.
	submitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 35*time.Second)
	defer cancel()
	err = client.Submit(submitCtx, input.Slug, input.Mode, *input.FolderIndex)
	state := NanShareSubmission{State: "accepted", Message: "已提交给 NanShare，生成进度请在 NanShare 查看"}
	if err != nil {
		state = NanShareSubmission{State: "unknown", Message: "未确认提交结果，请先在 NanShare 核对任务，避免重复解锁或生成"}
		if errors.Is(err, nanshare.ErrAuth) {
			state = NanShareSubmission{State: "failed", Message: err.Error()}
		}
	}
	state.RequestID = input.RequestID
	s.searchMu.Lock()
	attempt.receipt = state
	cached.attempts[input.RequestID] = attempt
	cached.submissions[input.Slug] = state
	s.searchMu.Unlock()
	return state, nil
}
