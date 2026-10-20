package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnimeRenameReceiptMatchesEveryRequestedIDAndName(t *testing.T) {
	entries := []RenameTarget{{"3516637914517734609", "作品.S01E01.字幕组.mkv"}, {"3516637914517734610", "作品.S01E01.字幕组.chs.ass"}}
	for _, tc := range []struct {
		name string
		body string
		ok   bool
	}{
		{"full report", `{"state":true,"error":"","data":{"3516637914517734610":"作品.S01E01.字幕组.chs.ass","3516637914517734609":"作品.S01E01.字幕组.mkv"}}`, true},
		{"missing member", `{"state":true,"data":{"3516637914517734609":"作品.S01E01.字幕组.mkv"}}`, false},
		{"wrong name", `{"state":true,"data":{"3516637914517734609":"wrong.mkv","3516637914517734610":"作品.S01E01.字幕组.chs.ass"}}`, false},
		{"wrong id", `{"state":true,"data":{"3516637914517734611":"作品.S01E01.字幕组.mkv","3516637914517734610":"作品.S01E01.字幕组.chs.ass"}}`, false},
		{"extra member", `{"state":true,"data":{"3516637914517734609":"作品.S01E01.字幕组.mkv","3516637914517734610":"作品.S01E01.字幕组.chs.ass","3":"other"}}`, false},
		{"duplicate id", `{"state":true,"data":{"3516637914517734609":"wrong.mkv","3516637914517734609":"作品.S01E01.字幕组.mkv","3516637914517734610":"作品.S01E01.字幕组.chs.ass"}}`, false},
		{"item status", `{"state":true,"data":{"3516637914517734609":{"state":true},"3516637914517734610":{"state":false}}}`, false},
		{"array", `{"state":true,"data":["作品.S01E01.字幕组.mkv","作品.S01E01.字幕组.chs.ass"]}`, false},
		{"async", `{"state":true,"task_id":"123","data":{"3516637914517734609":"作品.S01E01.字幕组.mkv","3516637914517734610":"作品.S01E01.字幕组.chs.ass"}}`, false},
		{"contradictory error", `{"state":true,"errno":1,"data":{"3516637914517734609":"作品.S01E01.字幕组.mkv","3516637914517734610":"作品.S01E01.字幕组.chs.ass"}}`, false},
		{"rejected", `{"state":false,"data":{"3516637914517734609":"作品.S01E01.字幕组.mkv","3516637914517734610":"作品.S01E01.字幕组.chs.ass"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := animeRenameReceipt([]byte(tc.body), entries)
			if (err == nil) != tc.ok || err != nil && !errors.Is(err, ErrAnimeBatchReceipt) {
				t.Fatal("incorrect rename receipt", err)
			}
			if err := animeBatchReceipt([]byte(tc.body)); err == nil {
				t.Fatal("rename report must not prove a move")
			}
		})
	}
}

func TestAnimeRenameReportNeedsOnePostAndNoReadback(t *testing.T) {
	const id = "3516637914517734609"
	const name = "作品.S01E01.字幕组.mkv"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/files/batch_rename" {
			t.Error("rename must not perform an extra read", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("files_new_name["+id+"]") != name {
			t.Error("incorrect submitted name", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"state": true, "error": "", "data": map[string]string{id: name}})
	}))
	defer server.Close()
	c := newTestShareClient()
	c.WebURL = server.URL
	if err := c.RenameAnimeEntries(context.Background(), []RenameTarget{{id, name}}, "fixed-cookie"); err != nil || calls != 1 {
		t.Fatal("rename report was rejected or generated extra requests", calls, err)
	}
}

func TestAnimeReceiptDiagnosticDoesNotRetainUpstreamContent(t *testing.T) {
	for _, tc := range []struct{ body, reason string }{
		{`{"state":true,"error":"private-cookie-value"}`, "error_field"},
		{`{"state":true,"data":"private-cookie-value"}`, "data_string"},
		{`{"state":true,"data":true}`, "data_boolean"},
		{`{"state":true,"data":{"private-cookie-value":1}}`, "data_object"},
		{`{"state":true,"data":["private-cookie-value"]}`, "data_array"},
		{`{"state":true,"data":123}`, "data_other"},
		{`{"state":true,"private-cookie-value":1}`, "unexpected_field"},
		{`{"state":"private-cookie-value"}`, "invalid_envelope"},
	} {
		err := animeBatchReceipt([]byte(tc.body))
		var receipt *AnimeBatchReceiptError
		if !errors.Is(err, ErrAnimeBatchReceipt) || !errors.As(err, &receipt) || receipt.Reason() != tc.reason || err.Error() != ErrAnimeBatchReceipt.Error()+": "+tc.reason {
			t.Fatal("unsafe or incorrect receipt diagnostic", err)
		}
	}
}

func TestAnimeBatchAcceptsOnlyClearSynchronousReceipts(t *testing.T) {
	for _, body := range []string{`{"state":true}`, `{"state":1,"errno":0}`, `{"state":true,"data":[]}`, `{"state":true,"data":null}`, `{"state":true,"error":""}`} {
		if err := animeBatchReceipt([]byte(body)); err != nil {
			t.Fatal(body, err)
		}
	}
	for _, body := range []string{`{}`, `{"state":false}`, `{"state":true,"errno":1}`, `{"state":true,"error":"partial failure"}`, `{"state":true,"data":{"400":{"state":true},"401":{"state":false}}}`, `{"state":true,"task_id":"123"}`, `{"state":true,"state":false}`, `{"state":true,"data":[{"id":"400"}]}`} {
		if err := animeBatchReceipt([]byte(body)); err == nil {
			t.Fatal("ambiguous batch marked successful", body)
		}
	}
}

func TestAnimeBatchSinglePostFormsAndAmbiguousNoRetry(t *testing.T) {
	for _, body := range []string{`{"state":true}`, `{"state":true,"data":{"partial":true}}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" {
					t.Error("mutations must be POST")
				}
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.URL.Path == "/files/batch_rename" && r.PostForm.Get("files_new_name[3516637914517734609]") != "作品.S01E01.字幕组.mkv" {
					t.Error(r.PostForm)
				}
				if r.URL.Path == "/files/move" && (r.PostForm.Get("fid[0]") != "3516637914517734609" || r.PostForm.Get("pid") != "900") {
					t.Error(r.PostForm)
				}
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			c := newTestShareClient()
			c.WebURL = server.URL
			c.ReadRetryDelays = []time.Duration{time.Millisecond}
			renameErr := c.RenameAnimeEntries(context.Background(), []RenameTarget{{ID: "3516637914517734609", Name: "作品.S01E01.字幕组.mkv"}}, "UID=123_A")
			moveErr := c.MoveAnimeEntries(context.Background(), []string{"3516637914517734609"}, "900", "UID=123_A")
			wantError := body != `{"state":true}`
			if (renameErr != nil) != wantError || (moveErr != nil) != wantError || calls != 2 {
				t.Fatal("incorrect receipt or write replay", calls, renameErr, moveErr)
			}
		})
	}
}
