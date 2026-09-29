package nanshare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Resource deliberately excludes share URLs, cookies and upstream user objects.
type Resource struct {
	Source           string   `json:"source"`
	Key              string   `json:"key"`
	Slug             string   `json:"slug"`
	PanType          string   `json:"pan_type"`
	Name             string   `json:"name"`
	Remark           string   `json:"remark"`
	Title            string   `json:"title"`
	Resolution       []string `json:"video_resolution"`
	Sources          []string `json:"resource_sources"`
	Size             string   `json:"share_size"`
	Publisher        string   `json:"publisher"`
	PublishedAt      string   `json:"publish_time"`
	SubtitleLanguage []string `json:"subtitle_language"`
	SubtitleType     []string `json:"subtitle_type"`
	Points           *int     `json:"unlock_points"`
	Unlocked         bool     `json:"is_unlocked"`
	UnlockedUsers    int      `json:"unlocked_users_count"`
	Official         bool     `json:"is_official"`
	Validity         string   `json:"validate_status"`
}

func (c *Client) Search(ctx context.Context, account, kind string, id int64) ([]Resource, error) {
	if account == "" || (kind != "movie" && kind != "tv") || id < 1 || id > 9007199254740991 {
		return nil, ErrInput
	}
	var response struct {
		Resources []Resource `json:"resources"`
	}
	if err := c.request(ctx, http.MethodPost, "/discover/resource/hdhive", map[string]any{"tmdb_id": id, "media_type": kind, "account_name": account}, &response); err != nil {
		return nil, err
	}
	if response.Resources == nil {
		return nil, ErrFormat
	}
	items := make([]Resource, 0, len(response.Resources))
	seen := make(map[string]bool, len(response.Resources))
	for _, item := range response.Resources {
		if item.Source != "hdhive" || item.Key == "" || item.PanType == "" {
			return nil, ErrFormat
		}
		if item.PanType != "115" {
			continue
		}
		if item.Slug == "" || len(item.Slug) > 256 {
			return nil, ErrFormat
		}
		if !seen[item.Slug] {
			seen[item.Slug] = true
			items = append(items, item)
		}
	}
	return items, nil
}

type Folder struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Path  string `json:"path"`
}

type ShareOptions struct {
	Version   string   `json:"version"`
	AccountID string   `json:"accountId"`
	Modes     []string `json:"modes"`
	Folders   []Folder `json:"folders"`
}

type TransferFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type TransferOptions struct {
	AccountID string           `json:"accountId"`
	Folders   []TransferFolder `json:"folders"`
}

type TransferResult struct {
	Code    string `json:"code"`
	Count   int    `json:"count"`
	Message string `json:"message"`
	OK      bool   `json:"ok"`
	Success bool   `json:"success"`
}

func (c *Client) TransferOptions(ctx context.Context) (TransferOptions, error) {
	var response struct {
		Data struct {
			AccountID string `json:"use_pan115_config"`
			Folders   []struct {
				ID   json.RawMessage `json:"id"`
				CID  json.RawMessage `json:"cid"`
				Name string          `json:"name"`
				Path string          `json:"path"`
			} `json:"transfer_folders"`
		} `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/transfer/115/config", nil, &response); err != nil {
		return TransferOptions{}, err
	}
	result := TransferOptions{AccountID: strings.TrimSpace(response.Data.AccountID), Folders: make([]TransferFolder, 0, len(response.Data.Folders))}
	for _, folder := range response.Data.Folders {
		id := transferFolderID(folder.CID)
		if id == "" {
			id = transferFolderID(folder.ID)
		}
		if id == "" {
			continue
		}
		result.Folders = append(result.Folders, TransferFolder{ID: id, Name: strings.TrimSpace(folder.Name), Path: strings.TrimSpace(folder.Path)})
	}
	if result.AccountID == "" || len(result.Folders) == 0 {
		return TransferOptions{}, fmt.Errorf("%w：请先配置 115 转存助手账号和目录", ErrInput)
	}
	return result, nil
}

func transferFolderID(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) != nil {
		id = string(raw)
	}
	if id == "" || len(id) > 32 || strings.Trim(id, "0123456789") != "" {
		return ""
	}
	return id
}

func decodeTransferResult(data []byte, result *TransferResult) error {
	var response struct {
		envelope
		Count   int    `json:"count"`
		Message string `json:"message"`
		Results []struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &response) != nil || response.OK == nil || response.Success == nil {
		return ErrFormat
	}
	if *response.OK != *response.Success || response.Count < 0 {
		return ErrFormat
	}
	result.Count = response.Count
	if *response.OK && *response.Success {
		if response.Count == 0 || response.Count != len(response.Results) {
			return ErrFormat
		}
		for _, item := range response.Results {
			if !item.Success {
				return ErrFormat
			}
		}
		result.OK, result.Success, result.Code, result.Message = true, true, "ok", fmt.Sprintf("转存成功，共 %d 个；文件已保存到所选目录", response.Count)
		return nil
	}
	result.Code, result.Message = "failed", "转存失败，请在 NanShare 查看详细结果"
	if strings.Contains(response.Message, "无法确认资源积分") {
		result.Code, result.Message = "points_unknown", "无法确认资源积分，已按 NanShare 积分限制策略跳过"
		return nil
	}
	if strings.Contains(response.Message, "积分") {
		result.Code, result.Message = "points_limit", "资源积分不满足 NanShare 当前策略，请检查积分余额与限制设置"
		return nil
	}
	for _, item := range response.Results {
		if strings.Contains(item.Message, "已经转存过该文件") {
			result.Code, result.Message = "already_saved", "该文件已转存过，请在网盘核查；未重复转存"
		} else if !item.Success {
			result.Code, result.Message = "failed", "转存失败，请在 NanShare 查看详细结果"
			break
		}
	}
	if response.Count > 0 {
		result.Code, result.Message = "partial", fmt.Sprintf("已成功转存 %d 个，其余未完成，请在 NanShare 核查，勿重复提交", response.Count)
	}
	return nil
}

func (c *Client) TransferResource(ctx context.Context, source, panType, folderID string, links []string, slug string) (TransferResult, error) {
	if source != "hdhive" || panType != "115" || strings.TrimSpace(folderID) == "" || strings.TrimSpace(slug) == "" || len(slug) > 256 || len(links) > 256 {
		return TransferResult{}, ErrInput
	}
	var result TransferResult
	if err := c.request(ctx, http.MethodPost, "/discover/resource/transfer", map[string]any{"source": source, "pan_type": panType, "folder_id": folderID, "links": links, "slug": slug}, &result); err != nil {
		return TransferResult{}, err
	}
	if result.Message == "" {
		return TransferResult{}, ErrFormat
	}
	return result, nil
}

// ShareOptions reads the feature's explicitly persisted remote account binding.
// No local 115 account or credential is used or forwarded to NanShare.
func (c *Client) ShareOptions(ctx context.Context, searchAccount string) (ShareOptions, error) {
	var helper struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/hdhive/config", nil, &helper); err != nil {
		return ShareOptions{}, err
	}
	var re0 struct {
		Enabled bool   `json:"enabled"`
		Account string `json:"use_hdhive_account"`
	}
	if json.Unmarshal(helper.Data, &re0) != nil {
		return ShareOptions{}, ErrFormat
	}
	if !re0.Enabled || re0.Account == "" || re0.Account != searchAccount {
		return ShareOptions{}, fmt.Errorf("%w：请在 NanShare 启用 RE0 解析，并为搜索和解析选择同一 RE0 账号", ErrChanged)
	}
	var response struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.request(ctx, http.MethodGet, "/strm/share/config", nil, &response); err != nil {
		return ShareOptions{}, err
	}
	var config ShareConfig
	if json.Unmarshal(response.Data, &config) != nil || config.Enabled == nil || config.Normal == nil || config.Reshare == nil {
		return ShareOptions{}, ErrFormat
	}
	if !*config.Enabled || strings.TrimSpace(config.AccountID) == "" {
		return ShareOptions{}, fmt.Errorf("%w：请在 NanShare 启用分享 STRM 并明确绑定 115 账号", ErrInput)
	}
	result := ShareOptions{AccountID: config.AccountID, Modes: make([]string, 0, 2), Folders: make([]Folder, 0, len(config.Folders))}
	if *config.Normal {
		result.Modes = append(result.Modes, "normal")
	}
	if *config.Reshare && strings.TrimSpace(config.OpenAccountID) != "" {
		result.Modes = append(result.Modes, "reshare")
	}
	for index, folder := range config.Folders {
		if strings.TrimSpace(folder.Path) != "" {
			result.Folders = append(result.Folders, Folder{Index: index, Name: folder.Name, Path: folder.Path})
		}
	}
	if len(result.Modes) == 0 || len(result.Folders) == 0 {
		return ShareOptions{}, fmt.Errorf("%w：请在 NanShare 配置生成模式和输出目录，转存模式还需绑定 115 Open 账号", ErrInput)
	}
	// Hash the entire config in memory; secrets in it are never returned or saved.
	var canonical map[string]any
	if json.Unmarshal(response.Data, &canonical) != nil {
		return ShareOptions{}, ErrFormat
	}
	var helperCanonical map[string]any
	if json.Unmarshal(helper.Data, &helperCanonical) != nil {
		return ShareOptions{}, ErrFormat
	}
	encoded, err := json.Marshal([]any{canonical, helperCanonical})
	if err != nil {
		return ShareOptions{}, ErrFormat
	}
	sum := sha256.Sum256(encoded)
	result.Version = hex.EncodeToString(sum[:])
	return result, nil
}

func (c *Client) Submit(ctx context.Context, slug string, mode string, folder int) error {
	if slug == "" || (mode != "normal" && mode != "reshare") || folder < 0 {
		return ErrInput
	}
	var response envelope
	return c.request(ctx, http.MethodPost, "/hdhive/resource/save", map[string]any{"slug": slug, "path_index": folder, "enable_reshare": mode == "reshare"}, &response)
}
