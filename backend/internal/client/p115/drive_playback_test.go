package p115

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestDrivePlaybackURLIdentityAndCookieRequirement(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{`{"12":{"file_id":"12","pick_code":"pick1234","url":{"url":"https://cdn.115.com/video?f=1"}}}`, true},
		{`{"13":{"url":{"url":"https://cdn.115.com/video"}}}`, false},
		{`{"12":{"file_id":"13","url":{"url":"https://cdn.115.com/video"}}}`, false},
		{`{"12":{"pick_code":"other1234","url":{"url":"https://cdn.115.com/video"}}}`, false},
		{`{"12":{"url":{"url":"http://127.0.0.1/"}}}`, false},
		{`{"12":{"url":{"url":"https://cdn.115.com/video?f=3"}}}`, false},
		{`{"12":{"url":{"url":"https://cdn.115.com/video"}},"13":{}}`, false},
	} {
		_, err := parseDrivePlaybackURL([]byte(tc.raw), "12", "pick1234")
		if (err == nil) != tc.ok {
			t.Fatalf("错误接受直链 %v", err)
		}
	}
}
func TestCacheDirectoryRejectsWrongScopeAndIncompleteData(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{`{"state":true,"cid":"9","count":0,"data":[]}`, true},
		{`{"state":true,"cid":"0","count":0,"data":[]}`, false},
		{`{"state":true,"count":0,"data":[]}`, false},
		{`{"state":true,"cid":"9","count":1,"data":[]}`, false},
		{`{"state":true,"cid":"9","count":1,"data":[{"cid":"7"}]}`, false},
		{`{"state":true,"cid":"9","count":1,"data":[{"fid":"1","cid":"9","s":"100","sha":"` + strings.Repeat("A", 40) + `","pc":"pick1234","n":"v"}]}`, true},
	} {
		c := NewShareClient()
		c.RequestInterval = 0
		c.HTTP.Transport = metadataTransport(func(*http.Request) (*http.Response, error) { return instantResponse(200, []byte(tc.raw)), nil })
		_, err := c.CacheDirectoryFiles(context.Background(), "9", "UID=22_A")
		if (err == nil) != tc.ok {
			t.Fatalf("目录校验错误: %v", err)
		}
	}
}
