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

func TestUnreadableReviewPreservesExistingSTRM(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Cookie") != "owner-cookie" {
			t.Error("未使用当前登录 Cookie 检查审核状态")
		}
		w.Write([]byte(`{"state":true,"data":{"share_state":0,"shareinfo":{"share_state":0},"count":1,"list":[]}}`))
	}))
	defer server.Close()
	client := p115.NewShareClient()
	client.WebURL = server.URL
	root := t.TempDir()
	input := generationOptions(t)
	directory := filepath.Join(root, input.OutputDir)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(directory, "previous.strm")
	if err := os.WriteFile(previous, []byte("原有播放链接"), 0600); err != nil {
		t.Fatal(err)
	}
	logs := &GenerationLogs{}
	g := &LoggedGenerator{Logs: logs, Generator: &STRMGenerator{Root: root, Client: client, Cookie: func() string { return "owner-cookie" }}}
	if _, err := g.Generate(context.Background(), input); !errors.Is(err, p115.ErrShareUnderReview) {
		t.Fatal(err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 1 || requests != 1 {
		t.Fatal("审核后继续读取或生成", err, requests)
	}
	content, err := os.ReadFile(previous)
	if err != nil || string(content) != "原有播放链接" {
		t.Fatal("审核失败改动了已有 STRM")
	}
	entries := logs.Snapshot("WARNING")
	if len(entries) != 1 || !strings.Contains(entries[0].Message, "审核中") {
		t.Fatal("缺少审核拦截日志")
	}
}

func TestReviewStopsTransferAndSnapshotWait(t *testing.T) {
	for _, stage := range []string{"source", "own", "deleted"} {
		t.Run(stage, func(t *testing.T) {
			fake := &transferFake{reviewStage: stage}
			if stage == "deleted" {
				fake.newSnapshots = 1
			}
			workflow, input := setupWorkflow(t, fake)
			// 若审核仍进入等待循环，应撞上任务超时而使测试失败。
			workflow.PollInterval = time.Hour
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := workflow.Generate(ctx, input)
			if !errors.Is(err, p115.ErrShareUnderReview) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("审核没有立即终止", err)
			}
			jobs := workflowJobs(t, workflow)
			if stage == "source" {
				if len(fake.calls) != 0 || len(jobs) != 0 {
					t.Fatal("审核原分享仍创建了转存任务")
				}
				return
			}
			if len(jobs) != 1 || !jobs[0].Finished || !strings.Contains(jobs[0].LastError, "审核") {
				t.Fatal("新分享审核未保存检查点")
			}
			if stage == "own" && (fake.deleted || strings.Contains(strings.Join(fake.calls, ","), "generate")) {
				t.Fatal("新分享审核后仍生成或删除")
			}
			if stage == "deleted" && (!fake.deleted || jobs[0].Status != "deleted") {
				t.Fatal("删除后的复查丢失清理状态")
			}
		})
	}
}

func TestReadableReviewCanGenerateSTRMWithoutPreflight(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Cookie") != "UID=42_A1_100" {
			t.Error("本人分享未使用当前 Cookie")
		}
		w.Write([]byte(`{"state":true,"data":{"share_state":0,"userinfo":{"user_id":"99"},"count":1,"list":[{"fid":"1","n":"video.mp4","s":"2000000"}]}}`))
	}))
	defer server.Close()
	client := p115.NewShareClient()
	client.WebURL = server.URL
	client.RequestInterval = 0
	root := t.TempDir()
	generator := &STRMGenerator{Root: root, Client: client, Cookie: func() string { return "UID=42_A1_100" }}
	input := generationOptions(t)
	result, err := generator.Generate(context.Background(), input)
	if err != nil || result.Written != 1 || requests != 1 {
		t.Fatal("可读审核分享被拦截或进行了额外探测", err)
	}
	if _, err := os.Stat(filepath.Join(root, input.OutputDir, "video.strm")); err != nil {
		t.Fatal(err)
	}
}

func TestOwnShareCleanupUsesCookieAndReadableSnapshot(t *testing.T) {
	for _, failure := range []string{"", "cleanup-unreadable", "cleanup-mismatch", "after-unreadable"} {
		name := failure
		if name == "" {
			name = "本人审核中可读则生成并清理成功"
		}
		t.Run(name, func(t *testing.T) {
			workflow, input := setupWorkflow(t, &transferFake{})
			ownReads, anonymousReads, deletes, currentCookieReads := 0, 0, 0, 0
			password := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Cookie") != "UID=42_A; SEID=original-cookie" {
					anonymousReads++
					t.Error("新分享流程没有使用同一登录 Cookie")
					w.Write([]byte(`{"state":false,"error":"分享审核中"}`))
					return
				}
				switch r.URL.Path {
				case "/share/snap":
					state, id := 1, "11"
					if r.URL.Query().Get("share_code") == "own" {
						ownReads++
						state, id = 0, "21"
						if failure == "cleanup-unreadable" && ownReads == 3 || failure == "after-unreadable" && deletes > 0 {
							w.Write([]byte(`{"state":true,"data":{"share_state":0,"count":1,"list":[]}}`))
							return
						}
					}
					size := 2000000
					if failure == "cleanup-mismatch" && ownReads == 3 {
						size = 1
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"state": true, "data": map[string]any{"share_state": state, "userinfo": map[string]any{"user_id": "99"}, "count": 1, "list": []map[string]any{{"fid": id, "n": "movie.mkv", "s": size}}}})
				case "/files/add":
					w.Write([]byte(`{"state":true,"cid":"999"}`))
				case "/share/receive":
					w.Write([]byte(`{"state":true}`))
				case "/files":
					w.Write([]byte(`{"state":true,"count":1,"data":[{"fid":"21","n":"movie.mkv","s":"2000000"}]}`))
				case "/share/send":
					w.Write([]byte(`{"state":true,"data":{"share_code":"own","receive_code":"test"}}`))
				case "/share/updateshare":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					password = r.PostForm.Get("receive_code")
					w.Write([]byte(`{"state":true}`))
				case "/share/shareinfo":
					_ = json.NewEncoder(w).Encode(map[string]any{"state": true, "data": map[string]any{"share_duration": "-1", "receive_code": password}})
				case "/rb/delete":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.Method != "POST" || r.PostForm.Get("fid") != "999" || ownReads < 3 {
						t.Error("清理范围或执行顺序错误")
					}
					if _, err := os.Stat(filepath.Join(workflow.Root, input.OutputDir, "movie.strm")); err != nil {
						t.Error("STRM 写入前执行了清理")
					}
					deletes++
					w.Write([]byte(`{"state":true}`))
				default:
					t.Error("出现未预期的请求", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client := p115.NewShareClient()
			client.WebURL = server.URL
			client.RequestInterval = 0
			workflow.Client = client
			// 模拟任务开始后用户切换登录，生成器必须继续使用工作流固定的 Cookie。
			workflow.Generator = &STRMGenerator{Root: workflow.Root, Client: client, Cookie: func() string { currentCookieReads++; return "other-account-cookie" }}
			result, err := workflow.Generate(context.Background(), input)
			if result == nil || result.Written != 1 || anonymousReads != 0 || currentCookieReads != 0 {
				t.Fatal("本人 Cookie 的生成流程错误", err)
			}
			wantStatus, wantDeletes := "completed", 1
			switch failure {
			case "":
				if err != nil || !result.Cleaned || ownReads != 4 {
					t.Fatal("可读的本人审核分享仍被阻止清理", err, ownReads)
				}
			case "after-unreadable":
				wantStatus = "deleted"
				if !errors.Is(err, p115.ErrShareUnderReview) || !result.Cleaned || !strings.Contains(err.Error(), "回收站") {
					t.Fatal("删除后不可读丢失恢复提示", err)
				}
			default:
				wantStatus, wantDeletes = "checking_cleanup", 0
				if err == nil || result.Cleaned {
					t.Fatal("本人 Cookie 无法通过目录校验时仍清理")
				}
			}
			jobs := workflowJobs(t, workflow)
			if deletes != wantDeletes || len(jobs) != 1 || jobs[0].Status != wantStatus || !jobs[0].Finished {
				t.Fatal("清理检查点或任务完成状态错误")
			}
		})
	}
}

func TestReadableForeignReviewStopsBeforeTransferAndSchedulesSafely(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, retry := range []bool{false, true} {
			name := "根目录"
			if nested {
				name = "子目录"
			}
			if retry {
				name += "开启重试"
			}
			t.Run(name, func(t *testing.T) {
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					if r.Method != "GET" || r.URL.Path != "/share/snap" || r.Header.Get("Cookie") != "UID=42_A1_100" {
						t.Error("审核分享触发云端写入或丢失 Cookie")
						w.WriteHeader(500)
						return
					}
					if nested && r.URL.Query().Get("cid") == "0" {
						w.Write([]byte(`{"state":true,"data":{"share_state":1,"userinfo":{"user_id":"99"},"count":1,"list":[{"cid":"123","n":"目录"}]}}`))
						return
					}
					w.Write([]byte(`{"state":true,"data":{"share_state":0,"userinfo":{"user_id":"99"},"shareinfo":{"share_state":0,"forbid_reason":""},"count":1,"list":[{"fid":"1","n":"电影.mkv","s":"100"}]}}`))
				}))
				defer server.Close()
				workflow, input := setupWorkflow(t, &transferFake{})
				client := p115.NewShareClient()
				client.WebURL = server.URL
				client.RequestInterval = 0
				workflow.Client = client
				workflow.Cookie = func() string { return "UID=42_A1_100" }
				s, db, _ := setupShareTasks(t, workflow)
				s.Cookie = workflow.Cookie
				s.Settings = workflow.Settings
				settings := s.Settings.Value()
				settings.ReviewRetryEnabled = retry
				if err := s.Settings.Save(settings); err != nil {
					t.Fatal(err)
				}
				input.Mode = "reshare"
				result, err := s.Generate(context.Background(), input)
				if !errors.Is(err, p115.ErrShareUnderReview) || !errors.Is(err, errSourceShareReview) || errors.Is(err, context.DeadlineExceeded) || result != nil {
					t.Fatal("未在转存前正确终止审核分享", err)
				}
				wantRequests := 1
				if nested {
					wantRequests = 2
				}
				if requests != wantRequests || len(workflowJobs(t, workflow)) != 0 {
					t.Fatal("审核后继续读取或创建转存任务")
				}
				task := taskHistory(t, db)[0]
				wantStatus := "review"
				if retry {
					wantStatus = "waiting_review"
				}
				if task.Status != wantStatus || (task.NextRetryAt != nil) != retry {
					t.Fatal("未使用审核状态或重试机制", task.Status)
				}
			})
		}
	}
}

func TestTransferWaitPreservesCauseWithoutDuplicateDeadline(t *testing.T) {
	for _, previous := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		calls := 0
		mismatch := errors.New("目录校验未通过：原分享 10 项，新分享 0 项")
		w := &ShareWorkflow{PollInterval: time.Millisecond}
		err := w.until(ctx, func(ctx context.Context) error {
			calls++
			if previous && calls == 1 {
				return mismatch
			}
			<-ctx.Done()
			return ctx.Err()
		})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, mismatch) != previous || strings.Count(err.Error(), "context deadline exceeded") != 1 || !strings.Contains(err.Error(), "等待 115 处理超时") {
			t.Fatal("超时丢失原始原因或重复输出", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &ShareWorkflow{}
	err := w.until(ctx, func(context.Context) error { t.Error("取消后仍开始读取"); return nil })
	if !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("用户取消不应归为超时", err)
	}
}
