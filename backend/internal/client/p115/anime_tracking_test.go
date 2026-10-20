package p115

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"strm115/backend/internal/model"
)

func TestAnimeStatusExactNormalization(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, tc := range []struct{ name, fields, state string }{
		{"finished exact", `"status":1,"display_status":"finished","percentDone":100,"display_percent":"100"`, "completed"},
		{"waiting", `"status":"1","display_status":"waiting","percentDone":0`, "accepted"},
		{"downloading", `"status":1,"display_status":"running","percentDone":52.5`, "downloading"},
		{"failed explicit", `"status":-1,"display_status":"failed","error":"private URL and Cookie"`, "failed"},
		{"stopped", `"status":0,"display_status":"stopped"`, "failed"},
		{"hundred still running", `"status":1,"display_status":"running","percentDone":100`, "downloading"},
		{"numeric only", `"status":1,"percentDone":100`, "unknown"},
		{"list numeric in exact", `"status":2,"display_status":"finished","percentDone":100`, "unknown"},
		{"new status", `"status":99,"display_status":"finished","percentDone":100`, "unknown"},
		{"null status", `"status":null,"display_status":"finished","percentDone":100`, "unknown"},
		{"empty status", `"status":"","display_status":"finished","percentDone":100`, "unknown"},
		{"text status", `"status":"unknown","display_status":"finished","percentDone":100`, "unknown"},
		{"boolean status", `"status":true,"display_status":"finished","percentDone":100`, "unknown"},
		{"fractional status", `"status":1.5,"display_status":"finished","percentDone":100`, "unknown"},
		{"huge status", `"status":9007199254740993,"display_status":"finished","percentDone":100`, "unknown"},
		{"no progress", `"status":1,"display_status":"finished"`, "unknown"},
		{"null progress", `"status":1,"display_status":"finished","percentDone":null`, "unknown"},
		{"invalid progress", `"status":1,"display_status":"finished","percentDone":"NaN"`, "unknown"},
		{"overflow progress", `"status":1,"display_status":"finished","percentDone":101`, "unknown"},
		{"conflicting progress", `"status":1,"display_status":"finished","percentDone":99,"display_percent":100`, "unknown"},
		{"unfinished progress", `"status":1,"display_status":"finished","percentDone":99`, "unknown"},
		{"error with success", `"status":1,"display_status":"finished","percentDone":100,"error":"private URL and Cookie"`, "unknown"},
		{"unrecognized display", `"status":1,"display_status":"private URL and Cookie","percentDone":100`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"state":true,"data":{"info_hash":"` + hash + `","file_id":9007199254740993,"wp_path_id":"200",` + tc.fields + `}}`
			got, err := parseAnimeTaskStatus([]byte(raw), hash)
			if err != nil || !got.Valid() || got.State != tc.state || got.ResultID != "9007199254740993" || got.Source != model.AnimeOfflineExact {
				t.Fatal(got, err)
			}
			saved, err := json.Marshal(got)
			if err != nil || strings.Contains(string(saved), "private") || strings.Contains(string(saved), "Cookie") {
				t.Fatal("unallowlisted data escaped", string(saved), err)
			}
		})
	}
}

func TestAnimeStatusListsAndMalformedIdentityAreNeverAbsence(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, body := range []string{
		`{"state":true}`, `{"state":true,"tasks":[],"count":0}`,
		`{"state":true,"tasks":[{"info_hash":"` + hash + `","status":2}],"page":1,"total":500}`,
		`{"state":false,"errcode":10021,"data":[]}`,
		`{"state":false,"errcode":10021,"tasks":[],"page":1}`,
		`{"state":false,"errcode":10021,"status":null}`,
		`{"state":true,"info_hash":"` + strings.Repeat("b", 40) + `","status":1}`,
		`{"state":true,"info_hash":"` + hash + `","status":1,"status":2}`,
		`{"state":true,"info_hash":"` + hash + `","file_id":null}`,
	} {
		if got, err := parseAnimeTaskStatus([]byte(body), hash); err == nil {
			t.Fatal("unsafe response accepted", body, got)
		}
	}
	got, err := parseAnimeTaskStatus([]byte(`{"state":false,"errcode":10021}`), hash)
	if err != nil || !got.Valid() || got.Present || got.State != "unknown" {
		t.Fatal("strict absence lost", got, err)
	}
	got, err = parseAnimeTaskStatus([]byte(animeMissingTaskResponse), hash)
	if err != nil || !got.Valid() || got.Present || got.State != "unknown" {
		t.Fatal("upstream empty defaults became a failed read", got, err)
	}
}

func TestAnimeStatusEmptyResultRemainsPresentAndIncomplete(t *testing.T) {
	hash := strings.Repeat("a", 40)
	// Observed immediately after a real accepted submission. Unknown display
	// values and unequal progress must not prove download/file completion.
	raw := `{"state":true,"errcode":0,"info_hash":"` + hash + `","file_id":"","status":0,"display_status":"unrecognized","percentDone":100,"display_percent":99}`
	got, err := parseAnimeTaskStatus([]byte(raw), hash)
	if err != nil || !got.Valid() || !got.Present || got.ResultID != "" || got.State != "unknown" || !got.Malformed {
		t.Fatal("initial result was treated as a missing task, failed read or completion", got, err)
	}
}

func TestAnimeStatusGatewayOnlyOneExactRead(t *testing.T) {
	hash := strings.Repeat("a", 40)
	for _, status := range []int{200, 502} {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			if r.Method != "GET" || r.URL.Path != "/web/" || r.URL.Query().Get("ac") != "get_user_task" || r.URL.Query().Get("info_hash") != hash || r.Header.Get("Cookie") != "UID=123_A" {
				t.Error("unexpected request")
			}
			w.WriteHeader(status)
			w.Write([]byte(`{"state":true,"data":{"info_hash":"` + hash + `","status":1,"display_status":"finished","percentDone":100}}`))
		}))
		client := NewCloudClient()
		client.HTTP, client.BaseURL = server.Client(), server.URL
		got, err := client.ReadAnimeTaskStatus(context.Background(), "UID=123_A", hash)
		server.Close()
		if requests != 1 || status == 200 && (err != nil || got.State != "completed") || status == 502 && err == nil {
			t.Fatal("retried or misclassified", requests, got, err)
		}
	}
}
