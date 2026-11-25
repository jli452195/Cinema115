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
	otherLink := "ed2k://|file|example.mkv|100|" + strings.Repeat("b", 32) + "|/"
	for _, tc := range []struct {
		name, body, step, eventOrder string
		status                       int
	}{
		{name: "single", body: `{"urls":["` + link + `"],"targetCID":"99"}`, step: "add", eventOrder: "ready,start,result,done", status: 200},
		{name: "batch", body: `{"urls":["` + link + `","` + otherLink + `"],"targetCID":"99"}`, step: "batch", eventOrder: "ready,start,start,result,result,done", status: 200},
		{name: "invalid-second", body: `{"urls":["` + link + `","invalid"],"targetCID":"99"}`, status: 400},
		{name: "empty", body: `{"urls":[],"targetCID":"99"}`, status: 400},
		{name: "missing-directory", body: `{"urls":["` + link + `"]}`, status: 400},
		{name: "body-too-large", body: `{"urls":["` + link + `&dn=` + strings.Repeat("x", 256<<10) + `"],"targetCID":"99"}`, status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client.steps = nil
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/admin/p115/cloud-download/tasks/batch?accountId="+id, strings.NewReader(tc.body)))
			if w.Code != tc.status || strings.Join(client.steps, ",") != tc.step {
				t.Fatalf("status=%d body=%s steps=%v", w.Code, w.Body.String(), client.steps)
			}
			if strings.Contains(w.Body.String(), "UID=") {
				t.Fatal("credentials exposed")
			}
			if tc.status != 200 {
				if tc.status == http.StatusRequestEntityTooLarge && (!strings.Contains(w.Body.String(), "本次未提交") || strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson")) {
					t.Fatal("请求体过大必须明确拒绝且不能开启提交流", w.Body.String())
				}
				return
			}
			if !w.Flushed || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") || w.Header().Get("X-Accel-Buffering") != "no" {
				t.Fatal("stream not flushed")
			}
			var eventTypes []string
			resultIndex := 0
			for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
				var event service.CloudDownloadBatchEvent
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				eventTypes = append(eventTypes, event.Type)
				if event.Type == "result" {
					if !event.Accepted || event.Index != resultIndex {
						t.Fatalf("unexpected result: %+v", event)
					}
					resultIndex++
				}
			}
			if strings.Join(eventTypes, ",") != tc.eventOrder || client.cid != "99" {
				t.Fatalf("events=%v cid=%s", eventTypes, client.cid)
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
