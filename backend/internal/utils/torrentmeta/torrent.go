// Package torrentmeta reads bounded BT v1 metadata without network or filesystem access.
package torrentmeta

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

const MaxBytes = 4 << 20

var (
	ErrInvalid     = errors.New("invalid torrent metadata")
	ErrUnsupported = errors.New("unsupported torrent metadata version")
)

// File paths use '/' relative to Name for multi-file torrents. For a single
// file, Path equals Name. Order and sizes are those declared in the torrent;
// no media filtering, renaming or path cleaning takes place here.
type File struct {
	Path string
	Size int64
}

type Metadata struct {
	InfoHash  string
	Name      string
	MultiFile bool
	Files     []File
}

// Parse returns a detached file manifest and the hash of the original info
// bytes. Explicit UTF-8 names take precedence. Only BT v1 is supported; hybrid
// torrents also remain unsupported by the existing download workflow.
func Parse(body []byte) (Metadata, error) {
	if len(body) == 0 || len(body) > MaxBytes || body[0] != 'd' {
		return Metadata{}, ErrInvalid
	}
	if err := checkEncoding(body); err != nil {
		return Metadata{}, err
	}
	// Decode only info: tracker fields do not affect identity or the manifest.
	var envelope struct {
		Info bencode.Bytes `bencode:"info"`
	}
	if err := decode(body, &envelope); err != nil {
		return Metadata{}, err
	}
	if err := checkV1Shape(envelope.Info); err != nil {
		return Metadata{}, err
	}
	mi := metainfo.MetaInfo{InfoBytes: envelope.Info}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return Metadata{}, fmt.Errorf("decode info: %w", ErrInvalid)
	}
	if !validPart(info.Name) || !validPart(info.BestName()) || info.PieceLength <= 0 || len(info.Pieces)%20 != 0 {
		return Metadata{}, ErrInvalid
	}
	files := info.UpvertedFiles()
	out := Metadata{Name: info.BestName(), MultiFile: info.IsDir(), Files: make([]File, 0, len(files))}
	seen := make(map[string]struct{}, len(files))
	var total int64
	for _, file := range files {
		if file.Length < 0 || total > math.MaxInt64-file.Length || !validParts(file.Path) || !validParts(file.PathUtf8) {
			return Metadata{}, ErrInvalid
		}
		path := file.DisplayPath(&info)
		if _, exists := seen[path]; exists {
			return Metadata{}, ErrInvalid
		}
		seen[path] = struct{}{}
		total += file.Length
		out.Files = append(out.Files, File{Path: path, Size: file.Length})
	}
	count := total / info.PieceLength
	if total%info.PieceLength != 0 {
		count++
	}
	if total <= 0 || count != int64(len(info.Pieces)/20) {
		return Metadata{}, ErrInvalid
	}
	out.InfoHash = mi.HashInfoBytes().HexString()
	return out, nil
}

func decode(raw []byte, target any) error {
	d := bencode.NewDecoder(bytes.NewReader(raw))
	d.MaxStrLen = MaxBytes
	if err := d.Decode(target); err != nil || d.Offset != int64(len(raw)) {
		return fmt.Errorf("decode bencode: %w", ErrInvalid)
	}
	return nil
}

// The library permits singleton-list coercion and represents absent lengths as
// zero. Check required wire types/presence before decoding into metainfo.Info.
func checkV1Shape(raw []byte) error {
	if len(raw) == 0 || raw[0] != 'd' {
		return ErrInvalid
	}
	var fields map[string]any
	if err := decode(raw, &fields); err != nil {
		return err
	}
	if _, exists := fields["meta version"]; exists {
		return ErrUnsupported
	}
	_, nameOK := fields["name"].(string)
	_, pieceLengthOK := fields["piece length"].(int64)
	_, piecesOK := fields["pieces"].(string)
	if !nameOK || !pieceLengthOK || !piecesOK {
		return ErrInvalid
	}
	if alias, exists := fields["name.utf-8"]; exists {
		if _, ok := alias.(string); !ok {
			return ErrInvalid
		}
	}
	length, single := fields["length"]
	rawFiles, multi := fields["files"]
	if single == multi {
		return ErrInvalid
	}
	if single {
		if _, ok := length.(int64); !ok {
			return ErrInvalid
		}
		return nil
	}
	files, ok := rawFiles.([]any)
	if !ok || len(files) == 0 {
		return ErrInvalid
	}
	for _, rawFile := range files {
		file, ok := rawFile.(map[string]any)
		if !ok {
			return ErrInvalid
		}
		if _, ok := file["length"].(int64); !ok || !stringList(file["path"]) {
			return ErrInvalid
		}
		if alias, exists := file["path.utf-8"]; exists && !stringList(alias) {
			return ErrInvalid
		}
	}
	return nil
}

func stringList(value any) bool {
	parts, ok := value.([]any)
	if !ok || len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if _, ok := part.(string); !ok {
			return false
		}
	}
	return true
}

func validPart(part string) bool {
	return part != "" && part != "." && part != ".." && !strings.ContainsAny(part, "/\\\x00") && !(len(part) >= 2 && part[1] == ':')
}

func validParts(parts []string) bool {
	for _, part := range parts {
		if !validPart(part) {
			return false
		}
	}
	return true
}
