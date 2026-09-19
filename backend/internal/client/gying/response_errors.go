package gying

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

var (
	ErrResourceUnavailable = errors.New("观影资源已下架或不可用")
	ErrAccessDenied        = errors.New("观影站访问受限，请稍后重试")
	ErrLoginFormat         = errors.New("观影登录接口返回了非预期内容，可能是验证页或站点异常，请稍后重试或查看观影日志")
)

// 只检查错误信封，不扫描正常资源内容；403 本身不能证明账号已退出。
func responseError(body []byte, status int, detail bool) error {
	var envelope struct {
		Code    json.RawMessage `json:"code"`
		Msg     string          `json:"msg"`
		Message string          `json:"message"`
	}
	decoded := json.Unmarshal(body, &envelope) == nil
	code, _ := strconv.Atoi(strings.Trim(string(envelope.Code), `"`))
	if status == http.StatusUnauthorized || code == http.StatusUnauthorized {
		return ErrForbidden
	}
	if decoded && code != 0 && code != http.StatusOK {
		message := envelope.Msg + " " + envelope.Message
		if detail && containsAny(message, "应版权方要求", "已删除相关资源", "资源已下架", "资源已删除", "影片已下架", "影片不存在", "资源不存在") {
			return ErrResourceUnavailable
		}
		if containsAny(message, "未登录", "请先登录", "请重新登录", "登录已失效", "登录状态已失效", "登录已过期", "登录过期", "登录超时") {
			return ErrForbidden
		}
		if code == 419 {
			return ErrChallenge
		}
		return ErrAccessDenied
	}
	if !decoded && isLoginShell(body) && (detail || !strings.Contains(string(body), "_obj.search")) {
		return ErrForbidden
	}
	if status == http.StatusForbidden || status == http.StatusTooManyRequests {
		return ErrAccessDenied
	}
	return nil
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}
