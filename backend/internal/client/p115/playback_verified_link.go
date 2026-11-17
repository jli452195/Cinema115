package p115

import (
	"context"
	"errors"
	"strings"
)

// Verify the candidate using the same response that supplies the UA-bound URL.
// Missing SHA1 alone permits one exact lookup; conflicting metadata never does.
func (c *ShareClient) VerifiedPlaybackURL(ctx context.Context, expected DrivePlaybackFile, cookie, ua string) (string, error) {
	if !validDriveID(expected.ID, false) || !instantHash.MatchString(expected.SHA1) || expected.Size <= 0 || CookieUserID(cookie) == "" {
		return "", ErrDriveInvalidResponse
	}
	readCtx, finish := tracePlaybackHTTP(ctx, "目标账号取链与身份核验 HTTP")
	info, id, err := c.pickCodeInfo(readCtx, expected.PickCode, cookie, ua)
	finish()
	if err != nil {
		return "", c.confirmRejectedPlaybackFile(ctx, expected, cookie, err)
	}
	return verifiedPlaybackDownload(expected, info, id, func() error { return c.VerifyPlaybackFile(ctx, expected, cookie) })
}

// confirmRejectedPlaybackFile 只在取链被泛拒绝后精确核对固定文件，不把拒绝直接当作删除。
func (c *ShareClient) confirmRejectedPlaybackFile(ctx context.Context, expected DrivePlaybackFile, cookie string, rejected error) error {
	if !errors.Is(rejected, ErrDriveRejected) {
		return rejected
	}
	if err := c.VerifyPlaybackFile(ctx, expected, cookie); err != nil {
		return err
	}
	return rejected
}

func verifiedPlaybackDownload(expected DrivePlaybackFile, info *DownloadInfo, id string, verifyMissingSHA func() error) (string, error) {
	if info == nil {
		return "", ErrDriveInvalidResponse
	}
	if id != expected.ID || info.Size != expected.Size || info.SHA1 != "" && !strings.EqualFold(info.SHA1, expected.SHA1) {
		return "", ErrDriveInvalidResponse
	}
	if info.SHA1 == "" {
		if err := verifyMissingSHA(); err != nil {
			return "", err
		}
	}
	if !AllowedDirectPlaybackURL(info.URL) {
		return "", ErrDriveLinkRestricted
	}
	return info.URL, nil
}
