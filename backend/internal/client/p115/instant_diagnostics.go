package p115

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// InstantUploadError exposes only generated diagnostics, never response bodies,
// credentials, sign_key or URLs. Cause preserves existing sentinel semantics.
type InstantUploadError struct {
	Stage  string
	Reason string
	Cause  error
}

func (e *InstantUploadError) Error() string { return e.Stage + "：" + e.Reason }
func (e *InstantUploadError) Unwrap() error { return e.Cause }

func instantFailure(stage, reason string, cause error) error {
	return &InstantUploadError{Stage: stage, Reason: reason, Cause: cause}
}

func InstantUploadFailureReason(err error) string {
	var detail *InstantUploadError
	if errors.As(err, &detail) {
		return detail.Error()
	}
	if errors.Is(err, ErrInstantUnsupported) {
		return ErrInstantUnsupported.Error()
	}
	if errors.Is(err, ErrInstantUncertain) {
		return ErrInstantUncertain.Error()
	}
	if err == nil {
		return "上传接口未返回结果"
	}
	return SharePlaybackFailureReason(err)
}

// Keep numeric response codes only: arbitrary strings may contain secrets.
func instantResponseCodes(raw []byte) string {
	var response map[string]json.RawMessage
	if json.Unmarshal(raw, &response) != nil {
		return "返回码不可解析"
	}
	result := ""
	for _, key := range []string{"status", "statuscode", "errno", "errcode", "code"} {
		v, ok := response[key]
		if !ok {
			continue
		}
		value := string(v)
		if len(v) > 0 && v[0] == '"' {
			if json.Unmarshal(v, &value) != nil {
				continue
			}
		}
		if len(value) > 20 {
			continue
		}
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			if result != "" {
				result += "，"
			}
			result += fmt.Sprintf("%s=%d", key, n)
		}
	}
	if result == "" {
		return "缺少有效数字返回码"
	}
	return result
}
