package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/service"
)

func TestMediaOrganizeTaskResponsePreservesPresentationWithoutExecutionContext(t *testing.T) {
	at := time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC)
	season := 2
	task := service.MediaOrganizeTask{
		MediaOrganizeHistoryItem: service.MediaOrganizeHistoryItem{
			RunID: "run", FileID: "video", Name: "original.mkv", Title: "作品", Kind: "tv", MediaType: "video",
			Season: 2, Episode: 3, Source: "/source/original.mkv", AttachmentCount: 1, CanDelete: true, CanRetry: true,
			MediaOrganizeResult: service.MediaOrganizeResult{Status: "failed", Message: "目标冲突", At: at, Target: "/target/renamed.mkv", Action: "private-action"},
		},
		Rule: service.MediaOrganizeRule{ID: "rule", Name: "规则", Storage: "115", Source: "/source", Target: "/target", Failure: "/failure", AccountID: "account", SourceID: "source-id"},
		Group: service.MediaOrganizeGroup{
			ID: "group", Identity: mediafacts.Identity{Name: "搜索片名", AlternativeName: "内部备选", Season: 2, SeasonExplicit: true},
			Kind: "tv", TMDBID: 42, Title: "作品", Year: "2024", Category: "剧集/其他剧集", Message: "匹配提示", MatchedSeason: &season, ResultCode: "private-code",
		},
		Entries: []service.MediaOrganizeEntry{
			{
				File: service.OrganizeFile{
					ShareFile: p115.ShareFile{ID: "video", Name: "original.mkv", Size: 123, SHA1: "private-sha1"},
					ParentID:  "parent", SourcePath: "original.mkv", MediaType: "video", Season: 2, Episode: 3, EndEpisode: 4,
					NamingValues: map[naming.Field]string{naming.Resolution: "1080p"},
				},
				Modified: 123, CD2FileID: "cd2-id", CD2Hashes: map[uint32]string{1: "private-hash"},
				CD2Current: &service.MediaCD2Location{Path: "/current", FileID: "current-id"}, Reference: true, CD2Duplicate: true,
				Group: "group", Relative: "relative", Target: "/target/renamed.mkv", Status: "error", Message: "写入未确认", ResultCode: "WRITE_UNCONFIRMED", UpdatedAt: at,
				Result:   &service.MediaOrganizeResult{Status: "failed", At: at},
				Failure:  &service.MediaOrganizeFailure{State: "moving", Message: "失败目录结果未确认", ParentID: "private-parent", FileID: "private-file", Reason: "private-reason"},
				P115Move: &service.Media115MoveIntent{Phase: "moving", AccountID: "private-account", SourceCID: "private-source", TargetCID: "private-target", SHA1: "private-sha1"},
			},
			{
				File:    service.OrganizeFile{ShareFile: p115.ShareFile{ID: "subtitle", Name: "original.srt"}, SourcePath: "original.srt", MediaType: "subtitle", Season: 2, Episode: 3},
				VideoID: "video", Status: "waiting", ResultCode: "WAITING_MAIN_FILE",
			},
		},
	}
	got, err := json.Marshal(mediaOrganizeTaskOutput(task))
	if err != nil {
		t.Fatal(err)
	}
	want := `{
		"runId":"run","fileId":"video","name":"original.mkv","title":"作品","kind":"tv","mediaType":"video",
		"season":2,"episode":3,"source":"/source/original.mkv","attachmentCount":1,"canDelete":true,"canRetry":true,
		"status":"failed","message":"目标冲突","at":"2026-11-05T08:30:00Z","target":"/target/renamed.mkv",
		"rule":{"name":"规则","source":"/source","failure":"/failure"},
		"group":{"id":"group","identity":{"name":"搜索片名"},"kind":"tv","tmdbId":42,"title":"作品","year":"2024","category":"剧集/其他剧集","message":"匹配提示"},
		"entries":[
			{"file":{"id":"video","name":"original.mkv","sourcePath":"original.mkv","mediaType":"video","season":2,"episode":3,"endEpisode":4,"namingValues":{"resolution":"1080p"}},
			 "target":"/target/renamed.mkv","status":"error","message":"写入未确认","resultCode":"WRITE_UNCONFIRMED","failure":{"state":"moving","message":"失败目录结果未确认"},"p115Move":{"phase":"moving"}},
			{"file":{"id":"subtitle","name":"original.srt","sourcePath":"original.srt","mediaType":"subtitle","season":2,"episode":3},"videoId":"video","status":"waiting","resultCode":"WAITING_MAIN_FILE"}
		]
	}`
	var actual, expected any
	if err := json.Unmarshal(got, &actual); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("任务详情必须保留展示、季集修正和附件依赖信息，并排除执行内部字段：\n%s", got)
	}
}

func TestMediaOrganizeTaskResponseDistinguishesExplicitZeroEpisode(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		task := service.MediaOrganizeTask{
			MediaOrganizeHistoryItem: service.MediaOrganizeHistoryItem{Season: 2, Episode: 0, EpisodeExplicit: explicit},
			Entries:                  []service.MediaOrganizeEntry{{File: service.OrganizeFile{Season: 2, Episode: 0, EpisodeExplicit: explicit}}},
		}
		body, err := json.Marshal(mediaOrganizeTaskOutput(task))
		if err != nil {
			t.Fatal(err)
		}
		var response map[string]any
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		file := response["entries"].([]any)[0].(map[string]any)["file"].(map[string]any)
		for _, item := range []map[string]any{response, file} {
			value, exists := item["episodeExplicit"]
			if exists != explicit || explicit && value != true || item["episode"] != float64(0) {
				t.Fatal("明确零集需要展示标记，缺失集号不能冒充 E00", string(body))
			}
		}
	}
}
