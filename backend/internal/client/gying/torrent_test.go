package gying

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTorrentTicketRefreshAndPrivacy(t *testing.T) {
	hash, signature := strings.Repeat("a", 40), strings.Repeat("b", 44)
	detailCalls, torrentCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/res/downurl/mv/Film":
			detailCalls++
			_, _ = w.Write([]byte(`{"downlist":{"hex":"` + signature + `","list":{"m":["` + hash + `"],"u":["Torrent"],"k":[0]}}}`))
		case r.URL.Path == "/dbt/Old/"+signature:
			torrentCalls++
			w.WriteHeader(403)
			_, _ = w.Write([]byte("<html>链接过期</html>"))
		case r.URL.Path == "/dbt/Torrent/"+signature:
			torrentCalls++
			_, _ = w.Write([]byte("torrent bytes"))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	body, err := client.ReadTorrent(context.Background(), "mv", "Film", hash, "/dbt/Old/"+signature)
	if err != nil || string(body) != "torrent bytes" || detailCalls != 1 || torrentCalls != 2 {
		t.Fatal("expired ticket was not refreshed exactly once", err, detailCalls, torrentCalls)
	}
	_, err = client.ReadTorrent(context.Background(), "mv", "Film", hash, "/dbt/Torrent/"+signature)
	if err != nil || detailCalls != 1 || torrentCalls != 3 {
		t.Fatal("valid search ticket caused another detail request", err)
	}
	var detail detailData
	_ = json.Unmarshal([]byte(`{"downlist":{"hex":"`+signature+`","list":{"m":["`+hash+`"],"u":["Torrent"],"k":[0]}}}`), &detail)
	links := detailLinks(detail)
	raw, err := json.Marshal(links)
	if err != nil || len(links) != 1 || !links[0].HasTorrent || links[0].TorrentPath == "" || strings.Contains(string(raw), signature) || strings.Contains(string(raw), "/dbt/") {
		t.Fatal("download ticket exposed or unavailable", err, string(raw))
	}
	for _, values := range [][3]string{{"mv", "../Film", hash}, {"http:", "Film", hash}, {"mv", "Film", "invalid"}} {
		if _, err := client.ReadTorrent(context.Background(), values[0], values[1], values[2], ""); !errors.Is(err, ErrInput) {
			t.Fatal("invalid source accepted", values, err)
		}
	}
}

func TestTorrentDoesNotFollowExternalRedirectOrExposeTicket(t *testing.T) {
	externalCalls := 0
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalCalls++ }))
	defer external.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, external.URL+"/private", http.StatusFound)
	}))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	signature := strings.Repeat("c", 44)
	_, err := client.ReadTorrent(context.Background(), "tv", "Show", strings.Repeat("a", 40), "/dbt/Torrent/"+signature)
	if err == nil || externalCalls != 0 || strings.Contains(err.Error(), signature) || strings.Contains(err.Error(), external.URL) {
		t.Fatal("followed redirect or exposed ticket", err, externalCalls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ReadTorrent(ctx, "tv", "Show", strings.Repeat("a", 40), "/dbt/Torrent/"+signature); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}
