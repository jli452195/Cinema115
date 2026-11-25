package bot

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

const maxTelegramTextDocumentBytes = 512 << 10

var (
	errTelegramTextDocumentTooLarge    = fmt.Errorf("TXT 附件超过 %d KiB", maxTelegramTextDocumentBytes>>10)
	errTelegramTextDocumentInvalid     = errors.New("TXT 附件不是有效的 UTF-8 或带 BOM 的 UTF-16 文本")
	errTelegramTextDocumentUnavailable = errors.New("Telegram TXT 附件不可用，请重新发送")
)

type telegramTextDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	FileSize int64  `json:"file_size,omitempty"`
}

func (d *telegramTextDocument) isText() bool {
	if d == nil {
		return false
	}
	if extension := path.Ext(strings.TrimSpace(d.FileName)); extension != "" {
		return strings.EqualFold(extension, ".txt")
	}
	mediaType, _, err := mime.ParseMediaType(d.MIMEType)
	return err == nil && mediaType == "text/plain"
}

func (d *telegramTextDocument) validate() error {
	if !d.isText() || d.FileSize < 0 {
		return errTelegramTextDocumentInvalid
	}
	if d.FileSize > maxTelegramTextDocumentBytes {
		return errTelegramTextDocumentTooLarge
	}
	if strings.TrimSpace(d.FileID) == "" {
		return errTelegramTextDocumentUnavailable
	}
	return nil
}

func (b *Telegram) readTextDocument(ctx context.Context, d *telegramTextDocument) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	var file struct {
		Path string `json:"file_path"`
		Size int64  `json:"file_size"`
	}
	if err := b.call(ctx, "getFile", map[string]string{"file_id": d.FileID}, &file); err != nil {
		if errors.Is(err, errTelegramBadRequest) {
			return "", errTelegramTextDocumentUnavailable
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("读取 Telegram TXT 附件信息: %w", err)
	}
	if file.Size > maxTelegramTextDocumentBytes {
		return "", errTelegramTextDocumentTooLarge
	}
	if file.Size < 0 || !validTelegramFilePath(file.Path) {
		return "", errTelegramTextDocumentUnavailable
	}
	return b.downloadTextDocument(ctx, file.Path)
}

func validTelegramFilePath(filePath string) bool {
	// 只接受 Telegram 返回的相对路径，隔离下载地址的主机、查询和片段。
	if strings.ContainsAny(filePath, `\:%?#`) || strings.IndexFunc(filePath, unicode.IsControl) >= 0 {
		return false
	}
	for _, segment := range strings.Split(filePath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func (b *Telegram) downloadTextDocument(ctx context.Context, filePath string) (string, error) {
	endpoint, err := url.JoinPath(b.BaseURL, "file", "bot"+b.Token, filePath)
	if err != nil {
		return "", errTelegramTextDocumentUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", errTelegramTextDocumentUnavailable
	}
	client := *b.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		// 请求错误可能包含携带令牌的下载地址。
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("下载 Telegram TXT 附件失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode >= 300 && resp.StatusCode < 400 || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			return "", errTelegramTextDocumentUnavailable
		}
		return "", fmt.Errorf("下载 Telegram TXT 附件失败（HTTP %d）", resp.StatusCode)
	}
	if resp.ContentLength > maxTelegramTextDocumentBytes {
		return "", errTelegramTextDocumentTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTelegramTextDocumentBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("读取 Telegram TXT 附件失败")
	}
	if len(data) > maxTelegramTextDocumentBytes {
		return "", errTelegramTextDocumentTooLarge
	}
	return decodeTelegramTextDocument(data)
}

func decodeTelegramTextDocument(data []byte) (string, error) {
	var text string
	var err error
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		text, err = decodeTelegramUTF16(data[2:], binary.LittleEndian)
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		text, err = decodeTelegramUTF16(data[2:], binary.BigEndian)
	default:
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		if !utf8.Valid(data) {
			return "", errTelegramTextDocumentInvalid
		}
		text = string(data)
	}
	if err != nil {
		return "", err
	}
	if strings.IndexFunc(text, func(r rune) bool {
		return unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r'
	}) >= 0 {
		return "", errTelegramTextDocumentInvalid
	}
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), nil
}

func decodeTelegramUTF16(data []byte, order binary.ByteOrder) (string, error) {
	if len(data)%2 != 0 {
		return "", errTelegramTextDocumentInvalid
	}
	var text strings.Builder
	text.Grow(len(data) / 2)
	for len(data) >= 2 {
		r := rune(order.Uint16(data))
		data = data[2:]
		if utf16.IsSurrogate(r) {
			if len(data) < 2 {
				return "", errTelegramTextDocumentInvalid
			}
			r = utf16.DecodeRune(r, rune(order.Uint16(data)))
			if r == unicode.ReplacementChar {
				return "", errTelegramTextDocumentInvalid
			}
			data = data[2:]
		}
		text.WriteRune(r)
	}
	return text.String(), nil
}
