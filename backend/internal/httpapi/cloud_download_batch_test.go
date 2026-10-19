package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

func TestCloudBatchAPIStreamsAndValidatesBeforeWriting(t *testing.T) {
	r, accounts, id := setupAccountsAPI(t, nil, nil)
	settings := testCloudDownloadSettings(t, accounts)
	client := &cloudSubmitAPIFake{}
	RegisterCloudDownload(r.Group("/api/admin"), accounts, settings, &service.CloudDownloadService{Accounts: accounts, Client: client, Coordinator: &service.CloudDownloadCoordinator{}}, p115.NewCloudClient(), p115.NewShareClient())
	link := "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)
	for _, tc := range []struct {
		name, body    string
		status, steps int
	}{
		{"valid", `{"urls":["` + link + `"],"targetCID":"99"}`, 200, 2},
		{"invalid-second", `{"urls":["` + link + `","invalid"],"targetCID":"99"}`, 400, 0},
		{"empty", `{"urls":[],"targetCID":"99"}`, 400, 0},
		{"missing-directory", `{"urls":["` + link + `"]}`, 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client.steps = nil
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/p115/cloud-download/tasks/batch?accountId="+id, strings.NewReader(tc.body)))
			if w.Code != tc.status || len(client.steps) != tc.steps {
				t.Fatalf("status=%d body=%s steps=%v", w.Code, w.Body.String(), client.steps)
			}
			if strings.Contains(w.Body.String(), "UID=") {
				t.Fatal("credentials exposed")
			}
			if tc.status != 200 {
				return
			}
			if !w.Flushed || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") || w.Header().Get("X-Accel-Buffering") != "no" {
				t.Fatal("stream not flushed")
			}
			var events []service.CloudDownloadBatchEvent
			for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
				var event service.CloudDownloadBatchEvent
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				events = append(events, event)
			}
			if len(events) != 4 || events[0].Type != "ready" || !events[2].Accepted || events[3].Type != "done" || client.cid != "99" {
				t.Fatalf("events=%+v cid=%s", events, client.cid)
			}
		})
	}
	w := httptest.NewRecorder()
	client.steps = nil
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/p115/cloud-download/tasks/batch?accountId=missing", strings.NewReader(`{"urls":["`+link+`"],"targetCID":"99"}`)))
	if w.Code != 409 || len(client.steps) != 0 {
		t.Fatal("missing account fell back")
	}
}
