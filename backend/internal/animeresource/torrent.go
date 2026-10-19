package animeresource

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"math"
	"strconv"
)

type bvalue struct {
	kind       byte
	start, end int
	text       []byte
	number     int64
	dict       map[string]bvalue
	list       []bvalue
}

type bparser struct {
	data  []byte
	pos   int
	nodes int
}

// TorrentHash hashes the exact original info byte slice, never re-encodes it.
// Strict bencode bounds reject duplicate keys, malformed/trailing data, depth
// bombs, v2/hybrid torrents and incomplete BT v1 metadata.
func TorrentHash(body []byte) (string, error) {
	if len(body) == 0 || len(body) > MaxTorrentBytes {
		return "", ErrInvalid
	}
	p := bparser{data: body}
	root, err := p.value(0)
	if err != nil || p.pos != len(body) || root.kind != 'd' {
		return "", ErrInvalid
	}
	info := root.dict["info"]
	if info.kind != 'd' {
		return "", ErrInvalid
	}
	if _, exists := info.dict["meta version"]; exists {
		return "", ErrUnsupported
	}
	name, pieceLength, pieces := info.dict["name"], info.dict["piece length"], info.dict["pieces"]
	if name.kind != 's' || len(name.text) == 0 || pieceLength.kind != 'i' || pieceLength.number <= 0 || pieces.kind != 's' || len(pieces.text)%20 != 0 {
		return "", ErrInvalid
	}
	length, single := info.dict["length"]
	files, multi := info.dict["files"]
	if single == multi {
		return "", ErrInvalid
	}
	var total int64
	if single {
		if length.kind != 'i' || length.number < 0 {
			return "", ErrInvalid
		}
		total = length.number
	} else {
		if files.kind != 'l' || len(files.list) == 0 {
			return "", ErrInvalid
		}
		for _, file := range files.list {
			n, path := file.dict["length"], file.dict["path"]
			if file.kind != 'd' || n.kind != 'i' || n.number < 0 || total > math.MaxInt64-n.number || path.kind != 'l' || len(path.list) == 0 {
				return "", ErrInvalid
			}
			for _, part := range path.list {
				if part.kind != 's' || len(part.text) == 0 {
					return "", ErrInvalid
				}
			}
			total += n.number
		}
	}
	count := total / pieceLength.number
	if total%pieceLength.number != 0 {
		count++
	}
	if total <= 0 || count != int64(len(pieces.text)/20) {
		return "", ErrInvalid
	}
	h := sha1.Sum(body[info.start:info.end])
	return hex.EncodeToString(h[:]), nil
}

func (p *bparser) value(depth int) (bvalue, error) {
	v := bvalue{start: p.pos}
	p.nodes++
	if depth > 32 || p.nodes > 32768 || p.pos >= len(p.data) {
		return v, ErrInvalid
	}
	v.kind = p.data[p.pos]
	switch v.kind {
	case 'i':
		p.pos++
		end := bytes.IndexByte(p.data[p.pos:], 'e')
		if end < 1 || end > 20 {
			return v, ErrInvalid
		}
		raw := string(p.data[p.pos : p.pos+end])
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != raw {
			return v, ErrInvalid
		}
		v.number = n
		p.pos += end + 1
	case 'd', 'l':
		p.pos++
		if v.kind == 'd' {
			v.dict = make(map[string]bvalue)
		}
		var previous []byte
		for p.pos < len(p.data) && p.data[p.pos] != 'e' {
			var key bvalue
			if v.kind == 'd' {
				var err error
				key, err = p.value(depth + 1)
				if err != nil || key.kind != 's' || previous != nil && bytes.Compare(previous, key.text) >= 0 {
					return v, ErrInvalid
				}
				previous = key.text
			}
			child, err := p.value(depth + 1)
			if err != nil {
				return v, err
			}
			if v.kind == 'd' {
				v.dict[string(key.text)] = child
			} else {
				v.list = append(v.list, child)
			}
		}
		if p.pos >= len(p.data) {
			return v, ErrInvalid
		}
		p.pos++
	default:
		if v.kind < '0' || v.kind > '9' {
			return v, ErrInvalid
		}
		colon := bytes.IndexByte(p.data[p.pos:], ':')
		if colon < 1 || colon > 8 {
			return v, ErrInvalid
		}
		raw := string(p.data[p.pos : p.pos+colon])
		n, err := strconv.Atoi(raw)
		p.pos += colon + 1
		if err != nil || strconv.Itoa(n) != raw || n < 0 || n > len(p.data)-p.pos {
			return v, ErrInvalid
		}
		v.kind, v.text = 's', p.data[p.pos:p.pos+n]
		p.pos += n
	}
	v.end = p.pos
	return v, nil
}
