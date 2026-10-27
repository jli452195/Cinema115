package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Only locally generated diagnostics may cross the playback log boundary.
type PlaybackCompletionError struct{ Reason string }

func (e *PlaybackCompletionError) Error() string { return e.Reason }
func completionFailure(reason string) error      { return &PlaybackCompletionError{Reason: reason} }

func (c *ShareClient) CompletePlaybackUpload(ctx context.Context, info *DownloadInfo, cid string, result *InstantUploadResult, cookie, ua string) (DrivePlaybackFile, string, error) {
	return completePlaybackReceipt(info, cid, result,
		func(pc string) (*DownloadInfo, string, error) { return c.pickCodeInfo(ctx, pc, cookie, ua) },
		func(id string) (DrivePlaybackFile, error) { return c.playbackFileInfo(ctx, id, cookie) },
	)
}

// Use the exact receipt identity, never the search index of a newly uploaded file.
func completePlaybackReceipt(info *DownloadInfo, cid string, result *InstantUploadResult, readLink func(string) (*DownloadInfo, string, error), readFile func(string) (DrivePlaybackFile, error)) (DrivePlaybackFile, string, error) {
	if result == nil || info == nil || !instantHash.MatchString(info.SHA1) || !validDriveID(cid, true) {
		return DrivePlaybackFile{}, "", completionFailure("秒传回执或源 SHA1 无效")
	}
	pc := result.PickCode
	var exact *DrivePlaybackFile
	if !instantPickCode.MatchString(pc) {
		if !validDriveID(result.FileID, false) {
			return DrivePlaybackFile{}, "", completionFailure("秒传成功回执缺少有效 fileID 和 pickcode")
		}
		f, err := readFile(result.FileID)
		if err != nil {
			return f, "", err
		}
		exact = &f
		pc = f.PickCode
	}
	got, id, err := readLink(pc)
	if err != nil {
		return DrivePlaybackFile{}, "", completionFailure("目标 pickcode 取链失败：" + SharePlaybackFailureReason(err))
	}
	if got == nil || !validDriveID(id, false) || (result.FileID != "" && id != result.FileID) {
		return DrivePlaybackFile{}, "", completionFailure("目标取链文件 ID 与秒传回执不一致")
	}
	if got.SHA1 != "" && (!instantHash.MatchString(got.SHA1) || !strings.EqualFold(got.SHA1, info.SHA1)) {
		return DrivePlaybackFile{}, "", completionFailure("目标取链响应 SHA1 与源文件不一致")
	}
	f := DrivePlaybackFile{ID: id, CID: cid, Name: got.Name, Size: got.Size, SHA1: got.SHA1, PickCode: pc}
	if got.SHA1 == "" && exact == nil {
		v, err := readFile(id)
		if err != nil {
			return f, "", err
		}
		exact = &v
	}
	if exact != nil {
		if exact.ID != id || exact.PickCode != pc {
			return f, "", completionFailure("目标精确查询的文件标识与取链响应不一致")
		}
		if !instantHash.MatchString(exact.SHA1) || !strings.EqualFold(exact.SHA1, info.SHA1) {
			return f, "", completionFailure("目标精确查询 SHA1 缺失或与源文件不一致")
		}
		f = *exact
		if f.CID == "" {
			f.CID = cid
		}
	}
	if !AllowedDirectPlaybackURL(got.URL) {
		return f, "", completionFailure("目标直链无效或要求额外 Cookie")
	}
	return f, got.URL, nil
}

func (c *ShareClient) playbackFileInfo(ctx context.Context, id, cookie string) (DrivePlaybackFile, error) {
	if !validDriveID(id, false) {
		return DrivePlaybackFile{}, completionFailure("目标文件 ID 无效")
	}
	q := url.Values{"file_id": {id}}
	raw, err := c.playbackReader(cookie).requestEnvelope(ctx, http.MethodGet, c.WebURL+"/files/get_info?"+q.Encode(), cookie, nil)
	if err != nil {
		return DrivePlaybackFile{}, fmt.Errorf("目标 fileID 精确查询失败: %w", err)
	}
	var resp struct {
		Data []struct {
			Area      json.Number `json:"aid"`
			ID        json.Number `json:"file_id"`
			CID       json.Number `json:"category_id"`
			PC        string      `json:"pick_code"`
			SHA       string      `json:"sha1"`
			Name      string      `json:"file_name"`
			Size      json.Number `json:"file_size"`
			FID       json.Number `json:"fid"`
			Parent    json.Number `json:"cid"`
			PickCode  string      `json:"pc"`
			Hash      string      `json:"sha"`
			ShortName string      `json:"n"`
			ShortSize json.Number `json:"s"`
		}
	}
	if json.Unmarshal(raw, &resp) != nil || len(resp.Data) != 1 {
		return DrivePlaybackFile{}, completionFailure("目标精确查询响应数量或格式无效")
	}
	f := resp.Data[0]
	// get_info uses compact fields on actual drive records, including permanent
	// deletion tombstones (aid=120). Do not confuse a successful metadata read
	// with an existing downloadable file. Reject conflicting aliases first.
	merge := func(a, b string) (string, bool) {
		if a != "" && b != "" && a != b {
			return "", false
		}
		if a != "" {
			return a, true
		}
		return b, true
	}
	idValue, idOK := merge(f.ID.String(), f.FID.String())
	if !idOK || idValue != id {
		return DrivePlaybackFile{}, completionFailure("目标精确查询文件 ID 不匹配")
	}
	if f.Area == "120" {
		return DrivePlaybackFile{}, ErrDriveFileMissing
	}
	if f.Area != "" && f.Area != "1" && f.Area != "12" {
		return DrivePlaybackFile{}, completionFailure("目标精确查询返回未知文件区域，未确认文件可用性")
	}
	pc, pcOK := merge(f.PC, f.PickCode)
	hash, hashOK := merge(strings.ToUpper(f.SHA), strings.ToUpper(f.Hash))
	sizeValue, sizeOK := merge(f.Size.String(), f.ShortSize.String())
	cid, cidOK := merge(f.CID.String(), f.Parent.String())
	if !pcOK || !hashOK || !sizeOK || !cidOK {
		return DrivePlaybackFile{}, completionFailure("目标精确查询文件字段冲突")
	}
	f.ID, f.PC, f.SHA, f.Size, f.CID = json.Number(idValue), pc, hash, json.Number(sizeValue), json.Number(cid)
	if f.Name == "" {
		f.Name = f.ShortName
	}
	if f.ID.String() != id || !instantPickCode.MatchString(f.PC) || !instantHash.MatchString(f.SHA) {
		return DrivePlaybackFile{}, completionFailure("目标精确查询缺少 SHA1、pickcode 或文件 ID 不匹配")
	}
	size, sizeErr := f.Size.Int64()
	if sizeErr != nil || size <= 0 {
		return DrivePlaybackFile{}, completionFailure("目标精确查询缺少有效文件大小")
	}
	return DrivePlaybackFile{ID: id, CID: f.CID.String(), Name: f.Name, Size: size, SHA1: strings.ToUpper(f.SHA), PickCode: f.PC}, nil
}

// Fresh, exact read used only after a target link failed. Never consult the
// pickcode metadata cache or infer deletion from an ambiguous response.
func (c *ShareClient) VerifyPlaybackFile(ctx context.Context, expected DrivePlaybackFile, cookie string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if CookieUserID(cookie) == "" {
		return completionFailure("目标账号无效")
	}
	httpCtx, finish := tracePlaybackHTTP(ctx, "目标文件失效核查（精确元数据）")
	defer finish()
	f, err := c.playbackFileInfo(httpCtx, expected.ID, cookie)
	if err != nil {
		return err
	}
	if f.PickCode != expected.PickCode || f.Size != expected.Size || !strings.EqualFold(f.SHA1, expected.SHA1) {
		return completionFailure("目标精确查询文件身份、SHA1 或大小不一致")
	}
	return nil
}

// Cached metadata is only an optimization; evict all handles for this exact
// file identity after confirmed loss. Credentials themselves are untouched.
func (c *ShareClient) ForgetPlaybackFileMetadata(file DrivePlaybackFile) {
	c.playbackSources.mu.Lock()
	defer c.playbackSources.mu.Unlock()
	for key, v := range c.playbackSources.entries {
		if v.File.ID == file.ID && v.File.PickCode == file.PickCode {
			delete(c.playbackSources.entries, key)
		}
	}
}
