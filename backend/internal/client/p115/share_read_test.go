package p115

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadShareBeforeClassifyingReview(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		failed, review bool
	}{
		{"审核中列表可读", `{"state":true,"data":{"share_state":0,"userinfo":{"user_id":"other"},"count":1,"list":[{"fid":"1","n":"video.mp4","s":"20"}]}}`, false, false},
		{"审核提示不覆盖可读列表", `{"state":true,"message":"审核中","data":{"share_state":0,"count":1,"list":[{"cid":"123","n":"目录"}]}}`, false, false},
		{"拒绝请求后识别审核字段", `{"state":false,"data":{"share_state":0}}`, true, true},
		{"拒绝请求后识别审核文字", `{"state":false,"msg":"分享正在审核中"}`, true, true},
		{"有数量但无列表时识别审核", `{"state":true,"data":{"share_state":0,"count":1,"list":[]}}`, true, true},
		{"缺失列表时识别嵌套审核", `{"state":true,"data":{"shareinfo":{"share_state":"0"},"count":0}}`, true, true},
		{"错误条目并处于审核中", `{"state":true,"data":{"share_state":0,"count":1,"list":[{"fid":"bad","n":"video.mp4"}]}}`, true, true},
		{"无数据但有审核提示", `{"state":true,"message":"分享审核中"}`, true, true},
		{"空目录不是无法读取", `{"state":true,"data":{"share_state":0,"count":0,"list":[]}}`, false, false},
		{"提取码错误不误判审核", `{"state":false,"error":"提取码错误"}`, true, false},
		{"缺少数据不默认审核", `{"state":true,"data":{}}`, true, false},
		{"空列表缺口不默认审核", `{"state":true,"data":{"count":5,"list":[]}}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Cookie") != "UID=42_A1_100" || r.URL.Query().Get("limit") != "100" {
					t.Error("未直接使用 Cookie 读取列表")
				}
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			_, err := client.List(context.Background(), &ShareInfo{Code: "test"}, "0", 0, "UID=42_A1_100")
			if (err != nil) != tc.failed || errors.Is(err, ErrShareUnderReview) != tc.review || calls != 1 {
				t.Fatalf("读取优先或审核分类错误：%v，请求次数 %d", err, calls)
			}
		})
	}
}

func TestTransferReviewUsesOwnershipFromSameResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body, cookie string
		review             bool
	}{
		{"他人审核中仍有目录", `{"state":true,"data":{"share_state":0,"shareinfo":{"share_state":0,"forbid_reason":""},"userinfo":{"user_id":"99"},"count":1,"list":[{"cid":"123","n":"目录"}]}}`, "UID=42_A1_100", true},
		{"本人审核中且可读", `{"state":true,"data":{"share_state":0,"userinfo":{"user_id":"42"},"count":1,"list":[{"fid":"1","n":"film.mkv","s":"20"}]}}`, "UID=42_A1_100", false},
		{"归属缺失不能转存待审核分享", `{"state":true,"data":{"share_state":"0","count":0,"list":[]}}`, "UID=42_A1_100", true},
		{"本人目录不可读仍拦截", `{"state":true,"data":{"share_state":0,"userinfo":{"user_id":42},"count":1,"list":[]}}`, "UID=42_A1_100", true},
		{"他人已通过审核", `{"state":true,"data":{"share_state":1,"userinfo":{"user_id":99},"count":0,"list":[]}}`, "UID=42_A1_100", false},
		{"缺失审核字段不默认审核", `{"state":true,"data":{"count":0,"list":[]}}`, "UID=42_A1_100", false},
		{"审核文字同样拦截他人分享", `{"state":true,"message":"分享审核中","data":{"count":0,"list":[]}}`, "UID=42_A1_100", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Cookie") != tc.cookie || r.Method != "GET" || r.URL.Query().Get("cid") != "123" {
					t.Error("转存检查未复用带 Cookie 的目录读取")
				}
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := newTestShareClient()
			client.WebURL = server.URL
			_, err := client.ListForTransfer(context.Background(), &ShareInfo{Code: "test"}, "123", 0, tc.cookie)
			if errors.Is(err, ErrShareUnderReview) != tc.review || !tc.review && err != nil || calls != 1 {
				t.Fatal("转存审核判断或请求次数错误", err, calls)
			}
		})
	}
}

func TestShareOwnerComparison(t *testing.T) {
	for _, tc := range []struct {
		owner, cookie string
		matches       bool
	}{
		{`"42"`, "CID=private; UID=42_A1_100; SEID=private", true},
		{`42`, "UID=42_A1_100", true},
		{`"9007199254740993"`, "UID=9007199254740993_A1_100", true},
		{`"9007199254740993"`, "UID=9007199254740992_A1_100", false},
		{`null`, "UID=42_A1_100", false}, {`""`, "UID=_A1_100", false},
		{`0`, "UID=0_A1_100", false}, {`42`, "UID=invalid_A1_100", false},
		{`42`, "", false}, {`"other"`, "UID=other_A1_100", false},
	} {
		response := shareResponse{Data: json.RawMessage(`{"userinfo":{"user_id":` + tc.owner + `}}`)}
		if response.ownedBy(tc.cookie) != tc.matches {
			t.Error("分享者归属判断错误")
		}
	}
}

func TestLaterPageFailureRetainsReviewReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"state":true,"data":{"share_state":0,"count":200,"list":[]}}`))
	}))
	defer server.Close()
	client := newTestShareClient()
	client.WebURL = server.URL
	if _, err := client.List(context.Background(), &ShareInfo{Code: "test"}, "0", 100, "cookie"); !errors.Is(err, ErrShareUnderReview) {
		t.Fatal("后续分页失败丢失审核原因", err)
	}
}
