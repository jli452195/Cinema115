package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
)

// 用独立的网盘树模拟转存与分享快照，分享中的目录 ID 与网盘 ID 不相同。
type batchTransferFake struct {
	owners                   map[string]string
	review                   bool
	failReceive              bool
	next                     int
	drive                    map[string][]p115.ShareFile
	names                    map[string]string
	shared                   []p115.ShareFile
	received                 []string
	shares, deletes, creates int
	ownListCalls             int
	password                 string
}

func (f *batchTransferFake) ListForTransfer(_ context.Context, share *p115.ShareInfo, _ string, _ int, cookie string) (*p115.SharePage, error) {
	if cookie != "UID=42_A; SEID=private-cookie" {
		return nil, errors.New("未使用本次登录 Cookie")
	}
	if share.Code == "review" && f.review {
		return nil, p115.ErrShareUnderReview
	}
	owner := "99"
	if value, ok := f.owners[share.Code]; ok {
		owner = value
	}
	return &p115.SharePage{OwnerID: owner, Count: 1, Files: []p115.ShareFile{{ID: "10", Name: "同名电影.mkv", Size: 100}}}, nil
}

func (f *batchTransferFake) List(ctx context.Context, share *p115.ShareInfo, cid string, offset int, cookie string) (*p115.SharePage, error) {
	if share.Code != "own" {
		return f.ListForTransfer(ctx, share, cid, offset, cookie)
	}
	f.ownListCalls++
	if cookie != "UID=42_A; SEID=private-cookie" {
		return nil, errors.New("本人分享校验未携带 Cookie")
	}
	var files []p115.ShareFile
	if cid == "0" {
		files = f.shared
	} else {
		id, err := strconv.Atoi(cid)
		if err != nil {
			return nil, err
		}
		files = f.drive[strconv.Itoa(id-10000)]
	}
	result := make([]p115.ShareFile, len(files))
	copy(result, files)
	for i := range result {
		id, _ := strconv.Atoi(result[i].ID)
		result[i].ID = strconv.Itoa(id + 10000)
	}
	return &p115.SharePage{Count: len(result), Files: result}, nil
}

func (f *batchTransferFake) CreateDirectory(_ context.Context, parent, name, cookie string) (string, error) {
	if strings.HasPrefix(name, "Cinema115-临时-") && parent != "0" {
		return "", errors.New("批次分享转存未使用根目录")
	}
	if cookie != "UID=42_A; SEID=private-cookie" {
		return "", errors.New("创建目录账号错误")
	}
	if f.drive == nil {
		f.drive = make(map[string][]p115.ShareFile)
		f.names = make(map[string]string)
	}
	f.next++
	f.creates++
	id := strconv.Itoa(100 + f.next)
	f.names[id] = name
	f.drive[parent] = append(f.drive[parent], p115.ShareFile{ID: id, Name: name, Directory: true})
	return id, nil
}

func (f *batchTransferFake) ReceiveShare(_ context.Context, share *p115.ShareInfo, ids []string, cid, cookie string) error {
	if cookie != "UID=42_A; SEID=private-cookie" || len(ids) != 1 || ids[0] != "10" {
		return errors.New("转存参数不正确")
	}
	f.received = append(f.received, share.Code)
	if f.failReceive {
		return p115.ErrShareUnderReview
	}
	f.drive[cid] = append(f.drive[cid], p115.ShareFile{ID: strconv.Itoa(1000 + len(f.received)), Name: "同名电影.mkv", Size: 100})
	return nil
}

func (f *batchTransferFake) DriveFiles(_ context.Context, cid string, _ int, cookie string) (*p115.SharePage, error) {
	if cookie != "UID=42_A; SEID=private-cookie" {
		return nil, errors.New("网盘读取账号错误")
	}
	return &p115.SharePage{Count: len(f.drive[cid]), Files: f.drive[cid]}, nil
}

func (f *batchTransferFake) SendShare(_ context.Context, ids []string, _ string) (*p115.ShareInfo, error) {
	f.shares++
	f.shared = nil
	for _, id := range ids {
		if name := f.names[id]; name != "" {
			f.shared = append(f.shared, p115.ShareFile{ID: id, Name: name, Directory: true})
		} else {
			f.shared = append(f.shared, p115.ShareFile{ID: id, Name: "同名电影.mkv", Size: 100})
		}
	}
	return &p115.ShareInfo{Code: "own", URL: "https://115.com/s/own"}, nil
}

func (f *batchTransferFake) ConfigureShare(_ context.Context, share *p115.ShareInfo, password, _ string) error {
	f.password = password
	share.Password = password
	share.URL = "https://115.com/s/own?password=" + password
	return nil
}

func (f *batchTransferFake) DeleteDirectory(_ context.Context, cid, cookie string) error {
	if cookie != "UID=42_A; SEID=private-cookie" || !strings.HasPrefix(f.names[cid], "Cinema115-临时-") {
		return errors.New("误删了父目录或来源目录")
	}
	f.deletes++
	return nil
}

func TestMergedShareExcludesReviewAndRetriesOnlyThatSource(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("重试开关%v", enabled), func(t *testing.T) {
			f := &batchTransferFake{review: true}
			s, db, input := setupShareTasks(t, nil)
			settings := s.Settings.Value()
			settings.ReshareEnabled = true
			settings.ReviewRetryEnabled = enabled
			settings.FixedSharePassword = "abcd"
			if err := s.Settings.Save(settings); err != nil {
				t.Fatal(err)
			}
			var generated []GenerateInput
			gen := generateFunc(func(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
				if ctx.Value(generationCookieKey{}) != "UID=42_A; SEID=private-cookie" {
					t.Error("生成未固定使用本人 Cookie")
				}
				if in.CID != "0" || len(ctx.Value(generationRootIDsKey{}).([]string)) != 1 || in.URL != "https://115.com/s/own?password=abcd" {
					t.Error("没有在同一新分享的来源目录生成", in.CID)
				}
				generated = append(generated, in)
				return &GenerationResult{Written: 1, Files: []GeneratedSTRM{{Path: "同名电影.strm"}}}, nil
			})
			w := NewShareWorkflow(f, gen, s.Cookie, s.Settings, s.Settings.root, db)
			w.PollInterval = time.Millisecond
			s.Generator = &LoggedGenerator{Generator: w, Logs: &GenerationLogs{}}
			inputs := make([]GenerateInput, 3)
			for i, code := range []string{"a", "review", "b"} {
				inputs[i] = input
				inputs[i].URL = "https://115.com/s/" + code
				inputs[i].Mode = "reshare"
			}
			out, err := s.GenerateBatch(WithGenerationSource(context.Background(), "Telegram"), inputs)
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != 3 || out[0].Err != nil || out[2].Err != nil || !errors.Is(out[1].Err, p115.ErrShareUnderReview) {
				t.Fatal("未隔离审核项", out, err)
			}
			if f.shares != 1 || f.deletes != 1 || f.creates != 3 || strings.Join(f.received, ",") != "a,b" || len(generated) != 2 {
				t.Fatal("没有统一转存分享或审核项被转存", f.shares, f.deletes, f.creates, f.received)
			}
			if f.ownListCalls != 1 {
				t.Fatal("平铺新分享被重复读取", f.ownListCalls)
			}
			if out[0].Result.ReshareURL != out[2].Result.ReshareURL || !out[0].Result.Cleaned || out[0].Result.Written != 1 || out[0].RetryRootIDs[0] == out[2].RetryRootIDs[0] {
				t.Fatal("共用分享、来源目录或计数错误")
			}
			for _, task := range taskHistory(t, db) {
				if strings.Contains(task.SourceURL, "/review") {
					want := "review"
					if enabled {
						want = "waiting_review"
					}
					if task.Status != want || (task.NextRetryAt != nil) != enabled {
						t.Fatal("审核重试没有独立持久化", task.Status)
					}
				} else if task.Status != "success" {
					t.Fatal("正常来源未成功", task.Status)
				}
			}
			// 调度器重新装配后仍能从数据库找到审核项；模拟该项审核通过。
			f.review = false
			// 单链接重试仍使用原有工作流，因此生成起始目录为新分享根目录。
			w.Generator = generateFunc(func(_ context.Context, in GenerateInput) (*GenerationResult, error) {
				generated = append(generated, in)
				return &GenerationResult{Written: 1}, nil
			})
			restarted := *s
			if err := restarted.retryDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			want := 1
			if enabled {
				want = 2
			}
			if f.shares != want || len(f.received) != want+1 {
				t.Fatal("自动重试重复处理了正常来源", f.received)
			}
		})
	}
}

func TestMergedShareReceiveReviewDoesNotReplayMutation(t *testing.T) {
	f := &batchTransferFake{failReceive: true}
	s, db, input := setupShareTasks(t, nil)
	w := NewShareWorkflow(f, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		t.Error("失败转存后继续生成")
		return nil, nil
	}), s.Cookie, s.Settings, s.Settings.root, db)
	s.Generator = w
	input.URL = "https://115.com/s/a"
	input.Mode = "reshare"
	out, err := s.GenerateBatch(context.Background(), []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(out[0].Err, p115.ErrShareUnderReview) || f.shares != 0 || f.deletes != 0 {
		t.Fatal("失败后继续分享或清理")
	}
	task := taskHistory(t, db)[0]
	if task.Status != "review" || task.NextRetryAt != nil {
		t.Fatal("已发起转存的异常被自动重放")
	}
}

func TestMergedAllReviewDoesNotCreateEmptyShare(t *testing.T) {
	f := &batchTransferFake{review: true}
	s, db, input := setupShareTasks(t, nil)
	s.Generator = NewShareWorkflow(f, generateFunc(func(context.Context, GenerateInput) (*GenerationResult, error) {
		t.Error("审核中仍调用生成")
		return nil, nil
	}), s.Cookie, s.Settings, s.Settings.root, db)
	input.URL, input.Mode = "https://115.com/s/review", "reshare"
	out, err := s.GenerateBatch(context.Background(), []GenerateInput{input})
	if err != nil || !errors.Is(out[0].Err, p115.ErrShareUnderReview) || f.creates != 0 || f.shares != 0 || taskHistory(t, db)[0].Status != "waiting_review" {
		t.Fatal("全审核批次创建了空分享或未安排重试", err)
	}
}

func TestMergedOwnShareRetryKeepsSourceCIDAndDoesNotRecreateShare(t *testing.T) {
	f := &batchTransferFake{}
	s, db, input := setupShareTasks(t, nil)
	var retryRoot string
	gen := generateFunc(func(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
		ids := ctx.Value(generationRootIDsKey{}).([]string)
		if ids[0] == "11001" {
			retryRoot = ids[0]
			return nil, p115.ErrShareUnderReview
		}
		return &GenerationResult{Written: 2}, nil
	})
	w := NewShareWorkflow(f, gen, s.Cookie, s.Settings, s.Settings.root, db)
	s.Generator = w
	inputs := []GenerateInput{input, input}
	inputs[0].URL, inputs[0].Mode = "https://115.com/s/a", "reshare"
	inputs[1].URL, inputs[1].Mode = "https://115.com/s/b", "reshare"
	out, err := s.GenerateBatch(context.Background(), inputs)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(out[0].Err, p115.ErrShareUnderReview) || out[1].Err != nil || f.deletes != 0 || retryRoot == "" {
		t.Fatal("合并后的来源错误未隔离或误清理")
	}
	for _, task := range taskHistory(t, db) {
		if strings.Contains(task.SourceURL, "/b") && task.Status != "success" {
			t.Fatal("成功来源进入了重试")
		}
	}
	calls := 0
	w.Generator = generateFunc(func(ctx context.Context, in GenerateInput) (*GenerationResult, error) {
		calls++
		if in.Mode != "direct" || in.CID != "0" || ctx.Value(generationRootIDsKey{}).([]string)[0] != retryRoot || !strings.Contains(in.URL, "/own?") {
			t.Fatal("重试没有限定到原合并分享的来源目录", in.CID)
		}
		return &GenerationResult{Written: 1}, nil
	})
	if err := s.retryDue(context.Background(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || f.shares != 1 || len(f.received) != 2 || f.deletes != 0 {
		t.Fatal("审核重试重新转存、分享或清理了合并目录")
	}
}
