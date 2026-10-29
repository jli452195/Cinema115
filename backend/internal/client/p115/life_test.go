package p115

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLifeEventsAreReadOnlyAndRepeatable(t *testing.T) {
	requests := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/behavior/detail" || r.Header.Get("Cookie") != "life-cookie" || r.ContentLength > 0 {
			t.Errorf("unexpected life request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("limit") != "100" || len(r.URL.Query()) != 2 {
			t.Errorf("unexpected life query keys")
		}
		w.Write([]byte(`{"state":true,"data":{"count":"2","list":[{"id":"3516637914517734609","type":2,"file_id":3516637914517734610,"parent_id":"0","update_time":"1793200000"},{"id":3516637914517734608,"type":"8","file_id":"3516637914517734610"}]}}`))
	}))
	defer s.Close()
	c := newTestShareClient()
	c.WebURL = s.URL
	a, err := c.LifeEvents(context.Background(), 0, 100, "life-cookie")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.LifeEvents(context.Background(), 0, 100, "life-cookie")
	if err != nil || !reflect.DeepEqual(a, b) || requests != 2 {
		t.Fatalf("repeat read changed history: %v %v", b, err)
	}
	if a.Count != 2 || a.Events[0].ID != "3516637914517734609" || a.Events[0].ParentID != "0" || !a.Events[0].ChangesDirectory() || a.Events[1].ChangesDirectory() {
		t.Fatalf("invalid event decode: %+v", a)
	}
}

func TestLifeEventsRejectInvalidWindowAndMalformedPage(t *testing.T) {
	for _, data := range []string{
		`{"count":1,"list":[{"id":"1","type":2,"parent_id":"../../x"}]}`,
		`{"count":1,"list":[{"id":"1.2","type":2}]}`,
		`{"count":1,"list":[{"id":"1","type":2,"update_time":-1}]}`,
		`{"count":0,"list":null}`,
		`{"count":-1,"list":[]}`,
	} {
		t.Run(data, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"state":true,"data":` + data + `}`))
			}))
			defer s.Close()
			c := newTestShareClient()
			c.WebURL = s.URL
			if _, err := c.LifeEvents(context.Background(), 0, 100, "cookie"); err == nil {
				t.Fatal("accepted malformed history")
			}
		})
	}
	c := newTestShareClient()
	for _, window := range [][2]int{{-1, 1}, {0, 0}, {0, 1001}, {9999, 2}} {
		if _, err := c.LifeEvents(context.Background(), window[0], window[1], "cookie"); err == nil {
			t.Fatal("accepted invalid paging")
		}
	}
}
