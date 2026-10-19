package p115

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
