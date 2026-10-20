package p115

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Observed get_user_task response for a missing task: 115 includes empty task
// defaults even though state=false and errcode=10021.
const animeMissingTaskResponse = `{"info_hash":"","status":0,"url":"","size":0,"error_msg":"用户任务不存在","errcode":10021,"file_id":"","percentDone":0,"display_percent":0,"retry_count":0,"retry_limit":0,"state":false,"name":"","move":0,"display_status":"","status_text":""}`

func TestAnimeTaskObservationStrictIdentityAndAbsence(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, body     string
		ok, present    bool
		target, result string
	}{
		{"absent", `{"state":false,"errcode":10021}`, true, false, "", ""},
		{"absent with upstream defaults", animeMissingTaskResponse, true, false, "", ""},
		{"exact", `{"state":true,"data":{"info_hash":"` + hash + `","wp_path_id":"9007199254740993","file_id":300}}`, true, true, "9007199254740993", "300"},
		{"root payload", `{"state":1,"info_hash":"` + hash + `","file_id":"300"}`, true, true, "", "300"},
		{"missing state", `{"info_hash":"` + hash + `"}`, false, false, "", ""},
		{"wrong flag", `{"state":"false","errcode":10021}`, false, false, "", ""},
		{"false with data", `{"state":false,"errcode":10021,"data":{"info_hash":"` + hash + `"}}`, false, false, "", ""},
		{"false with identity", `{"state":false,"errcode":10021,"hash":"` + hash + `"}`, false, false, "", ""},
		{"false with result", `{"state":false,"errcode":10021,"file_id":"300"}`, false, false, "", ""},
		{"malformed code", `{"state":false,"errcode":10021.5}`, false, false, "", ""},
		{"conflicting code", `{"state":false,"errcode":10021,"errno":5}`, false, false, "", ""},
		{"error success", `{"state":true,"errcode":10021}`, false, false, "", ""},
		{"duplicate state", `{"state":false,"state":true,"info_hash":"` + hash + `"}`, false, false, "", ""},
		{"duplicate nested hash", `{"state":true,"data":{"info_hash":"` + hash + `","info_hash":"` + hash + `"}}`, false, false, "", ""},
		{"conflicting aliases", `{"state":true,"info_hash":"` + hash + `","hash":"` + strings.Repeat("b", 40) + `"}`, false, false, "", ""},
		{"invalid directory", `{"state":true,"info_hash":"` + hash + `","wp_path_id":1.2}`, false, false, "", ""},
		{"zero target", `{"state":true,"info_hash":"` + hash + `","wp_path_id":"0"}`, false, false, "", ""},
		{"numeric hash", `{"state":true,"info_hash":123}`, false, false, "", ""},
		{"null success", `{"state":true,"data":null}`, false, false, "", ""},
		{"trailing", `{"state":false,"errcode":10021} {}`, false, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := parseAnimeTask([]byte(tc.body), hash)
			if (err == nil) != tc.ok || tc.ok && (v.Present != tc.present || v.TargetCID != tc.target || v.ResultID != tc.result) {
				t.Fatal(v, err)
			}
		})
	}
}

func TestAnimeMissingTaskDefaultsRejectContradictions(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for field, value := range map[string]any{
		"info_hash": hash, "infoHash": hash, "hash": hash,
		"file_id": "300", "wp_path_id": "200", "url": "magnet:?xt=urn:btih:" + hash,
		"status": 1, "stat": 2, "percentDone": 100, "display_percent": 100,
		"progress": 100, "display_status": "finished", "name": "old task",
		"tasks": []any{}, "list": []any{}, "count": 0, "total": 0, "page": 1, "page_count": 1,
		"data": map[string]any{},
	} {
		t.Run(field, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(animeMissingTaskResponse), &payload); err != nil {
				t.Fatal(err)
			}
			payload[field] = value
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseAnimeTask(raw, hash); !errors.Is(err, ErrAnimeOfflineResponse) {
				t.Fatal("contradictory miss authorized submission", err)
			}
			if _, err := parseAnimeTaskStatus(raw, hash); !errors.Is(err, ErrAnimeOfflineResponse) {
				t.Fatal("contradictory miss became status observation", err)
			}
		})
	}
}

func TestAnimeOfflineGatewayHasNoDeleteAndChecksEnvelopes(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, body string
		want       error
	}{
		{"accepted", `{"state":1}`, nil},
		{"receipt", `{"state":true,"data":{"info_hash":"` + hash + `","wp_path_id":"200"}}`, nil},
		{"missing state", `{}`, ErrAnimeOfflineResponse},
		{"rejected", `{"state":false,"errcode":403,"error":"private cookie"}`, ErrAnimeSubmitRejected},
		{"exists unclear", `{"state":false,"errcode":10008}`, ErrAnimeOfflineResponse},
		{"hash mismatch", `{"state":true,"data":{"info_hash":"` + strings.Repeat("b", 40) + `"}}`, ErrAnimeOfflineResponse},
		{"alias mismatch", `{"state":true,"data":{"hash":"` + strings.Repeat("b", 40) + `"}}`, ErrAnimeOfflineResponse},
		{"directory mismatch", `{"state":true,"data":{"wp_path_id":"999"}}`, ErrAnimeOfflineResponse},
		{"invalid data", `{"state":true,"data":[]}`, ErrAnimeOfflineResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/lixianssp/" || r.Method != "POST" || r.Header.Get("Cookie") != "UID=123_A; SEID=fixed" {
					t.Error("unexpected gateway request")
				}
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := NewCloudClient()
			client.HTTP = server.Client()
			client.BaseURL = server.URL
			err := client.SubmitAnimeTask(context.Background(), "UID=123_A; SEID=fixed", hash, "200")
			if !errors.Is(err, tc.want) || requests != 1 {
				t.Fatal("gateway retried or misclassified", requests, err)
			}
		})
	}
}

func TestAnimeOfflineInspectUsesOnlyExactHashEndpoint(t *testing.T) {
	hash := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/web/" || r.URL.Query().Get("ac") != "get_user_task" || r.URL.Query().Get("info_hash") != hash || r.Header.Get("Cookie") != "UID=123_A" {
			t.Error("wrong exact query")
		}
		w.Write([]byte(animeMissingTaskResponse))
	}))
	defer server.Close()
	client := NewCloudClient()
	client.HTTP = server.Client()
	client.BaseURL = server.URL
	v, err := client.InspectAnimeTask(context.Background(), "UID=123_A", hash)
	if err != nil || v.Present {
		t.Fatal(v, err)
	}
	for _, bad := range []string{"", strings.Repeat("a", 32), strings.Repeat("g", 40), strings.ToUpper(hash)} {
		if _, err := client.InspectAnimeTask(context.Background(), "UID=123_A", bad); !errors.Is(err, ErrCloudInput) {
			t.Fatal("invalid input queried", err)
		}
	}
}
