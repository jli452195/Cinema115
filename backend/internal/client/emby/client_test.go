package emby

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientRejectsRedirectsAndInvalidURLs(t *testing.T) {
	for _, raw := range []string{"ftp://server", "http://user:password@server", "http://server?api_key=secret", "http://server#fragment", "server"} {
		if _, err := NormalizeURL(raw); err == nil {
			t.Fatalf("未拒绝无效地址 %q", raw)
		}
	}
	if got, err := NormalizeURL("http://server/prefix/emby/"); err != nil || got != "http://server/prefix" {
		t.Fatal(got, err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("不应将密钥发送到重定向目标") }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer origin.Close()
	if _, err := New(origin.URL, "private-key").Info(context.Background()); err == nil || strings.Contains(err.Error(), "private-key") {
		t.Fatal("未拒绝重定向或泄露密钥", err)
	}
}
func TestEmbyResponsePreservesIDsAndStripsPrivateMediaFields(t *testing.T) {
	var page Page
	err := json.Unmarshal([]byte(`{"Items":[{"Id":"9007199254740993","Type":"Episode","SeriesId":"series","ParentId":"season","ParentIndexNumber":1,"IndexNumber":2,"IndexNumberEnd":3,"ProviderIds":{"Tmdb":"777"},"MediaSources":[{"Container":"mkv","Size":123,"Path":"private-path","RequiredHttpHeaders":{"Cookie":"private-cookie"},"MediaStreams":[{"Type":"Video","Codec":"hevc","Width":3840,"Height":2160,"VideoRange":"HDR"}]}]}],"TotalRecordCount":1}`), &page)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := page.Items[0].Record("lib")
	if !ok || item.ID != "9007199254740993" || item.SeriesID != "series" || item.Season != 1 || item.EndEpisode != 3 {
		t.Fatal(item)
	}
	data, _ := json.Marshal(page.Items[0].MediaSources)
	if strings.Contains(string(data), "private-") {
		t.Fatal("媒体版本响应泄露私人路径或请求头")
	}
}
