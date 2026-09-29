package p115

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
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

// Confirm the exact upload receipt without asking for a UA-specific CDN URL.
func (c *ShareClient) CompletePlaybackFile(ctx context.Context, info *DownloadInfo, cid string, result *InstantUploadResult, cookie string) (DrivePlaybackFile, error) {
	var f DrivePlaybackFile
	if result == nil || info == nil || !instantHash.MatchString(info.SHA1) || info.Size <= 0 || !validDriveID(cid, true) || CookieUserID(cookie) == "" {
		return f, completionFailure("秒传回执或目标账号无效")
	}
	var err error
	if validDriveID(result.FileID, false) {
		f, err = c.playbackFileInfo(ctx, result.FileID, cookie)
	} else if result.FileID == "" && instantPickCode.MatchString(result.PickCode) {
		var found *DrivePlaybackFile
		found, err = c.playbackPickCodeMetadata(ctx, "receipt:"+CookieUserID(cookie), result.PickCode, cookie)
		if found != nil {
			f = *found
		}
	} else {
		return f, completionFailure("秒传回执缺少有效文件标识")
	}
	if err != nil {
		return f, err
	}
	if !validDriveID(f.ID, false) || !instantPickCode.MatchString(f.PickCode) || result.PickCode != "" && f.PickCode != result.PickCode || !strings.EqualFold(f.SHA1, info.SHA1) || f.Size != info.Size || f.CID != "" && f.CID != cid {
		return f, completionFailure("目标文件身份、SHA1、大小或目录与秒传回执不一致")
	}
	if f.CID == "" {
		f.CID = cid
	}
	return f, nil
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
		return DrivePlaybackFile{}, completionFailure("目标 fileID 精确查询失败：" + SharePlaybackFailureReason(err))
	}
	var resp struct {
		Data []struct {
			ID   json.Number `json:"file_id"`
			CID  json.Number `json:"category_id"`
			PC   string      `json:"pick_code"`
			SHA  string      `json:"sha1"`
			Name string      `json:"file_name"`
			Size json.Number `json:"file_size"`
		}
	}
	if json.Unmarshal(raw, &resp) != nil || len(resp.Data) != 1 {
		return DrivePlaybackFile{}, completionFailure("目标精确查询响应数量或格式无效")
	}
	f := resp.Data[0]
	if f.ID.String() != id || !instantPickCode.MatchString(f.PC) || !instantHash.MatchString(f.SHA) {
		return DrivePlaybackFile{}, completionFailure("目标精确查询缺少 SHA1、pickcode 或文件 ID 不匹配")
	}
	size, _ := f.Size.Int64()
	return DrivePlaybackFile{ID: id, CID: f.CID.String(), Name: f.Name, Size: size, SHA1: strings.ToUpper(f.SHA), PickCode: f.PC}, nil
}
