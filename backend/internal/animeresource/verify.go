// Package animeresource verifies BT v1 identities from server-observed releases.
// Post keys, torrent filenames, RSS GUIDs and file SHA1 are never info-hashes.
package animeresource

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"strm115/backend/internal/client/animefeed"
	"strm115/backend/internal/utils/torrentmeta"
)

const VerificationVersion = 1
const MaxTorrentBytes = torrentmeta.MaxBytes

var (
	ErrInvalid     = errors.New("invalid anime resource identity")
	ErrUnsupported = errors.New("unsupported anime resource protocol")
	ErrConflict    = errors.New("anime resource identity evidence conflicts")
)

// Evidence is a credential-free whitelist. No raw URL, headers or arbitrary
// upstream document is retained. Fingerprint ties verification to one release
// observation, including its original source download hints.
type Evidence struct {
	SchemaVersion      int    `json:"schemaVersion"`
	Method             string `json:"method"`
	Provider           string `json:"provider"`
	PostKey            string `json:"postKey"`
	ContentFingerprint string `json:"contentFingerprint"`
	MetadataSHA256     string `json:"metadataSha256,omitempty"`
}

// Verified cannot be constructed from an arbitrary claimed hash by callers.
// Its exported projection contains no credentials and is immutable by value.
type Verified struct {
	hash       string
	verifiedAt int64
	evidence   Evidence
}

func (v Verified) Hash() string       { return v.hash }
func (v Verified) VerifiedAt() int64  { return v.verifiedAt }
func (v Verified) Evidence() Evidence { return v.evidence }
func (v Verified) Valid() bool {
	return len(v.hash) == 40 && v.verifiedAt > 0 && v.evidence.SchemaVersion == 1 && v.evidence.ContentFingerprint != ""
}

// Restore accepts only a previously committed, trusted verification record.
// It is for storage recovery, never for interpreting an HTTP claimed hash.
func Restore(hash string, version int, verifiedAt int64, evidence Evidence) (Verified, error) {
	validHex := func(s string, size int) bool {
		b, err := hex.DecodeString(s)
		return err == nil && len(b) == size && strings.ToLower(s) == s
	}
	if version != VerificationVersion || !validHex(hash, 20) || verifiedAt <= 0 || evidence.SchemaVersion != 1 || !animefeed.ValidProvider(evidence.Provider) || evidence.PostKey == "" || len(evidence.PostKey) > 64 || !strings.HasPrefix(evidence.ContentFingerprint, "v1:") || !validHex(strings.TrimPrefix(evidence.ContentFingerprint, "v1:"), 32) {
		return Verified{}, ErrInvalid
	}
	if evidence.Method == "torrent_info" {
		if evidence.Provider == "garden" || !validHex(evidence.MetadataSHA256, 32) {
			return Verified{}, ErrInvalid
		}
	} else if evidence.Method != "source_magnet" || evidence.Provider != "garden" || evidence.MetadataSHA256 != "" {
		return Verified{}, ErrInvalid
	}
	return Verified{hash: hash, verifiedAt: verifiedAt, evidence: evidence}, nil
}

type TorrentReader interface {
	ReadTorrent(context.Context, string, string, string) ([]byte, error)
}

func Fingerprint(row animefeed.SourceRelease) (string, error) {
	raw, err := json.Marshal(row)
	if err != nil {
		return "", ErrInvalid
	}
	h := sha256.Sum256(raw)
	return "v1:" + hex.EncodeToString(h[:]), nil
}

// Verify accepts an observation produced by a controlled provider adapter.
// Mikan/AniBT require the actual torrent; Garden publishes a BT v1 magnet.
func Verify(ctx context.Context, provider string, row animefeed.SourceRelease, reader TorrentReader) (Verified, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return Verified{}, err
	}
	if !animefeed.ValidProvider(provider) || row.PostKey == "" || row.Title == "" {
		return Verified{}, ErrInvalid
	}
	fp, err := Fingerprint(row)
	if err != nil {
		return Verified{}, err
	}
	out := Verified{evidence: Evidence{SchemaVersion: 1, Provider: provider, PostKey: row.PostKey, ContentFingerprint: fp}}
	if provider == "garden" {
		if row.TorrentURL != "" || !strings.HasPrefix(row.PostKey, "rel_") {
			return Verified{}, ErrInvalid
		}
		out.hash, err = MagnetHash(row.MagnetHint)
		out.evidence.Method = "source_magnet"
	} else {
		if _, err := TorrentURL(provider, row.PostKey, row.TorrentURL); err != nil {
			return Verified{}, err
		}
		if reader == nil {
			return Verified{}, ErrInvalid
		}
		body, readErr := reader.ReadTorrent(ctx, provider, row.PostKey, row.TorrentURL)
		if readErr != nil {
			if ctx.Err() != nil {
				return Verified{}, ctx.Err()
			}
			return Verified{}, readErr
		}
		out.hash, err = TorrentHash(body)
		if err == nil && row.MagnetHint != "" {
			var hint string
			hint, err = MagnetHash(row.MagnetHint)
			if err == nil && hint != out.hash {
				err = ErrConflict
			}
		}
		digest := sha256.Sum256(body)
		out.evidence.Method, out.evidence.MetadataSHA256 = "torrent_info", hex.EncodeToString(digest[:])
	}
	if err != nil {
		return Verified{}, err
	}
	if err := ctx.Err(); err != nil {
		return Verified{}, err
	}
	out.verifiedAt = time.Now().UTC().UnixMilli()
	return out, nil
}

// MagnetHash accepts exactly one BT v1 xt; base32 and hex share one identity.
// Trackers and display names never enter a persisted submission URL.
func MagnetHash(raw string) (string, error) {
	if len(raw) == 0 || len(raw) > 16384 || strings.ContainsAny(raw, "\r\n\x00") {
		return "", ErrInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "magnet" || u.Host != "" || u.User != nil || u.Path != "" || u.Fragment != "" {
		return "", ErrUnsupported
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["xt"]) != 1 {
		return "", ErrInvalid
	}
	xt := q.Get("xt")
	if !strings.HasPrefix(strings.ToLower(xt), "urn:btih:") {
		return "", ErrUnsupported
	}
	hash := xt[len("urn:btih:"):]
	if len(hash) == 40 {
		if _, err := hex.DecodeString(hash); err == nil {
			return strings.ToLower(hash), nil
		}
	}
	if len(hash) == 32 {
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(hash))
		if err == nil && len(decoded) == 20 {
			return hex.EncodeToString(decoded), nil
		}
	}
	return "", ErrInvalid
}
