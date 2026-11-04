package p115

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// DriveMetadataBatchLimit 是本地保守分组大小，不代表上游允许的最大数量。
const DriveMetadataBatchLimit = 32

// DriveMetadataLink 只在本轮内存使用，下载地址和预览地址均不得持久保存。
type DriveMetadataLink struct {
	File    ShareFile
	URL     string
	Preview bool
	Err     error `json:"-"`
}

// PrepareDriveMetadata 复用图片预览地址，并将其余附件合并为一次只读取链。
func (c *ShareClient) PrepareDriveMetadata(ctx context.Context, files []ShareFile, cookie, imageMode string) (map[string]DriveMetadataLink, error) {
	return prepareDriveMetadata(ctx, files, cookie, imageMode, c.driveMetadataDownloadLinks)
}

func prepareDriveMetadata(ctx context.Context, files []ShareFile, cookie, imageMode string, readBatch func(context.Context, []ShareFile, string) (map[string]DriveMetadataLink, error)) (map[string]DriveMetadataLink, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(files) == 0 || len(files) > DriveMetadataBatchLimit || CookieUserID(cookie) == "" || imageMode != "" && imageMode != "original" && imageMode != "cdn" {
		return nil, fmt.Errorf("附件取链批次或图片策略无效")
	}
	links := make(map[string]DriveMetadataLink, len(files))
	seenPC := make(map[string]bool, len(files))
	originals := make([]ShareFile, 0, len(files))
	for _, file := range files {
		if !validMetadataFile(file) || seenPC[file.PickCode] {
			return nil, fmt.Errorf("附件身份、大小或取链信息无效（最大 32 MB）")
		}
		if _, exists := links[file.ID]; exists {
			return nil, fmt.Errorf("附件文件编号重复")
		}
		seenPC[file.PickCode] = true
		link := DriveMetadataLink{File: file}
		if DriveMetadataUsesPreview(file, imageMode) {
			link.URL, link.Preview = metadataPreviewURL(file), true
		}
		links[file.ID] = link
		if !link.Preview {
			originals = append(originals, file)
		}
	}
	if len(originals) > 0 {
		prepared, err := readBatch(ctx, originals, cookie)
		if err != nil {
			return nil, err
		}
		for _, file := range originals {
			link, found := prepared[file.ID]
			if !found {
				link = DriveMetadataLink{File: file, Err: fmt.Errorf("附件批量取链未返回该文件")}
			}
			links[file.ID] = link
		}
	}
	return links, nil
}

func validMetadataFile(file ShareFile) bool {
	return !file.Directory && validDriveID(file.ID, false) && instantPickCode.MatchString(file.PickCode) && instantHash.MatchString(file.SHA1) && file.Size > 0 && file.Size <= MaxMetadataSize && strings.TrimSpace(file.Name) != ""
}

func (c *ShareClient) driveMetadataDownloadLinks(ctx context.Context, files []ShareFile, cookie string) (map[string]DriveMetadataLink, error) {
	codes := make([]string, 0, len(files))
	for _, file := range files {
		codes = append(codes, file.PickCode)
	}
	payload, err := json.Marshal(map[string]string{"pickcode": strings.Join(codes, ","), "user_id": CookieUserID(cookie)})
	if err != nil {
		return nil, fmt.Errorf("构造附件批量取链: %w", err)
	}
	// 批量能力只供附件使用，播放单文件取链仍保持原来的严格契约。
	raw, err := c.requestEnvelopeWithUA(ctx, http.MethodPost, c.AppURL+"/app/chrome/downurl", cookie, url.Values{"data": {RSAEncrypt(payload)}}, PlaybackUserAgent)
	if err != nil {
		return nil, fmt.Errorf("附件批量取链失败: %w", err)
	}
	fields, err := animeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("附件批量取链响应无效")
	}
	var encoded string
	if json.Unmarshal(fields["data"], &encoded) != nil {
		return nil, fmt.Errorf("附件批量取链响应无效")
	}
	decoded, err := RSADecrypt(encoded)
	if err != nil {
		return nil, fmt.Errorf("附件批量取链解码失败")
	}
	return parseDriveMetadataLinks(decoded, files)
}

func parseDriveMetadataLinks(raw []byte, files []ShareFile) (map[string]DriveMetadataLink, error) {
	rows, err := animeObject(raw)
	if err != nil {
		return nil, fmt.Errorf("附件批量取链响应格式无效或重复")
	}
	links := make(map[string]DriveMetadataLink, len(files))
	for _, file := range files {
		links[file.ID] = DriveMetadataLink{File: file}
	}
	for id := range rows {
		if _, exists := links[id]; !exists {
			return nil, fmt.Errorf("附件批量取链包含未知文件编号")
		}
	}
	for _, file := range files {
		link := links[file.ID]
		rawRow, found := rows[file.ID]
		if !found {
			link.Err = fmt.Errorf("附件批量取链未返回该文件")
			links[file.ID] = link
			continue
		}
		row, err := animeObject(rawRow)
		if err != nil && bytes.HasPrefix(bytes.TrimSpace(rawRow), []byte("{")) {
			return nil, fmt.Errorf("附件批量取链文件字段重复或无效")
		}
		urlFields, err := animeObject(row["url"])
		if err != nil && bytes.HasPrefix(bytes.TrimSpace(row["url"]), []byte("{")) {
			return nil, fmt.Errorf("附件批量取链地址字段重复或无效")
		}
		// 已知文件失效只影响自身，仍对其余附件逐项核验且不补发请求。
		link.URL, link.Err = parseDriveMetadataRow(file, row, urlFields)
		links[file.ID] = link
	}
	return links, nil
}

func parseDriveMetadataRow(file ShareFile, row, urlFields map[string]json.RawMessage) (string, error) {
	var pc, name, hash string
	var size json.Number
	// 顶层映射键是精确文件身份；可选的重复编号字段存在时必须一致。
	if rawID, exists := row["file_id"]; exists {
		var id string
		if json.Unmarshal(rawID, &id) != nil {
			var number json.Number
			if json.Unmarshal(rawID, &number) == nil {
				id = number.String()
			}
		}
		if id != file.ID {
			return "", fmt.Errorf("附件批量取链文件编号冲突")
		}
	}
	if json.Unmarshal(row["pick_code"], &pc) != nil || pc != file.PickCode || json.Unmarshal(row["file_name"], &name) != nil || name != file.Name || json.Unmarshal(row["file_size"], &size) != nil || json.Unmarshal(row["sha1"], &hash) != nil || !instantHash.MatchString(hash) || !strings.EqualFold(hash, file.SHA1) {
		return "", fmt.Errorf("附件批量取链身份与目录列表不一致")
	}
	actualSize, err := size.Int64()
	if err != nil || actualSize != file.Size {
		return "", fmt.Errorf("附件批量取链大小与目录列表不一致")
	}
	var link string
	if json.Unmarshal(urlFields["url"], &link) != nil || !AllowedMediaURL(link) {
		return "", fmt.Errorf("附件批量取链地址无效")
	}
	u, _ := url.Parse(link)
	for _, flag := range u.Query()["f"] {
		if flag != "" && flag != "0" && flag != "1" {
			return "", fmt.Errorf("附件下载需要不支持的鉴权信息")
		}
	}
	return link, nil
}

func metadataImageFormat(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".png":
		return "png"
	case ".gif":
		return "gif"
	}
	return ""
}

// DriveMetadataUsesPreview 与取链准备共用判断，调用者可据此准确分配接口请求额度。
func DriveMetadataUsesPreview(file ShareFile, imageMode string) bool {
	return imageMode == "cdn" && validMetadataFile(file) && metadataImageFormat(file.Name) != "" && metadataPreviewURL(file) != ""
}

func metadataPreviewURL(file ShareFile) string {
	if !allowedMetadataImageURL(file.Thumbnail) {
		return ""
	}
	u, _ := url.Parse(file.Thumbnail)
	for _, hash := range u.Query()["sha1"] {
		if hash != "" && !strings.EqualFold(hash, file.SHA1) {
			return ""
		}
	}
	switch strings.ToLower(u.Hostname()) {
	case "thumb.115.com", "thumbapi.115.com":
		identity := strings.SplitN(path.Base(u.Path), "_", 2)
		if !strings.EqualFold(identity[0], file.SHA1) {
			return ""
		}
		query := u.Query()
		query.Set("sha1", identity[0])
		size := "0"
		if len(identity) == 2 && identity[1] != "" {
			size = identity[1]
		}
		query.Set("size", size)
		return "https://imgjump.115.com/?" + query.Encode()
	}
	return file.Thumbnail
}

func allowedMetadataImageURL(raw string) bool {
	if !AllowedMediaURL(raw) {
		return false
	}
	u, _ := url.Parse(raw)
	if u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "webapi.115.com" || host == "proapi.115.com" {
		return false
	}
	base := strings.ToLower(path.Base(u.Path))
	for _, denied := range []string{"403.jpg", "404.jpg", "error.jpg", "error.png", "error.gif"} {
		if base == denied {
			return false
		}
	}
	return true
}

// ReadDriveMetadataLink 下载已核验的链接；预览图的字节大小独立于网盘原文件。
func (c *ShareClient) ReadDriveMetadataLink(ctx context.Context, link DriveMetadataLink) ([]byte, error) {
	if link.Err != nil {
		return nil, link.Err
	}
	if !validMetadataFile(link.File) || link.Preview && (metadataImageFormat(link.File.Name) == "" || !allowedMetadataImageURL(link.URL)) {
		return nil, fmt.Errorf("附件下载身份或图片预览地址无效")
	}
	data, err := c.readMetadataURLMode(ctx, link.URL, link.Preview)
	if err != nil {
		return nil, err
	}
	if !link.Preview {
		if int64(len(data)) != link.File.Size {
			return nil, fmt.Errorf("附件下载不完整")
		}
		return data, nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != metadataImageFormat(link.File.Name) || config.Width <= 0 || config.Height <= 0 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 16000000 {
		return nil, fmt.Errorf("图片预览内容、格式或尺寸无效")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("图片预览内容不完整")
	}
	return data, nil
}
