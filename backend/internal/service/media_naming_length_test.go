package service

import (
	"context"
	"testing"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/naming"
)

type longOriginalTitleMetadata struct{}

func (longOriginalTitleMetadata) Details(ctx context.Context, kind string, id int) (*tmdb.Detail, error) {
	return &tmdb.Detail{Item: tmdb.Item{
		ID: id, Kind: kind, Date: "2026-10-07",
		Title:         "被放逐的作弊赋予魔术师享受随心所欲的第二人生。",
		OriginalTitle: "追放されたチート付与魔術師は気ままなセカンドライフを謳歌する。 ～俺は武器だけじゃなく、あらゆるものに『強化ポイント』を付与できるし、俺の意思でいつでも効果を解除できるけど、残った人たち大丈夫？～",
	}}, ctx.Err()
}

func TestMediaNamingLongOriginalTitleFallsBack(t *testing.T) {
	template, err := naming.Default(naming.TV)
	if err != nil {
		t.Fatal(err)
	}
	template.Filename[0].Field = naming.OriginalTitle
	templates := &mediaNamingTemplates{record: naming.Snapshot{Revision: 1, Template: template}}
	s := MediaNaming{Templates: templates, Metadata: longOriginalTitleMetadata{}}
	input := MediaNamingInput{Kind: "tv", TMDBID: 313346, Files: []MediaNamingFile{{
		OrganizeSelection: OrganizeSelection{ID: "video", Season: 1, Episode: 6},
		Name:              "被放逐的作弊赋予魔术师享受随心所欲的第二人生。 (2026) - S01E06 - 1080p.Crunchyroll.WEB-DL.H264.AAC 2.0.strm",
	}}}
	if size := len(input.Files[0].Name); size != 130 {
		t.Fatalf("原文件名字节数 = %d，预期 130", size)
	}
	preview, err := s.Preview(context.Background(), input)
	if err != nil || len(preview.Items) != 1 || preview.Items[0].Result == nil {
		t.Fatalf("命名预览失败：%+v, %v", preview, err)
	}
	result := preview.Items[0].Result
	if preview.Items[0].Error != "" || len(result.Name) != 107 || result.Name != "被放逐的作弊赋予魔术师享受随心所欲的第二人生。.S01E06.1080p.WEB-DL.H264.AAC.2.0.strm" {
		t.Fatalf("超长原语种名称应自动回退为作品名称：%+v", preview.Items[0])
	}
	if templates.record.Template.Filename[0].Field != naming.OriginalTitle {
		t.Fatal("回退不得修改已保存模板")
	}

	// 正常长度仍使用原语种名称。
	s.Metadata = &mediaNamingMetadata{}
	preview, err = s.Preview(context.Background(), input)
	if err != nil || len(preview.Items) != 1 || preview.Items[0].Result == nil || preview.Items[0].Result.Name != "Original.S01E06.1080p.WEB-DL.H264.AAC.2.0.strm" {
		t.Fatalf("正常长度不应触发降级：%+v, %v", preview, err)
	}
}
