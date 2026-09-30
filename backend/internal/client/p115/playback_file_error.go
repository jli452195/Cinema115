package p115

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrDriveFileMissing     = errors.New("目标文件已不存在或已移出网盘")
	ErrDriveRejected        = errors.New("115 拒绝目标文件请求")
	ErrDriveInvalidResponse = errors.New("目标直链响应或文件身份无效")
	ErrDriveLinkRestricted  = errors.New("目标直链要求额外凭据，无法直接 302 播放")
)

// Keep only fixed endpoint names, numeric codes and local classifications.
// Raw upstream text can contain credentials and must not cross this boundary.
type DriveAPIError struct {
	Endpoint string
	Code     int64
	Cause    error
}

func (e *DriveAPIError) Error() string {
	return fmt.Sprintf("%s：%s（业务码=%d）", e.Endpoint, e.Cause, e.Code)
}
func (e *DriveAPIError) Unwrap() error { return e.Cause }

func driveAPIError(path string, raw []byte, response shareResponse) error {
	var codes struct {
		Code  json.Number `json:"code"`
		Errno json.Number `json:"errno"`
		ErrNo json.Number `json:"errNo"`
	}
	decodeErr := json.Unmarshal(raw, &codes)
	code := int64(0)
	conflict := decodeErr != nil
	for _, v := range []json.Number{codes.Code, codes.Errno, codes.ErrNo} {
		if v == "" {
			continue
		}
		n, err := v.Int64()
		if err != nil {
			conflict = true
			continue
		}
		if code != 0 && n != code {
			conflict = true
		}
		if n != 0 {
			code = n
		}
	}
	cause := ErrDriveRejected
	category := shareDownloadError(response)
	switch {
	case errors.Is(category, ErrShareLoginRequired) || code == 990001:
		cause = ErrShareLoginRequired
	case errors.Is(category, ErrShareThrottled):
		cause = ErrShareThrottled
	// Observed on get_info for a nonexistent ID. A download rejection,
	// generic "文件错误", HTTP 404 or an empty success is never deletion proof.
	case path == "/files/get_info" && code == 20018 && !conflict && string(response.State) == "false":
		cause = ErrDriveFileMissing
	}
	return &DriveAPIError{Endpoint: path, Code: code, Cause: cause}
}
