package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestGenerationFailureLogIncludesUpstreamStatus(t *testing.T) {
	logs := &GenerationLogs{}
	g := &LoggedGenerator{Logs: logs, Generator: generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		return nil, fmt.Errorf("读取原分享: 115 分享接口返回 HTTP 405")
	})}
	_, _ = g.Generate(context.Background(), GenerateInput{})
	entry := logs.Snapshot("ERROR")
	if len(entry) != 1 || !strings.Contains(entry[0].Message, "生成失败：读取原分享: 115 分享接口返回 HTTP 405") {
		t.Fatal("日志丢失原始原因", entry)
	}
}

func TestGenerationErrorDetailPreservesCause(t *testing.T) {
	for _, message := range []string{"读取分享目录: 115 拒绝请求：访问码错误", "读取原分享: 分享分页不完整，请重试", "生成配置无效：请先登录 115", "115 分享接口返回 HTTP 405"} {
		if got := generationErrorDetail(errors.New(message), GenerateInput{}); got != message {
			t.Fatalf("丢失具体原因：%s", got)
		}
	}
	err := fmt.Errorf("创建 STRM: %w", &os.PathError{Op: "open", Path: "/strm/电影.strm", Err: os.ErrPermission})
	if got := generationErrorDetail(err, GenerateInput{}); !strings.Contains(got, "permission denied") || !strings.Contains(got, "电影.strm") {
		t.Fatal("丢失文件系统错误", got)
	}
}

func TestGenerationErrorDetailRedactsCredentials(t *testing.T) {
	for _, tc := range []struct{ message, secret string }{
		{`请求失败: Get "https://115.com/s/secret-code?password=secret-password": connection refused`, "secret"},
		{`拒绝请求 Cookie: UID=private; CID=private; SEID=private`, "private"},
		{"Authorization: Bearer private\n读取失败", "private"},
		{`错误 {"password":"private", "receive_code":"private", "token":"private", "SEID":"private"}`, "private"},
		{`receive_code=private&share_code=private; UID=private`, "private"},
		{`请求失败 Bearer private`, "private"},
		{`提取码 custom-secret 无效`, "custom-secret"},
	} {
		got := generationErrorDetail(errors.New(tc.message), GenerateInput{Password: "custom-secret"})
		if strings.Contains(got, tc.secret) {
			t.Fatal("凭证未脱敏", got)
		}
	}
}
