package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"modernc.org/sqlite"
	"strm115/backend/internal/model"
)

var (
	ErrAnimeInvalid    = errors.New("invalid anime data")
	ErrAnimeConflict   = errors.New("anime revision conflict")
	ErrAnimeExists     = errors.New("anime record already exists")
	ErrAnimeIdentity   = errors.New("anime identity cannot be changed in place")
	ErrAnimeCapability = errors.New("anime execution is unavailable in P1")
)

// AnimeExistsError lets the API locate an existing entry/subscription without
// parsing a SQLite error. It never carries external data or credentials.
type AnimeExistsError struct{ Kind, ID string }

func (e *AnimeExistsError) Error() string { return "anime " + e.Kind + " already exists: " + e.ID }
func (e *AnimeExistsError) Unwrap() error { return ErrAnimeExists }

func animeDBError(action string, err error) error {
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case 1555, 2067:
			return fmt.Errorf("%s: %w", action, ErrAnimeExists)
		case 787, 275, 1299:
			return fmt.Errorf("%s: %w", action, ErrAnimeInvalid)
		}
	}
	return fmt.Errorf("%s: %w", action, err)
}

func animeChanged(result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read anime write result: %w", err)
	}
	if n != 1 {
		return ErrAnimeConflict
	}
	return nil
}

func newAnimeID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate anime ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func animeJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode anime DTO: %w", err)
	}
	return string(b), nil
}

func decodeAnimeJSON(raw string, dst any) error {
	return model.DecodeAnimeJSON(strings.NewReader(raw), dst)
}

type animeScanner interface{ Scan(...any) error }
type animeReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const animeEntryColumns = `id,bangumi_id,edition_key,title,original_title,metadata_json,revision,created_at,updated_at`

func scanAnimeEntry(row animeScanner) (model.AnimeEntry, error) {
	var e model.AnimeEntry
	var raw string
	if err := row.Scan(&e.ID, &e.BangumiID, &e.EditionKey, &e.Title, &e.OriginalTitle, &raw, &e.Revision, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return e, err
	}
	if err := decodeAnimeJSON(raw, &e.Metadata); err != nil {
		return e, err
	}
	return e, nil
}

// SaveAnimeEntry treats Revision as expectedRevision (zero for a new entry).
// External confirmation may fill an unknown Bangumi ID once, but rebinding a
// confirmed chapter requires a new entry instead of moving its history.
func (s *Store) SaveAnimeEntry(ctx context.Context, in model.AnimeEntry) (model.AnimeEntry, error) {
	if err := in.Validate(); err != nil {
		return model.AnimeEntry{}, fmt.Errorf("%w: %v", ErrAnimeInvalid, err)
	}
	if (in.ID == "") != (in.Revision == 0) {
		return model.AnimeEntry{}, ErrAnimeInvalid
	}
	raw, err := animeJSON(in.Metadata)
	if err != nil {
		return model.AnimeEntry{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.AnimeEntry{}, fmt.Errorf("begin anime entry: %w", err)
	}
	defer tx.Rollback()
	if in.ID != "" {
		old, err := scanAnimeEntry(tx.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE id=?`, in.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return model.AnimeEntry{}, ErrAnimeConflict
		}
		if err != nil {
			return model.AnimeEntry{}, fmt.Errorf("read anime entry: %w", err)
		}
		if old.Revision != in.Revision {
			return model.AnimeEntry{}, ErrAnimeConflict
		}
		if old.EditionKey != in.EditionKey || old.BangumiID != nil && !sameAnimeString(old.BangumiID, in.BangumiID) {
			return model.AnimeEntry{}, ErrAnimeIdentity
		}
		in.CreatedAt = old.CreatedAt
	}
	if in.BangumiID != nil {
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT id FROM anime_entries WHERE bangumi_id=? AND edition_key=? AND id<>?`, *in.BangumiID, in.EditionKey, in.ID).Scan(&existing)
		if err == nil {
			return model.AnimeEntry{}, &AnimeExistsError{Kind: "entry", ID: existing}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return model.AnimeEntry{}, fmt.Errorf("find anime chapter: %w", err)
		}
	}
	now := time.Now().UTC().UnixMilli()
	var result sql.Result
	if in.ID == "" {
		in.ID, err = newAnimeID()
		if err != nil {
			return model.AnimeEntry{}, err
		}
		in.CreatedAt = now
		result, err = tx.ExecContext(ctx, `INSERT INTO anime_entries(`+animeEntryColumns+`) VALUES(?,?,?,?,?,?,1,?,?)`, in.ID, in.BangumiID, in.EditionKey, in.Title, in.OriginalTitle, raw, now, now)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE anime_entries SET bangumi_id=?,title=?,original_title=?,metadata_json=?,revision=revision+1,updated_at=? WHERE id=? AND revision=?`, in.BangumiID, in.Title, in.OriginalTitle, raw, now, in.ID, in.Revision)
	}
	if err != nil {
		return model.AnimeEntry{}, animeDBError("save anime entry", err)
	}
	if err := animeChanged(result); err != nil {
		return model.AnimeEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.AnimeEntry{}, fmt.Errorf("commit anime entry: %w", err)
	}
	in.Revision++
	in.UpdatedAt = now
	return in, nil
}

func (s *Store) GetAnimeEntry(ctx context.Context, id string) (model.AnimeEntry, error) {
	if !model.ValidAnimeID(id) {
		return model.AnimeEntry{}, ErrAnimeInvalid
	}
	e, err := scanAnimeEntry(s.db.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE id=?`, id))
	if err != nil {
		return e, fmt.Errorf("get anime entry: %w", err)
	}
	return e, nil
}

func (s *Store) FindAnimeEntry(ctx context.Context, bangumiID, editionKey string) (model.AnimeEntry, error) {
	if !model.ValidAnimeExternalID(bangumiID) || editionKey == "" {
		return model.AnimeEntry{}, ErrAnimeInvalid
	}
	e, err := scanAnimeEntry(s.db.QueryRowContext(ctx, `SELECT `+animeEntryColumns+` FROM anime_entries WHERE bangumi_id=? AND edition_key=?`, bangumiID, editionKey))
	if err != nil {
		return e, fmt.Errorf("find anime entry: %w", err)
	}
	return e, nil
}

// Read-only until the settings service verifies the explicit primary account
// and directory. An unconfigured database has revision=1 and null bindings.
func (s *Store) GetAnimeSettings(ctx context.Context) (model.AnimeSettings, error) {
	var v model.AnimeSettings
	err := s.db.QueryRowContext(ctx, `SELECT revision,account_id,account_revision,uid,download_cid,download_path,organize_cid,organize_path FROM anime_settings WHERE id=1`).Scan(&v.Revision, &v.AccountID, &v.AccountRevision, &v.UID, &v.DownloadCID, &v.DownloadPath, &v.OrganizeCID, &v.OrganizePath)
	if err != nil {
		return v, fmt.Errorf("get anime settings: %w", err)
	}
	return v, nil
}

func sameAnimeString(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

const animeSubscriptionColumns = `id,entry_id,status,revision,run_generation,first_enabled_at,form_json,created_at,updated_at,naming_json`

func scanAnimeSubscription(row animeScanner) (model.AnimeSubscription, error) {
	var sub model.AnimeSubscription
	var raw, naming string
	err := row.Scan(&sub.ID, &sub.EntryID, &sub.Status, &sub.Revision, &sub.RunGeneration, &sub.FirstEnabledAt, &raw, &sub.CreatedAt, &sub.UpdatedAt, &naming)
	if err != nil {
		return sub, err
	}
	sub.Sources = make([]model.AnimeSource, 0, model.AnimeMaxSources)
	if err := decodeAnimeJSON(raw, &sub.Form); err != nil {
		return sub, err
	}
	if err := decodeAnimeJSON(naming, &sub.Naming); err != nil {
		return sub, err
	}
	return sub, nil
}

const animeSourceColumns = `id,subscription_id,role,position,status,run_when_enabled,provider,source_key,item_id,group_id,group_name,namespace_id,binding_json,rules_json,revision,run_generation,active_baseline_id,health,checked_at,attempted_at,check_token,next_check_at,retry_after,error_code,created_at,updated_at`

func scanAnimeSource(row animeScanner) (model.AnimeSource, error) {
	var s model.AnimeSource
	var binding, rules string
	err := row.Scan(&s.ID, &s.SubscriptionID, &s.Role, &s.Position, &s.Status, &s.RunWhenEnabled, &s.Provider, &s.SourceKey, &s.ItemID, &s.GroupID, &s.GroupName, &s.NamespaceID, &binding, &rules, &s.Revision, &s.RunGeneration, &s.ActiveBaselineID, &s.Health, &s.CheckedAt, &s.AttemptedAt, &s.CheckToken, &s.NextCheckAt, &s.RetryAfter, &s.ErrorCode, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	if err := decodeAnimeJSON(binding, &s.Binding); err != nil {
		return s, err
	}
	if err := decodeAnimeJSON(rules, &s.Rules); err != nil {
		return s, err
	}
	return s, nil
}

func readAnimeSources(ctx context.Context, q animeReader, id string, removed bool) ([]model.AnimeSource, error) {
	query := `SELECT ` + animeSourceColumns + ` FROM anime_subscription_sources WHERE subscription_id=?`
	if !removed {
		query += ` AND status<>'removed'`
	}
	rows, err := q.QueryContext(ctx, query+` ORDER BY position,id`, id)
	if err != nil {
		return nil, fmt.Errorf("query anime sources: %w", err)
	}
	defer rows.Close()
	items := make([]model.AnimeSource, 0, model.AnimeMaxSources)
	for rows.Next() {
		item, err := scanAnimeSource(rows)
		if err != nil {
			return nil, fmt.Errorf("read anime source: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read anime sources: %w", err)
	}
	return items, nil
}

func (s *Store) GetAnimeSubscription(ctx context.Context, id string, includeRemovedSources bool) (model.AnimeSubscription, error) {
	if !model.ValidAnimeID(id) {
		return model.AnimeSubscription{}, ErrAnimeInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return model.AnimeSubscription{}, fmt.Errorf("begin anime detail: %w", err)
	}
	defer tx.Rollback()
	sub, err := scanAnimeSubscription(tx.QueryRowContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions WHERE id=?`, id))
	if err != nil {
		return sub, fmt.Errorf("get anime subscription: %w", err)
	}
	sub.Sources, err = readAnimeSources(ctx, tx, id, includeRemovedSources)
	if err != nil {
		return sub, err
	}
	if err := tx.Commit(); err != nil {
		return sub, fmt.Errorf("finish anime detail: %w", err)
	}
	return sub, nil
}

type AnimeSubscriptionFilter struct {
	Status, Keyword, BangumiID string
	Page, PageSize             int
}
type AnimeSubscriptionPage struct {
	Items                 []model.AnimeSubscription
	Total, Page, PageSize int
}

func animePage(page, size int) (int, int, error) {
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 50
	}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return 0, 0, ErrAnimeInvalid
	}
	return page, size, nil
}

func (s *Store) ListAnimeSubscriptions(ctx context.Context, f AnimeSubscriptionFilter) (AnimeSubscriptionPage, error) {
	var out AnimeSubscriptionPage
	page, size, err := animePage(f.Page, f.PageSize)
	if err != nil {
		return out, err
	}
	switch f.Status {
	case "", "draft", "paused", "enabled", "archived":
	default:
		return out, ErrAnimeInvalid
	}
	if len(f.Keyword) > 512 || f.BangumiID != "" && !model.ValidAnimeExternalID(f.BangumiID) {
		return out, ErrAnimeInvalid
	}
	out.Page, out.PageSize = page, size
	out.Items = make([]model.AnimeSubscription, 0, size)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, fmt.Errorf("begin anime list: %w", err)
	}
	defer tx.Rollback()
	where := ` WHERE (?='' AND status<>'removed' OR status=?) AND (?='' OR entry_id IN (SELECT id FROM anime_entries WHERE instr(lower(title),lower(?))>0 OR instr(lower(original_title),lower(?))>0))`
	where += ` AND (?='' OR entry_id IN (SELECT id FROM anime_entries WHERE bangumi_id=?))`
	args := []any{f.Status, f.Status, f.Keyword, f.Keyword, f.Keyword, f.BangumiID, f.BangumiID}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM anime_subscriptions`+where, args...).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("count anime subscriptions: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+animeSubscriptionColumns+` FROM anime_subscriptions`+where+` ORDER BY updated_at DESC,id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return out, fmt.Errorf("query anime subscriptions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scanAnimeSubscription(rows)
		if err != nil {
			return out, fmt.Errorf("read anime list: %w", err)
		}
		out.Items = append(out.Items, item)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("read anime list: %w", err)
	}
	if err := rows.Close(); err != nil {
		return out, fmt.Errorf("close anime list: %w", err)
	}
	// Close the parent rows before querying children on the single connection.
	for i := range out.Items {
		out.Items[i].Sources, err = readAnimeSources(ctx, tx, out.Items[i].ID, false)
		if err != nil {
			return out, err
		}
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("finish anime list: %w", err)
	}
	return out, nil
}
