package notification

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTemplateEscapesRuntimeValuesAndRetainsMarkup(t *testing.T) {
	html, err := Render(`<b>{title}</b><blockquote>{resource}</blockquote>`, map[string]string{
		"title": `<script>alert("x")</script>`, "resource": "A&B <1080p>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "A&amp;B &lt;1080p&gt;") || !strings.HasPrefix(html, "<b>") {
		t.Fatal(html)
	}
	if _, err := validateHTML(html); err != nil {
		t.Fatal(err)
	}
}

func TestTemplateUsesOnlyTelegramSupportedNamedEntities(t *testing.T) {
	html, err := Render(`<b>&apos;{title}&apos;</b> &quot; &amp; &lt; &gt;`, map[string]string{"title": "it's"})
	if err != nil || strings.Contains(html, "&apos;") || !strings.Contains(html, "&#39;") {
		t.Fatal(html, err)
	}
}

func TestTemplateRejectsUnsupportedMarkupAndVariables(t *testing.T) {
	for _, template := range []string{
		`<script>{title}</script>`, `<a href="javascript:alert(1)">{title}</a>`, `<b onclick="alert(1)">{title}</b>`,
		`<b>{title}</i>`, `<b{title}>test</b>`, `{unknown}`, `{title`, `<code><b>test</b></code>`,
		`<blockquote><blockquote>text</blockquote></blockquote>`, `<b/>text`, `<b></b>`, `<!--text-->`,
		`<b><code>text</code></b>`, `<i><pre>text</pre></i>`, `<b>&#32;</b>`,
		`</root><root>`, "abc\x00", strings.Repeat("😀", MaxTemplateLength/2+1),
	} {
		if err := Validate(template); !errors.Is(err, ErrTemplate) {
			t.Errorf("template %q accepted: %v", template, err)
		}
	}
	if _, err := Render(`<b>{title}</b>`, map[string]string{}); !errors.Is(err, ErrTemplate) {
		t.Fatal("空渲染结果被接受", err)
	}
}

func TestTemplateBoundsVisibleUnicodeWithoutBreakingTags(t *testing.T) {
	template := `<blockquote>` + strings.Repeat(`{title}`, 180) + `</blockquote>`
	html, err := Render(template, map[string]string{"title": strings.Repeat("😀<&", 4000)})
	if err != nil {
		t.Fatal(err)
	}
	length, err := validateHTML(html)
	if err != nil || length > MaxMessageLength || !strings.HasSuffix(html, "</blockquote>") || strings.ContainsRune(html, '\ufffd') {
		t.Fatalf("length=%d err=%v suffix=%t", length, err, strings.HasSuffix(html, "</blockquote>"))
	}
}

func TestDefaultTemplateEventsAndBeijingTime(t *testing.T) {
	for _, kind := range []string{"downloaded", "organized", "failed"} {
		event := Sample(kind)
		values := Values(event)
		html, err := Render("", values)
		if err != nil || !strings.Contains(html, "第 07 集") || !strings.Contains(html, "2026-11-05 20:30:00 +08:00") {
			t.Fatal(html, err)
		}
		if kind == "downloaded" && (values["event"] != "下载完成" || values["path"] != "/动漫下载") || kind == "organized" && (values["event"] != "整理完成" || values["path"] != "/媒体库/动漫") {
			t.Fatal(values)
		}
		if strings.Contains(html, "入库成功") {
			t.Fatal("整理结果不能声称 Emby 入库成功")
		}
	}
	event := Sample("organized")
	event.CreatedAt = time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC).UnixMilli()
	if Values(event)["time"] != "2026-11-05 20:30:00 +08:00" {
		t.Fatal(Values(event)["time"])
	}
}
