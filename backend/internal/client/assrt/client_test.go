package assrt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

func TestAssrtSearchMixedUpstreamRecords(t *testing.T) {
	c := New(strings.Repeat("a", 32))
	// The live API sometimes mixes formatted and raw records in one response.
	// Raw subtype is a numeric code; m_subtype contains the display name.
	c.HTTP.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return reply(`{"status":0,"sub":{"subs":[
			{"id":231537,"native_name":"浪客剑心 新京都篇 后篇","videoname":"Shin Kyoto Hen 02","lang":{"desc":"繁"},"subtype":"Subrip(srt)"},
			{"fileid":"209143","m_title_bot":"浪客剑心 新京都篇 前篇","videoname":"Shin Kyoto Hen 01","subtype":"3","m_subtype":"SSA","m_lang":"英&nbsp;简","uploadtime":"2012-04-29 01:50:24","m_rl_name":"诸神字幕组"}
		]}}`), nil
	})
	items, err := c.Search(context.Background(), "浪客剑心", 0)
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	if items[0].ID != 231537 || items[0].Format != "Subrip(srt)" || items[0].Language != "繁" {
		t.Fatal("documented record changed", items[0])
	}
	want := Item{ID: 209143, Title: "浪客剑心 新京都篇 前篇", VideoName: "Shin Kyoto Hen 01", Language: "英 简", Group: "诸神字幕组", Format: "SSA", UploadedAt: "2012-04-29 01:50:24"}
	if items[1] != want {
		t.Fatal("raw record not normalized", items[1])
	}
	for _, id := range []string{"", "0", "-1", "invalid", "9223372036854775808"} {
		c.HTTP.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
			return reply(fmt.Sprintf(`{"status":0,"sub":{"subs":[{"fileid":%q,"m_title_bot":"无效记录"}]}}`, id)), nil
		})
		if _, err := c.Search(context.Background(), "浪客剑心", 0); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("invalid fileid %q accepted: %v", id, err)
		}
	}
}

func TestAssrtSearchAPIStatusAndDownloadBoundary(t *testing.T) {
	c := New(strings.Repeat("a", 32))
	calls := 0
	response := `{"status":0,"sub":{"subs":[{"id":123,"native_name":"示例字幕","videoname":"Show.BD","lang":{"desc":"简体"},"release_site":"Group"}]}}`
	downloadURLValue := "https://file0.assrt.net/download/123/sub.zip?signature=private"
	c.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "api.assrt.net" {
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 32) || r.URL.Query().Has("token") {
				t.Fatal("unsafe authentication", r.URL)
			}
			switch r.URL.Path {
			case "/v1/sub/search":
				if r.URL.Query().Get("q") != "Show" || r.URL.Query().Get("pos") != "15" {
					t.Fatal("wrong pagination", r.URL)
				}
				return reply(response), nil
			case "/v1/sub/detail":
				return reply(fmt.Sprintf(`{"status":0,"sub":{"subs":[{"id":123,"filename":"字幕.zip","size":3,"url":%q}]}}`, downloadURLValue)), nil
			}
		}
		if r.URL.Host != "file0.assrt.net" || r.Header.Get("Authorization") != "" {
			t.Fatal("token leak or unsafe download", r.URL.Host)
		}
		return reply("zip"), nil
	})
	items, err := c.Search(context.Background(), "Show", 15)
	if err != nil || len(items) != 1 || items[0].Language != "简体" || items[0].Group != "Group" {
		t.Fatal(items, err)
	}
	for _, tc := range []struct {
		body string
		want error
	}{
		{`{"status":20001}`, ErrToken}, {`{"status":30900}`, ErrRateLimited}, {`{"status":30000}`, ErrUnavailable}, {`{}`, ErrUnavailable}, {`{"status":0,"sub":{"subs":[{"id":0}]}}`, ErrUnavailable},
	} {
		response = tc.body
		if _, err := c.Search(context.Background(), "Show", 15); !errors.Is(err, tc.want) {
			t.Fatal(tc.body, err)
		}
	}
	before := calls
	if _, err := c.Search(context.Background(), "短", 0); !errors.Is(err, ErrInput) || calls != before {
		t.Fatal("invalid query reached upstream", err)
	}
	file, err := c.Download(context.Background(), 123)
	if err != nil || file.Name != "字幕.zip" || string(file.Data) != "zip" {
		t.Fatal(file, err)
	}
	for _, unsafe := range []string{"http://127.0.0.1/admin", "https://file0.assrt.net.evil.test/x", "https://u:p@file0.assrt.net/x", "https://file0.assrt.net:8080/x"} {
		downloadURLValue = unsafe
		before = calls
		if _, err := c.Download(context.Background(), 123); err == nil || calls != before+1 {
			t.Fatal("unsafe target fetched", unsafe, err)
		}
	}
}

func TestAssrtDownloadRedirectDoesNotForwardCredentials(t *testing.T) {
	c := New(strings.Repeat("a", 32))
	calls := 0
	c.HTTP.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host == "api.assrt.net" {
			return reply(`{"status":0,"sub":{"subs":[{"id":123,"filename":"a.srt","size":3,"url":"https://file0.assrt.net/download/123/x"}]}}`), nil
		}
		resp := reply("")
		resp.StatusCode = 302
		resp.Header.Set("Location", "http://127.0.0.1/private")
		return resp, nil
	})
	if _, err := c.Download(context.Background(), 123); err == nil || calls != 2 {
		t.Fatal("followed outside redirect", calls, err)
	}
}
