package storage

import (
	"context"
	"database/sql"
	"fmt"

	"strm115/backend/internal/model"
)

func animeTargetFromSettings(v model.AnimeSettings) (model.AnimeDownloadTarget, error) {
	if v.AccountID == nil || v.AccountRevision == nil || v.UID == nil || v.DownloadCID == nil || v.DownloadPath == nil || v.OrganizeCID == nil {
		return model.AnimeDownloadTarget{}, ErrAnimeDirectoryRequired
	}
	target := model.AnimeDownloadTarget{SchemaVersion: 1, SettingsRevision: v.Revision, AccountID: *v.AccountID, AccountRevision: *v.AccountRevision, UID: *v.UID, ParentCID: *v.DownloadCID, ParentPath: *v.DownloadPath}
	return target, target.Validate()
}

// The settings revision also advances for organization-only edits. Actual
// download account, credentials and directory must stay fixed independently.
func sameAnimeDownloadBinding(a, b model.AnimeDownloadTarget) bool {
	a.SettingsRevision = b.SettingsRevision
	return a == b
}

// CheckAnimeDownloadSettings validates a new task's target against one
// consistent SQLite view of both settings and the explicit primary account.
// Future job insertion must repeat checkAnimeDownloadTargetTx in its own
// transaction; a successful read is not a reservation for a later write.
func (s *Store) CheckAnimeDownloadSettings(ctx context.Context, v model.AnimeDownloadTarget) error {
	return s.checkAnimeDownloadTarget(ctx, v, true)
}

// CheckAnimeDownloadAccount permits an existing task to keep its original
// directory after defaults change. Credential/account changes always fail.
func (s *Store) CheckAnimeDownloadAccount(ctx context.Context, v model.AnimeDownloadTarget) error {
	return s.checkAnimeDownloadTarget(ctx, v, false)
}

func (s *Store) checkAnimeDownloadTarget(ctx context.Context, v model.AnimeDownloadTarget, currentSettings bool) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrAnimeInvalid, err)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin anime download target check: %w", err)
	}
	defer tx.Rollback()
	return checkAnimeDownloadTargetTx(ctx, tx, v, currentSettings)
}

func checkAnimeDownloadTargetTx(ctx context.Context, tx *sql.Tx, v model.AnimeDownloadTarget, currentSettings bool) error {
	if err := v.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrAnimeInvalid, err)
	}
	account := model.AnimeSettings{AccountID: &v.AccountID, AccountRevision: &v.AccountRevision, UID: &v.UID}
	if err := checkAnimePrimaryTx(ctx, tx, account); err != nil {
		return err
	}
	if !currentSettings {
		return nil
	}
	settings, err := readAnimeSettingsTx(ctx, tx)
	if err != nil {
		return fmt.Errorf("check anime download settings: %w", err)
	}
	// Organization-only changes advance the shared settings revision without
	// changing this download authorization. Compare the actual fixed binding.
	if !sameAnimeString(settings.AccountID, &v.AccountID) || settings.AccountRevision == nil || *settings.AccountRevision != v.AccountRevision || !sameAnimeString(settings.UID, &v.UID) || !sameAnimeString(settings.DownloadCID, &v.ParentCID) || !sameAnimeString(settings.DownloadPath, &v.ParentPath) {
		return ErrAnimeSettingsChanged
	}
	return nil
}
