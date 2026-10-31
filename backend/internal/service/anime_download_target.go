package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
	"strm115/backend/internal/storage"
)

// AnimeDownloadSession holds credentials only in memory. Persist Target(),
// then restore via ResumeDownloadTarget; never persist a P115Account or Cookie.
// All fields are private and fixed at construction, so workers may share it.
type AnimeDownloadSession struct {
	work        *storage.AnimeDownloadWork
	target      model.AnimeDownloadTarget
	cookie      string
	store       *storage.Store
	accounts    animePrimaryAccounts
	directories animeDirectories
}

func (s AnimeDownloadSession) String() string   { return "AnimeDownloadSession{credentials:redacted}" }
func (s AnimeDownloadSession) GoString() string { return s.String() }

func (s *AnimeDownloadSession) Target() model.AnimeDownloadTarget {
	if s == nil {
		return model.AnimeDownloadTarget{}
	}
	return s.target
}

// PrepareDownloadTarget checks the administrator's exact settings version.
// It performs only reads; claiming a resource and creating a job follow in P2-02.
func (s *AnimeAdminService) PrepareDownloadTarget(ctx context.Context, expectedSettingsRevision int64) (*AnimeDownloadSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if expectedSettingsRevision < 1 || expectedSettingsRevision > model.AnimeMaxRevision {
		return nil, storage.ErrAnimeInvalid
	}
	if s == nil || s.Store == nil {
		return nil, storage.ErrAnimeInvalid
	}
	settings, err := s.Store.GetAnimeSettings(ctx)
	if err != nil {
		return nil, err
	}
	if settings.Revision != expectedSettingsRevision {
		return nil, storage.ErrAnimeSettingsChanged
	}
	issues := make([]string, 0, 2)
	if settings.AccountID == nil {
		issues = append(issues, "PRIMARY_ACCOUNT_REQUIRED")
	}
	if settings.DownloadCID == nil || settings.DownloadPath == nil {
		issues = append(issues, "DOWNLOAD_DIRECTORY_REQUIRED")
	}
	if len(issues) != 0 {
		return nil, &AnimeConfigError{Code: "CONFIG_INCOMPLETE", Issues: issues}
	}
	target := model.AnimeDownloadTarget{
		SchemaVersion: model.AnimeSchemaVersion, SettingsRevision: settings.Revision,
		AccountID: *settings.AccountID,
		ParentCID: *settings.DownloadCID, ParentPath: *settings.DownloadPath,
	}
	session, err := s.ResumeDownloadTarget(ctx, target)
	if err != nil {
		return nil, err
	}
	// The read above may overlap an administrator changing settings. No network
	// I/O occurs in this final transaction, which P2-02 also repeats on insertion.
	if err := s.Store.CheckAnimeDownloadSettings(ctx, target); err != nil {
		return nil, err
	}
	return session, nil
}

// ResumeDownloadTarget reads only the saved account and CID.
// A change to current defaults cannot retarget an existing task. A directory
// rename is harmless: the original display path remains part of its snapshot.
func (s *AnimeAdminService) ResumeDownloadTarget(ctx context.Context, target model.AnimeDownloadTarget) (*AnimeDownloadSession, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := target.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", storage.ErrAnimeInvalid, err)
	}
	if s == nil || s.Store == nil || s.Accounts == nil || s.Directories == nil {
		return nil, &AnimeConfigError{Code: "CONFIG_INCOMPLETE", Issues: []string{"PRIMARY_ACCOUNT_REQUIRED"}}
	}
	a, err := s.Accounts.Primary(target.AccountID)
	if err != nil || !animeDownloadAccountMatches(a, target) {
		return nil, storage.ErrAnimeAccountChanged
	}
	session := &AnimeDownloadSession{target: target, cookie: a.Cookie, store: s.Store, accounts: s.Accounts, directories: s.Directories}
	if err := session.Validate(ctx); err != nil {
		return nil, err
	}
	return session, nil
}

func animeDownloadAccountMatches(a P115Account, v model.AnimeDownloadTarget) bool {
	return a.Primary && a.Ownership == "admin" && a.OwnerUserID == "" && a.ID == v.AccountID && a.Cookie != ""
}

// Validate rejects a removed, cleared or replaced account without replacing
// the pinned Cookie. Same-account login refreshes do not retarget a running
// session. Workers must call it before new I/O. Already-issued requests cannot
// be undone; unknown outcomes are reconciled by the later job lifecycle.
func (s *AnimeDownloadSession) Validate(ctx context.Context) error {
	if s == nil || s.store == nil || s.accounts == nil || s.directories == nil {
		return storage.ErrAnimeInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a, err := s.accounts.Primary(s.target.AccountID)
	if err != nil || !animeDownloadAccountMatches(a, s.target) {
		return storage.ErrAnimeAccountChanged
	}
	if err := s.store.CheckAnimeDownloadAccount(ctx, s.target); err != nil {
		return err
	}
	if s.work != nil {
		return s.store.CheckAnimeDownloadWork(ctx, *s.work)
	}
	return nil
}

// ReadDirectory verifies exact CID accessibility with the pinned credential,
// without scanning another account or falling back to root. Upstream errors
// are classified without echoing request data or credentials.
func (s *AnimeDownloadSession) ReadDirectory(ctx context.Context) (p115.DriveDirectory, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.Validate(ctx); err != nil {
		return p115.DriveDirectory{}, err
	}
	directory, err := s.directories.ReadDirectory(ctx, s.target.ParentCID, s.cookie)
	if err != nil {
		return p115.DriveDirectory{}, animeDownloadDirectoryError(ctx, err)
	}
	if err := ctx.Err(); err != nil {
		return p115.DriveDirectory{}, err
	}
	verified := s.target
	verified.ParentPath = directory.Path
	if directory.CID != s.target.ParentCID || verified.Validate() != nil {
		return p115.DriveDirectory{}, &AnimeConfigError{Code: "UPSTREAM_INVALID"}
	}
	if err := s.Validate(ctx); err != nil {
		return p115.DriveDirectory{}, err
	}
	return directory, nil
}

func animeDownloadDirectoryError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("read anime download directory: %w", context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("read anime download directory: %w", context.DeadlineExceeded)
	}
	return ErrAnimeDirectoryRead
}

func (s *AnimeDownloadSession) leaseToken() string {
	if s == nil || s.work == nil {
		return ""
	}
	return s.work.Token
}
