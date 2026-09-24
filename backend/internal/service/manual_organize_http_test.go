package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"strm115/backend/internal/client/p115"
)

// Exercise the actual form encoding, response parsing, context timing observer
// and batch reconciler together against a local in-memory drive.
func TestManualOrganizeBatchHTTPIntegrationAndStats(t *testing.T) {
	s, drive, id, plan := organizeBatchFixture(t, 51)
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Cookie") != "UID=123_snapshot; SEID=secret-cookie" {
			t.Error("账号凭据未固定")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/files":
			cid := r.Form.Get("cid")
			offset, _ := strconv.Atoi(r.Form.Get("offset"))
			page, err := drive.DriveFiles(r.Context(), cid, offset, r.Header.Get("Cookie"))
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			data := make([]map[string]any, 0, len(page.Files))
			for _, file := range page.Files {
				entry := map[string]any{"fid": file.ID, "cid": cid, "n": file.Name, "s": file.Size}
				if file.Directory {
					entry["fid"], entry["cid"] = "0", file.ID
				}
				data = append(data, entry)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"state": true, "cid": cid, "count": page.Count, "data": data})
		case "/files/batch_rename":
			entries := make([]p115.RenameTarget, 0, len(r.PostForm))
			for key := range r.PostForm {
				if !strings.HasPrefix(key, "files_new_name[") || !strings.HasSuffix(key, "]") {
					t.Error("非法改名参数", key)
					continue
				}
				entries = append(entries, p115.RenameTarget{ID: strings.TrimSuffix(strings.TrimPrefix(key, "files_new_name["), "]"), Name: r.PostForm.Get(key)})
			}
			if err := drive.RenameEntries(r.Context(), entries, r.Header.Get("Cookie")); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"state":true}`))
		case "/files/move":
			ids := make([]string, 0, len(r.PostForm)-1)
			for i := 0; ; i++ {
				value := r.PostForm.Get(fmt.Sprintf("fid[%d]", i))
				if value == "" {
					break
				}
				ids = append(ids, value)
			}
			if err := drive.MoveEntriesBatch(r.Context(), ids, r.PostForm.Get("pid"), r.Header.Get("Cookie")); err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"state":true}`))
		default:
			t.Error("unexpected operation", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	client := p115.NewShareClient()
	client.WebURL = server.URL
	client.RequestInterval = 0
	client.ReadRetryDelays = nil
	s.drive = client
	job := organizeTestRun(t, s, id, plan)
	mu.Lock()
	count := requests
	mu.Unlock()
	if job.Status != "completed" || job.Stats.HTTPRequests != count || count != 16 || job.Stats.DirectoryRequests != 12 || job.Stats.RenameRequests != 2 || job.Stats.MoveRequests != 2 {
		t.Fatalf("执行或统计不正确: status=%s requests=%d stats=%+v", job.Status, count, job.Stats)
	}
	if job.Stats.ElapsedMS < 0 || job.Stats.SaveMS < 0 || job.Stats.StartedAt.IsZero() {
		t.Fatal("耗时统计无效")
	}
	if _, err := s.Start(id, plan.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
}
