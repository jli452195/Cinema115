package service

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var (
	logURLPattern        = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
	logHeaderPattern     = regexp.MustCompile(`(?im)\b(cookie|set-cookie|authorization)\s*["']?\s*[:=][^\r\n]*`)
	logCredentialPattern = regexp.MustCompile(`(?i)(["']?\b(?:password|receive_code|share_code|token|access_token|refresh_token|bot_token|UID|CID|SEID|KID)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}&\]\r\n]+)`)
	logBearerPattern     = regexp.MustCompile(`(?i)\bBearer\s+[^\s,"';]+`)
)

// generationErrorDetail 保留错误原因，隐藏上游地址、凭证字段和本次输入的提取码。
func generationErrorDetail(err error, input GenerateInput) string {
	message := err.Error()
	message = logURLPattern.ReplaceAllString(message, "[链接已隐藏]")
	message = logHeaderPattern.ReplaceAllString(message, "$1: [已隐藏]")
	message = logCredentialPattern.ReplaceAllString(message, "${1}[已隐藏]")
	message = logBearerPattern.ReplaceAllString(message, "Bearer [已隐藏]")
	if password := strings.TrimSpace(input.Password); password != "" {
		message = strings.ReplaceAll(message, password, "[提取码已隐藏]")
	}
	if shareURL, parseErr := url.Parse(input.URL); parseErr == nil {
		if password := shareURL.Query().Get("password"); password != "" {
			message = strings.ReplaceAll(message, password, "[提取码已隐藏]")
		}
	}
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message))
}
