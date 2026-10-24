package torrentmeta

import (
	"bytes"
	"strconv"
)

// The upstream decoder has no depth/node limit and its typed dictionaries
// accept duplicate or unsorted keys. This syntax gate bounds work before
// library decoding; it builds no metadata tree or file manifest.
func checkEncoding(data []byte) error {
	g := encodingGuard{data: data}
	if err := g.value(0); err != nil || g.pos != len(data) {
		return ErrInvalid
	}
	return nil
}

type encodingGuard struct {
	data       []byte
	pos, nodes int
}

func (g *encodingGuard) value(depth int) error {
	g.nodes++
	if depth > 32 || g.nodes > 32768 || g.pos >= len(g.data) {
		return ErrInvalid
	}
	switch g.data[g.pos] {
	case 'i':
		g.pos++
		end := bytes.IndexByte(g.data[g.pos:], 'e')
		if end < 1 || end > 20 {
			return ErrInvalid
		}
		raw := string(g.data[g.pos : g.pos+end])
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != raw {
			return ErrInvalid
		}
		g.pos += end + 1
	case 'd', 'l':
		dict := g.data[g.pos] == 'd'
		g.pos++
		var previous []byte
		for g.pos < len(g.data) && g.data[g.pos] != 'e' {
			if dict {
				g.nodes++
				if g.nodes > 32768 {
					return ErrInvalid
				}
				key, err := g.text()
				if err != nil || previous != nil && bytes.Compare(previous, key) >= 0 {
					return ErrInvalid
				}
				previous = key
			}
			if err := g.value(depth + 1); err != nil {
				return err
			}
		}
		if g.pos >= len(g.data) {
			return ErrInvalid
		}
		g.pos++
	default:
		_, err := g.text()
		return err
	}
	return nil
}

func (g *encodingGuard) text() ([]byte, error) {
	if g.pos >= len(g.data) || g.data[g.pos] < '0' || g.data[g.pos] > '9' {
		return nil, ErrInvalid
	}
	colon := bytes.IndexByte(g.data[g.pos:], ':')
	if colon < 1 || colon > 8 {
		return nil, ErrInvalid
	}
	raw := string(g.data[g.pos : g.pos+colon])
	n, err := strconv.Atoi(raw)
	g.pos += colon + 1
	if err != nil || strconv.Itoa(n) != raw || n < 0 || n > len(g.data)-g.pos {
		return nil, ErrInvalid
	}
	text := g.data[g.pos : g.pos+n]
	g.pos += n
	return text, nil
}
