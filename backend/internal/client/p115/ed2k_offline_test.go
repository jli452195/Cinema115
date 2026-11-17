package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestED2KTaskObservedRealDirectoryResult(t *testing.T) {
	hash := "f1ca4d4add5624f5082bd2405af23d25"
	// 真实接口的 file_id 是结果目录，响应没有 wp_path_id，必须由服务核验目录归属。
	raw := `{"state":true,"info_hash":"` + hash + `","file_id":"3564133600913785924","display_status":"finished","display_percent":100,"percentDone":100,"status":1,"size":191712101}`
	got, err := parseED2KTask([]byte(raw), hash)
	if err != nil || !got.Present || got.Hash != hash || got.State != "completed" || got.TargetCID != "" || got.ResultID != "3564133600913785924" {
		t.Fatalf("observation=%+v err=%v", got, err)
	}
}

func TestED2KTaskRejectsAmbiguousIdentityAndProgress(t *testing.T) {
	hash := strings.Repeat("a", 32)
	for _, raw := range []string{
		`{"state":true,"info_hash":"` + strings.Repeat("a", 40) + `","display_status":"running"}`,
		`{"state":true,"info_hash":"` + hash + `","hash":"` + strings.Repeat("b", 32) + `","display_status":"running"}`,
		`{"state":true,"display_status":"running"}`,
		`{"state":true,"info_hash":"` + hash + `","display_status":"finished","percentDone":100,"display_percent":99}`,
		`{"state":true,"info_hash":"` + hash + `","display_status":"finished","percentDone":"100x"}`,
		`{"state":true,"info_hash":"` + hash + `","file_id":0,"display_status":"finished","percentDone":100}`,
		`{"state":true,"info_hash":"` + hash + `","file_id":1.5,"display_status":"running"}`,
		`{"state":false,"errcode":10021,"file_id":"300"}`,
	} {
		if got, err := parseED2KTask([]byte(raw), hash); err == nil {
			t.Fatalf("accepted ambiguous task: %+v", got)
		}
	}
	got, err := parseED2KTask([]byte(animeMissingTaskResponse), hash)
	if err != nil || got.Present {
		t.Fatalf("missing task=%+v err=%v", got, err)
	}
}

func TestED2KTaskInspectionOnlyReadsExactHash(t *testing.T) {
	hash := strings.Repeat("a", 32)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/web/" || r.URL.Query().Get("ac") != "get_user_task" || r.URL.Query().Get("info_hash") != hash || r.Header.Get("Cookie") != "UID=123_A; SEID=fixed" {
			t.Error("unexpected ED2K request")
		}
		_, _ = w.Write([]byte(`{"state":true,"info_hash":"` + hash + `","display_status":"running"}`))
	}))
	defer server.Close()
	client := NewCloudClient()
	client.HTTP, client.BaseURL = server.Client(), server.URL
	got, err := client.InspectED2KTask(context.Background(), "UID=123_A; SEID=fixed", hash)
	if err != nil || got.State != "downloading" || requests != 1 {
		t.Fatalf("observation=%+v requests=%d err=%v", got, requests, err)
	}
}
