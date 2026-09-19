package gying

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSeedCountsMixedValuesPreservePositions(t *testing.T) {
	var got seedCounts
	err := json.Unmarshal([]byte(`[34,"34",0,"0",null,"",-1,"-1","未知",true,{},[],1.5,"1.5",1e2,"1e2","99999999999999999999999999999999999999999"," 42 ","007",false,"password=private-cookie"]`), &got)
	want := seedCounts{34, 34, 0, 0, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, 42, 7, -1, -1}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("混合做种数解析错误: %v %v", got, err)
	}
	if err = json.Unmarshal([]byte(`null`), &got); err != nil || got != nil {
		t.Fatal(got, err)
	}
	if err = json.Unmarshal([]byte(`[]`), &got); err != nil || got == nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	for _, raw := range []string{`"34"`, `{}`, `[34,`} {
		if err = json.Unmarshal([]byte(raw), &got); err == nil {
			t.Fatalf("非数组或损坏 JSON 不应静默接受: %s", raw)
		}
	}
}

func TestSeedCountsPreserveResourcesAndUnknownValues(t *testing.T) {
	var detail detailData
	body := `{"downlist":{"type":{"a":["1080P"],"b":["i7"]},"list":{"m":["0123456789abcdef0123456789abcdef01234567","1123456789abcdef0123456789abcdef01234567","2123456789abcdef0123456789abcdef01234567","3123456789abcdef0123456789abcdef01234567","4123456789abcdef0123456789abcdef01234567"],"e":["0","unknown",null,"34"],"t":["零做种","未知做种","空值","正常做种","缺失值"],"p":["i7","i7","i7","i7","i7"]}},"panlist":{"url":["https://115.com/s/test"],"type":[5]}}`
	if err := json.Unmarshal([]byte(body), &detail); err != nil {
		t.Fatal(err)
	}
	links := detailLinks(detail)
	if len(links) != 6 || links[0].Type != "115" {
		t.Fatalf("资源丢失: %+v", links)
	}
	if links[1].Seeds == nil || *links[1].Seeds != 0 || links[4].Seeds == nil || *links[4].Seeds != 34 {
		t.Fatal("正常做种数丢失或错位")
	}
	for _, index := range []int{2, 3, 5} {
		if links[index].Seeds != nil {
			t.Fatal("未知做种数错误显示为零", index)
		}
	}
	for _, link := range links[1:] {
		if link.SubTypeLabel != "1080P" {
			t.Fatal("磁力分类受到影响")
		}
	}
	encoded, err := json.Marshal(links)
	if err != nil || strings.Count(string(encoded), `"seeds"`) != 2 || !strings.Contains(string(encoded), `"seeds":0`) {
		t.Fatal(string(encoded), err)
	}
}

func TestSeedCountsSearchKeepsMagnetAndPanWithoutIMDBRetry(t *testing.T) {
	searches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search":
			searches++
			fmt.Fprint(w, `<script>_obj.search={"l":{"title":["鸭子的天空"],"d":["ac"],"i":["test"]}};</script>`)
		case "/res/downurl/ac/test":
			fmt.Fprint(w, `{"code":200,"panlist":{"url":["https://115.com/s/test"]},"downlist":{"list":{"m":["0123456789abcdef0123456789abcdef01234567","1123456789abcdef0123456789abcdef01234567"],"e":["34","Cookie: secret-test-value"]}}}`)
		default:
			t.Errorf("意外请求 %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	messages := make([]string, 0, 8)
	ctx := WithProgress(context.Background(), func(level, message string) { messages = append(messages, level+" "+message) })
	result, err := client.SearchByTitle(ctx, "あひるの空", "tt10937602")
	if err != nil || len(result.Items) != 1 || len(result.Items[0].Links) != 3 || result.Partial || searches != 1 {
		t.Fatal(result, err, searches)
	}
	logs := strings.Join(messages, "\n")
	if !strings.Contains(logs, "downlist.list.e 有 1 项") || strings.Contains(logs, "secret-test-value") {
		t.Fatal(logs)
	}
}

func TestDetailCriticalFieldsRemainStrict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"downlist":{"list":{"m":[42],"e":["34"]}}}`)
	}))
	defer server.Close()
	client, err := New(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.fetchDetail(context.Background(), "ac", "test")
	var field *json.UnmarshalTypeError
	if !errors.Is(err, ErrDetailFormat) || !errors.As(err, &field) {
		t.Fatal("关键字段错误未保留类型信息", err)
	}
}
