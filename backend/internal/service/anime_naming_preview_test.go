package service

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/animeorganize"
	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
)

func TestAnimeNamingPreviewUsesSharedTemplateAndMatchesRealFiles(t *testing.T) {
	template, _ := naming.Default(naming.TV)
	template.Directories = nil
	template.Filename[0].Field = naming.OriginalTitle
	store := &mediaNamingTemplates{record: naming.Snapshot{Revision: 7, Template: template}}
	target := model.AnimeNamingTarget{TMDBID: "317259", Title: "至高之力", OriginalTitle: "VERTEX FORCE", Year: "2026", Season: 1, Fingerprint: strings.Repeat("a", 64), Seasons: []model.AnimeNamingSeason{{Number: 1}}}
	mapping := model.AnimeSourceMapping{SourceRef: "main", Segments: []model.AnimeMappingSegment{{First: 0, SourceBase: 0, TargetSeason: 1, TargetBase: 0}}}
	titles := []string{
		"[Nix-Raws] VERTEX FORCE S01E01 [CR WEB-DL 1080p AVC AAC][SC_TC]",
		"[字幕组] 动画 - 00 [1080p][简体] GB",
		"[字幕组] 动画 - 13 [720p][简日双语内嵌]",
		"[字幕组] 动画 [01-12]",
	}
	in := AnimeNamingPreviewInput{Target: target, Mapping: mapping, Group: "Nix-Raws"}
	for i, title := range titles {
		parsed, err := animeparse.Title(title)
		if err != nil {
			t.Fatal(err)
		}
		in.Items = append(in.Items, AnimeNamingPreviewFile{ID: []string{"first", "zero", "beyond", "batch"}[i], Title: title, Parse: parsed})
	}
	preview, err := PreviewAnimeNaming(context.Background(), store, in)
	if err != nil || store.calls != 1 || preview.Naming.Revision != 7 || len(preview.Items) != 4 {
		t.Fatalf("%+v %v, calls=%d", preview, err, store.calls)
	}
	if got := preview.Items[0].Result; got == nil || got.Name != "VERTEX FORCE.S01E01.1080p.WEB-DL.H264.AAC.[简繁]-Nix-Raws" || got.Extension != "" || got.Directory != "" {
		t.Fatal(got)
	}
	if got := preview.Items[1].Result; got == nil || !strings.Contains(got.Name, "S01E00.1080p.[简体]") {
		t.Fatal(got)
	}
	if got := preview.Items[2].Result; got == nil || !strings.Contains(got.Name, "S01E13.720p.[简日].内嵌") {
		t.Fatal(got)
	}
	if preview.Items[3].Status != "unmapped" || preview.Items[3].Result != nil {
		t.Fatal(preview.Items[3])
	}
	renderer, _ := naming.Compile(template)
	plan := model.AnimeNamingPlan{Target: target, Sources: []model.AnimeSourceMapping{mapping}}
	for i, file := range in.Items[:3] {
		projection := plan.Project("main", file.Parse)
		realName := file.Title + ".MKV"
		result, err := animeorganize.Render(renderer, target, projection, realName, false, mediafacts.Values(realName, model.AnimeSpec{}, in.Group))
		if err != nil || result.Stem != preview.Items[i].Result.Stem || result.Extension != ".mkv" {
			t.Fatalf("preview differs from file: %+v %v", result, err)
		}
	}
	// One oversized row must not hide the other rows.
	in.Items[0].Title = strings.Repeat("字", 100)
	template.Filename = append(template.Filename, naming.Block{Field: naming.OriginalName, Separator: "."})
	store.record.Template = template
	preview, err = PreviewAnimeNaming(context.Background(), store, in)
	if err != nil || preview.Items[0].Error == "" || preview.Items[0].Result != nil || preview.Items[1].Result == nil {
		t.Fatalf("%+v %v", preview, err)
	}
	// Preview must not mutate numbering or the caller's target metadata.
	if !reflect.DeepEqual(in.Target, target) || !reflect.DeepEqual(in.Mapping, mapping) {
		t.Fatal("preview mutated subscription metadata")
	}
}
