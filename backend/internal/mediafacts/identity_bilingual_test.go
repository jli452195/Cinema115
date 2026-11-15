package mediafacts

import (
	"encoding/json"
	"testing"
)

func TestParseIdentityBilingualCompleteTitles(t *testing.T) {
	for _, tc := range []struct {
		file, chinese, english, year string
		season                       int
	}{
		{"你好，星期六.Hello.Saturday.2022.S05E24.2160p.WEB-DL.SDR.HEVC.AAC2.0.strm", "你好，星期六", "Hello Saturday", "2022", 5},
		{"[你的名字我的姓氏].Once.Given.Never.Forgotten.2021.S01E40.WEB-DL.4K.EDR.HEVC.AAC-CMCTV.strm", "你的名字我的姓氏", "Once Given Never Forgotten", "2021", 1},
		{"【你的名字我的姓氏】Once_Given_Never_Forgotten.S01E40.strm", "你的名字我的姓氏", "Once Given Never Forgotten", "", 1},
		{"[LoliHouse]你的名字我的姓氏.Once.Given.Never.Forgotten.2021.S01E40.mkv", "你的名字我的姓氏", "Once Given Never Forgotten", "2021", 1},
		{"中国合伙人2.American.Dreams.in.China.2.(2013).mkv", "中国合伙人2", "American Dreams in China 2", "2013", 1},
	} {
		t.Run(tc.file, func(t *testing.T) {
			got := ParseIdentity(tc.file)
			if got.ChineseTitle != tc.chinese || got.EnglishTitle != tc.english || got.Name != tc.chinese+" "+tc.english || got.Year != tc.year || got.Season != tc.season {
				t.Fatalf("必须保留两个完整名称及原年份、季号，不能遗留右括号: %+v", got)
			}
			if identities := map[Identity]bool{got: true}; !identities[got] {
				t.Fatal("增加双语标题后身份必须仍能作为分组键")
			}
			body, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var restored Identity
			if err := json.Unmarshal(body, &restored); err != nil {
				t.Fatal(err)
			}
			if restored != identityWithoutSources(got) {
				t.Fatalf("双语标题必须随整理任务快照保存: restored=%+v got=%+v", restored, got)
			}
		})
	}
}

func TestParseIdentityBilingualDoesNotInventFragments(t *testing.T) {
	for _, file := range []string{
		"你好，星期六.2022.S05E24.strm",
		"Hello.Saturday.2022.S05E24.strm",
		"我的AI男友.2024.S01E01.strm",
		"阿Q正传.The.True.Story.(1981).mkv",
		"新世纪 EVA Final.S01E01.mkv",
		"中国式.AI.S01E01.mkv",
		"你好.Hello.S01E01.mkv",
		"你好.Didn't.S01E01.mkv",
		"你好，星期六.Hello.Saturday.特别篇.S00E01.mkv",
		"[你的名字我的姓氏].Once.Given/Never.Forgotten.2021.S01E40.strm",
		"[你的名字我的姓氏].Once.Given+Never.Forgotten.2021.S01E40.strm",
		"[你的名字我的姓氏].Once.Given.Never.Forgotten.2021.1080p.mkv",
	} {
		t.Run(file, func(t *testing.T) {
			got := ParseIdentity(file)
			if got.ChineseTitle != "" || got.EnglishTitle != "" {
				t.Fatalf("缺少完整双标题证据，不能增加片段别名: %+v", got)
			}
		})
	}
	got := ParseIdentity("你好.Hello.S01E01.mkv")
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"chineseTitle", "englishTitle"} {
		if _, ok := fields[field]; ok {
			t.Fatalf("未提取的双语标题不应膨胀普通任务快照: %s", body)
		}
	}
}

func TestResolveIdentityBilingualDirectoryInheritance(t *testing.T) {
	const directory = "[你的名字我的姓氏].Once.Given.Never.Forgotten.2021.S01.Complete"
	ctx, err := ParseDirectoryContext("/source", "/source/"+directory+"/Season 01/E40.strm")
	if err != nil {
		t.Fatal(err)
	}
	got := ResolveIdentity("E40.strm", ctx)
	if got.ChineseTitle != "你的名字我的姓氏" || got.EnglishTitle != "Once Given Never Forgotten" || got.Year != "2021" {
		t.Fatalf("仅集号文件应继承作品目录的完整双语标题: %+v", got)
	}
	got = ResolveIdentity("Other.Show.2024.S01E01.strm", ctx)
	if got.ChineseTitle != "" || got.EnglishTitle != "" || got.Name != "Other Show" {
		t.Fatalf("有独立片名的文件不能继承其他作品的双语标题: %+v", got)
	}
}

func BenchmarkParseIdentityBilingual(b *testing.B) {
	for _, file := range []string{
		"你好，星期六.Hello.Saturday.2022.S05E24.2160p.WEB-DL.SDR.HEVC.AAC2.0.strm",
		"[你的名字我的姓氏].Once.Given.Never.Forgotten.2021.S01E40.WEB-DL.4K.EDR.HEVC.AAC-CMCTV.strm",
	} {
		b.Run(file, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				got := ParseIdentity(file)
				if got.ChineseTitle == "" || got.EnglishTitle == "" {
					b.Fatal("双语片名未提取")
				}
			}
		})
	}
}
