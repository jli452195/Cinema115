package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

func TestOwnShareGeneratesOriginalWithPinnedCookie(t *testing.T) {
	for _, fixed := range []string{"", "1234", "abcd"} {
		t.Run("固定密码="+fixed, func(t *testing.T) {
			fake := &transferFake{ownerID: "42"}
			w, input := setupWorkflow(t, fake)
			settings := w.Settings.Value()
			settings.FixedSharePassword = fixed
			if err := w.Settings.Save(settings); err != nil {
				t.Fatal(err)
			}
			input.Mode, input.CID, input.Password = "reshare", "10", "manual"
			called := 0
			w.Generator = generateFunc(func(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
				called++
				// 模拟登录配置已变化，本次直接生成仍必须使用判断归属时的凭据。
				w.Cookie = func() string { return "UID=99_A" }
				if generationCookie(ctx, w.Cookie) != "UID=42_A; SEID=original-cookie" {
					t.Error("生成中途切换了账号")
				}
				expected := input
				expected.Mode = "direct"
				if in != expected {
					t.Error("没有保留原分享和生成范围")
				}
				return &GenerationResult{Written: 8}, nil
			})
			result, err := w.Generate(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if called != 1 || result.Mode != "direct" || result.Written != 8 || result.ReshareURL != "" || result.TempDirectoryID != "" || len(fake.calls) != 0 || len(workflowJobs(t, w)) != 0 {
				t.Fatal("本人分享不应创建新分享或执行云端写入")
			}
		})
	}
}

func TestUnknownShareOwnerStopsBeforeCloudWrites(t *testing.T) {
	for _, owner := range []string{"missing", "0", "invalid", "-1"} {
		t.Run(owner, func(t *testing.T) {
			fake := &transferFake{ownerID: owner}
			w, input := setupWorkflow(t, fake)
			_, err := w.Generate(context.Background(), input)
			if err == nil || !strings.Contains(err.Error(), "user_id") || len(fake.calls) != 0 || len(workflowJobs(t, w)) != 0 {
				t.Fatal("未知归属继续执行", err)
			}
		})
	}
	fake := &transferFake{}
	w, input := setupWorkflow(t, fake)
	w.Cookie = func() string { return "UID=invalid_A" }
	if _, err := w.Generate(context.Background(), input); !errors.Is(err, ErrGenerationInput) || fake.listCalls != 0 || len(fake.calls) != 0 {
		t.Fatal("无效 Cookie 未提前拦截", err)
	}
}

func TestReviewOwnershipThroughHTTPAndSTRMWriter(t *testing.T) {
	for _, owner := range []string{"42", "99"} {
		t.Run(owner, func(t *testing.T) {
			w, input := setupWorkflow(t, &transferFake{})
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/share/snap" || r.Method != http.MethodGet {
					t.Error("审核分享触发了云端写入")
					http.Error(rw, "unexpected operation", http.StatusBadRequest)
					return
				}
				if r.Header.Get("Cookie") != "UID=42_A; SEID=original-cookie" || r.URL.Query().Get("receive_code") != "1234" {
					t.Error("原分享检查的账号或密码错误")
				}
				_ = json.NewEncoder(rw).Encode(map[string]any{"state": true, "data": map[string]any{
					"share_state": 0, "userinfo": map[string]any{"user_id": owner}, "count": 1,
					"list": []map[string]any{{"fid": "3516725279353473021", "n": "电影.mkv", "s": 2000000}},
				}})
			}))
			defer server.Close()
			client := p115.NewShareClient()
			client.WebURL, client.RequestInterval = server.URL, 0
			w.Client = client
			w.Generator = &STRMGenerator{Root: w.Root, Client: client, Cookie: w.Cookie}
			input.MinSizeMB = 0
			result, err := w.Generate(context.Background(), input)
			if owner != "42" {
				if !errors.Is(err, errSourceShareReview) || !errors.Is(err, p115.ErrShareUnderReview) || requests != 1 {
					t.Fatal("他人审核分享未在转存前停止", err)
				}
				return
			}
			if err != nil || result.Written != 1 || result.Mode != "direct" {
				t.Fatal("本人可读审核分享生成失败", err)
			}
			data, err := os.ReadFile(filepath.Join(w.Root, input.OutputDir, "电影.strm"))
			if err != nil || !strings.Contains(string(data), "share_code=source") || !strings.Contains(string(data), "id=3516725279353473021") || !strings.Contains(string(data), "receive_code=1234") {
				t.Fatal("STRM 未使用原分享和原文件编号", err)
			}
			if len(workflowJobs(t, w)) != 0 {
				t.Fatal("本人分享生成了转存任务")
			}
		})
	}
}

func TestBatchSeparatesOwnSharesAndPersistsActualMode(t *testing.T) {
	f := &batchTransferFake{review: true, owners: map[string]string{"self": "42", "missing": ""}}
	s, db, input := setupShareTasks(t, nil)
	settings := s.Settings.Value()
	settings.ReshareEnabled, settings.FixedSharePassword = true, "abcd"
	if err := s.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	generated := 0
	gen := generateFunc(func(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
		generated++
		if generationCookie(ctx, s.Cookie) != "UID=42_A; SEID=private-cookie" {
			t.Error("批次未固定 Cookie")
		}
		if strings.Contains(in.URL, "/self?") {
			if in.Mode != "direct" || ctx.Value(generationRootIDsKey{}) != nil {
				t.Error("本人分享错误进入合并")
			}
		} else if !strings.Contains(in.URL, "/own?") || len(ctx.Value(generationRootIDsKey{}).([]string)) != 1 {
			t.Error("其他来源未使用新分享及限定范围")
		}
		return &GenerationResult{Written: 1}, nil
	})
	w := NewShareWorkflow(f, gen, s.Cookie, s.Settings, s.Settings.root, db)
	w.PollInterval = time.Millisecond
	s.Generator = w
	codes := []string{"self", "a", "review", "missing", "b"}
	inputs := make([]GenerateInput, len(codes))
	for i, code := range codes {
		inputs[i] = input
		inputs[i].URL, inputs[i].Mode, inputs[i].Password = "https://115.com/s/"+code+"?password=abcd", "reshare", ""
	}
	out, err := s.GenerateBatch(context.Background(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Err != nil || out[0].Result.Mode != "direct" || out[0].Result.ReshareURL != "" || out[1].Err != nil || out[4].Err != nil || !errors.Is(out[2].Err, p115.ErrShareUnderReview) || out[3].Err == nil {
		t.Fatal("批次未按归属及审核分别处理")
	}
	if generated != 3 || strings.Join(f.received, ",") != "a,b" || f.creates != 3 || f.shares != 1 || f.deletes != 1 {
		t.Fatal("合并转存范围不正确")
	}
	if out[1].Result.ReshareURL == "" || out[1].Result.ReshareURL != out[4].Result.ReshareURL {
		t.Fatal("外人分享未共用新分享")
	}
	for _, task := range taskHistory(t, db) {
		if !strings.Contains(task.SourceURL, "/self?") {
			continue
		}
		var req shareTaskRequest
		if err := json.Unmarshal(task.Request, &req); err != nil {
			t.Fatal(err)
		}
		if task.Mode != "direct" || task.Status != "success" || req.Merged || req.Input.Mode != "direct" || task.ShareURL != "" {
			t.Fatal("本人直出的持久化模式错误")
		}
	}
}

func TestReviewRetryUsesSavedFeatureAccount(t *testing.T) {
	calls := 0
	s, db, input := setupShareTasks(t, generateFunc(func(ctx context.Context, _ GenerateInput) (*GenerationResult, error) {
		calls++
		if generationCookie(ctx, nil) != "UID=42_A" {
			t.Error("重试切换了原任务账号")
		}
		if calls == 1 {
			return nil, p115.ErrShareUnderReview
		}
		return &GenerationResult{Written: 1}, nil
	}))
	s.AccountCookie = func(id string) string {
		if id != "saved-account" {
			t.Error("没有读取原任务绑定的账号")
		}
		return "UID=42_A"
	}
	settings := s.Settings.Value()
	settings.AccountID = "saved-account"
	if err := s.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Generate(context.Background(), input); !errors.Is(err, p115.ErrShareUnderReview) {
		t.Fatal(err)
	}
	settings.AccountID = "another-account"
	if err := s.Settings.Save(settings); err != nil {
		t.Fatal(err)
	}
	if err := s.retryDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || taskHistory(t, db)[0].Status != "success" {
		t.Fatal("配置切换后未按原账号完成重试")
	}
}
