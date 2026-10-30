package p115

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestWalkDriveFilesUsesFlatPagesAndKeepsSourceIdentity(t *testing.T) {
	const root = "3516637914517734609"
	const sub = "3516637914517734610"
	const leaf = "3516637914517734611"
	const file1 = "3516637914517734620"
	const file2 = "3516637914517734621"
	var calls []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "fixed-cookie" || r.Method != http.MethodGet {
			t.Error("scan must keep one cookie and use read-only requests")
		}
		q := r.URL.Query()
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/files/file":
			if q.Get("file_id") != root {
				t.Error("root ID lost precision")
			}
			fmt.Fprintf(w, `{"state":true,"data":[{"file_id":"%s","pick_code":"folder123","sha1":""}]}`, root)
		case "/files/downfolders":
			if q.Get("pickcode") != "folder123" || q.Get("per_page") != "1000" {
				t.Error("bad directory batch request")
			}
			if q.Get("page") == "1" {
				// A child can precede its parent in the batched response.
				fmt.Fprintf(w, `{"state":true,"data":{"root":{"fid":"%s"},"count":2,"has_next_page":true,"list":[{"fid":"%s","pid":"%s","fn":"Season 01"}]}}`, root, leaf, sub)
			} else {
				fmt.Fprintf(w, `{"state":true,"data":{"root":{"fid":"%s"},"count":2,"has_next_page":false,"list":[{"fid":"%s","pid":"%s","fn":"动画"}]}}`, root, sub, root)
			}
		case "/files":
			if q.Get("cid") != root || q.Get("cur") != "0" || q.Get("show_dir") != "0" || q.Get("limit") != "1000" {
				t.Error("file batch must recursively request the selected subtree")
			}
			offset := q.Get("offset")
			id, parent, name := file1, leaf, "第01集.mkv"
			if offset == "1" {
				id, parent, name = file2, root, "根目录.mp4"
			}
			fmt.Fprintf(w, `{"state":true,"cid":"%s","count":2,"offset":%s,"data":[{"fid":"%s","cid":"%s","n":"%s","s":123,"sha":"%s","pc":"pick1234"}]}`, root, offset, id, parent, name, strings.Repeat("a", 40))
		default:
			t.Error("unexpected API", r.URL.Path)
		}
	}))
	defer upstream.Close()
	c := newTestShareClient()
	c.WebURL = upstream.URL
	var entries []DriveTreeEntry
	err := c.WalkDriveFiles(context.Background(), root, "fixed-cookie", func(batch []DriveTreeEntry) error {
		entries = append(entries, batch...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"/files/file", "/files/downfolders", "/files/downfolders", "/files", "/files"}) {
		t.Fatalf("requests=%v", calls)
	}
	if len(entries) != 4 || entries[0].ID != sub || !entries[0].Directory || entries[0].RelativePath != "动画" || entries[1].ID != leaf || entries[1].RelativePath != "动画/Season 01" || entries[2].ID != file1 || entries[2].ParentCID != leaf || entries[2].RelativePath != "动画/Season 01/第01集.mkv" || entries[2].SHA1 != strings.Repeat("A", 40) || entries[3].RelativePath != "根目录.mp4" {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestWalkDriveFilesRejectsIncompleteOrForeignTree(t *testing.T) {
	for _, mode := range []string{"wrong-root", "root-is-file", "folder-root", "folder-count", "folder-cycle", "foreign-folder", "folder-repeat", "folder-traversal", "folder-page", "folder-depth", "foreign-file", "foreign-file-page", "file-repeat", "file-count-change", "file-offset", "file-hash", "file-identity", "file-directory-collision", "file-traversal", "file-empty-page"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/files/file":
					id, hash := "100", ""
					if mode == "wrong-root" {
						id = "999"
					}
					if mode == "root-is-file" {
						hash = strings.Repeat("A", 40)
					}
					fmt.Fprintf(w, `{"state":true,"data":[{"file_id":"%s","pick_code":"folder123","sha1":"%s"}]}`, id, hash)
				case "/files/downfolders":
					if mode == "folder-depth" {
						fmt.Fprint(w, `{"state":true,"data":{"root":{"fid":"100"},"count":65,"has_next_page":false,"list":[`)
						for i := 1; i <= 65; i++ {
							if i > 1 {
								fmt.Fprint(w, ",")
							}
							fmt.Fprintf(w, `{"fid":"%d","pid":"%d","fn":"folder"}`, 100+i, 99+i)
						}
						fmt.Fprint(w, `]}}`)
						return
					}
					parent, name, tail, more, root, count := "100", "Season 01", "", "false", "100", 1
					if mode == "folder-root" {
						root = "999"
					}
					if mode == "folder-count" {
						count = 2
					}
					if mode == "folder-cycle" {
						parent = "200"
					}
					if mode == "foreign-folder" {
						parent = "999"
					}
					if mode == "folder-repeat" {
						tail = `,{"fid":"200","pid":"100","fn":"duplicate"}`
						count = 2
					}
					if mode == "folder-traversal" {
						name = ".."
					}
					if mode == "folder-page" {
						more = "null"
					}
					fmt.Fprintf(w, `{"state":true,"data":{"root":{"fid":"%s"},"count":%d,"has_next_page":%s,"list":[{"fid":"200","pid":"%s","fn":"%s"}%s]}}`, root, count, more, parent, name, tail)
				case "/files":
					calls++
					if mode == "foreign-file-page" {
						fmt.Fprint(w, `{"state":true,"cid":"999","count":0,"offset":0,"data":[]}`)
						return
					}
					parent, name, hash, id, offset, count := "200", "01.mkv", strings.Repeat("A", 40), "300", calls-1, 1
					if mode == "foreign-file" {
						parent = "999"
					}
					if mode == "file-repeat" {
						count = 2
					}
					if mode == "file-count-change" {
						count = calls + 1
						id = fmt.Sprint(300 + calls)
					}
					if mode == "file-offset" {
						offset = 1
					}
					if mode == "file-hash" {
						hash = "invalid"
					}
					if mode == "file-identity" {
						id = "0"
					}
					if mode == "file-directory-collision" {
						id = "200"
					}
					if mode == "file-traversal" {
						name = "../01.mkv"
					}
					if mode == "file-empty-page" {
						fmt.Fprint(w, `{"state":true,"cid":"100","count":1,"offset":0,"data":[]}`)
						return
					}
					fmt.Fprintf(w, `{"state":true,"cid":"100","count":%d,"offset":%d,"data":[{"fid":"%s","cid":"%s","n":"%s","s":123,"sha":"%s","pc":"pick1234"}]}`, count, offset, id, parent, name, hash)
				}
			}))
			defer upstream.Close()
			c := newTestShareClient()
			c.WebURL = upstream.URL
			if err := c.WalkDriveFiles(context.Background(), "100", "fixed", func([]DriveTreeEntry) error { return nil }); !errors.Is(err, ErrDriveTreeIncomplete) || errors.Is(err, ErrDriveTreeUnsupported) {
				t.Fatalf("incomplete/foreign subtree must not permit fallback: %v", err)
			}
		})
	}
}

func TestWalkDriveFilesEmptyRootAndFallbackClassification(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotImplemented, http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
				switch r.URL.Path {
				case "/files/file":
					fmt.Fprint(w, `{"state":true,"data":[{"file_id":"100","pick_code":"folder123","sha1":""}]}`)
				case "/files/downfolders":
					fmt.Fprint(w, `{"state":true,"data":{"root":{"fid":"100"},"count":0,"has_next_page":false,"list":[]}}`)
				case "/files":
					fmt.Fprint(w, `{"state":true,"path":[{"cid":"0"},{"cid":"100"}],"count":0,"offset":0,"data":[]}`)
				}
			}))
			defer upstream.Close()
			c := newTestShareClient()
			c.WebURL = upstream.URL
			consumerCalls := 0
			consume := func([]DriveTreeEntry) error { consumerCalls++; return nil }
			if err := c.WalkDriveFiles(context.Background(), "0", "fixed", consume); !errors.Is(err, ErrDriveTreeUnsupported) || calls != 0 {
				t.Fatalf("virtual root fallback err=%v calls=%d", err, calls)
			}
			err := c.WalkDriveFiles(context.Background(), "100", "fixed", consume)
			if status == http.StatusOK {
				if err != nil || calls != 3 || consumerCalls != 0 {
					t.Fatalf("empty tree err=%v calls=%d callbacks=%d", err, calls, consumerCalls)
				}
			} else if err == nil || errors.Is(err, ErrDriveTreeUnsupported) != (status == http.StatusNotImplemented) || calls != 1 {
				t.Fatalf("HTTP %d classification err=%v calls=%d", status, err, calls)
			}
		})
	}
}

func TestWalkDriveFilesStopsOnCancellationOrConsumerFailure(t *testing.T) {
	for _, cancelDuringCallback := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelDuringCallback), func(t *testing.T) {
			fileRequests := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/files/file":
					fmt.Fprint(w, `{"state":true,"data":[{"file_id":"100","pick_code":"folder123","sha1":""}]}`)
				case "/files/downfolders":
					fmt.Fprint(w, `{"state":true,"data":{"root":{"fid":"100"},"count":1,"has_next_page":false,"list":[{"fid":"200","pid":"100","fn":"Season 01"}]}}`)
				case "/files":
					fileRequests++
				}
			}))
			defer upstream.Close()
			c := newTestShareClient()
			c.WebURL = upstream.URL
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			consumerErr := errors.New("local output stopped")
			err := c.WalkDriveFiles(ctx, "100", "fixed", func([]DriveTreeEntry) error {
				if cancelDuringCallback {
					cancel()
					return nil
				}
				return consumerErr
			})
			want := consumerErr
			if cancelDuringCallback {
				want = context.Canceled
			}
			if !errors.Is(err, want) || fileRequests != 0 {
				t.Fatalf("err=%v requests=%d", err, fileRequests)
			}
		})
	}
}
