package storage

import (
	"context"
	"database/sql"
	"errors"
)

func readAnimeActionReceiptTx(ctx context.Context, tx *sql.Tx, key AnimeWriteKey, out any) (bool, error) {
	var digest, raw string
	err := tx.QueryRowContext(ctx, `SELECT request_digest,receipt_json FROM anime_request_keys WHERE actor_key=? AND operation=? AND request_key=?`, key.ActorKey, key.Operation, key.Key).Scan(&digest, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if digest != key.Digest {
		return false, ErrAnimeIdempotency
	}
	return true, decodeAnimeJSON(raw, out)
}

func writeAnimeActionReceiptTx(ctx context.Context, tx *sql.Tx, key AnimeWriteKey, out any, now int64) error {
	raw, err := animeJSON(out)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO anime_request_keys(actor_key,operation,request_key,request_digest,http_status,receipt_json,created_at) VALUES(?,?,?,?,200,?,?)`, key.ActorKey, key.Operation, key.Key, key.Digest, raw, now)
	return err
}
