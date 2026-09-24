package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type subtitleAPIDrive struct {
	files   []p115.ShareFile
	uploads int
}

func (d *subtitleAPIDrive) DriveFiles(context.Context, string, int, string) (*p115.SharePage, error) {
	return &p115.SharePage{Files: append([]p115.ShareFile{}, d.files...), Count: len(d.files)}, nil
}
func (d *subtitleAPIDrive) CreateDirectory(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (d *subtitleAPIDrive) RenameEntries(context.Context, []p115.RenameTarget, string) error {
	return nil
}
func (d *subtitleAPIDrive) MoveEntriesBatch(context.Context, []string, string, string) error {
	return nil
}
func (d *subtitleAPIDrive) UploadSubtitle(_ context.Context, parent, name string, data []byte, cookie string) error {
	d.uploads++
	d.files = append(d.files, p115.ShareFile{ID: "200", Name: name, Size: int64(len(data))})
	return nil
}

func TestManualOrganizeSubtitleMultipartAboveJSONLimit(t *testing.T) {
	router, accounts, id := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "organize.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	video := p115.ShareFile{ID: "101", Name: "Movie.mkv", Size: 100}
	job := service.OrganizeJob{ID: "job", Config: service.OrganizeConfig{AccountID: id}, Status: "completed", Items: []service.OrganizeItem{{File: service.OrganizeFile{ShareFile: video, MediaType: "video"}, TargetCID: "10", TargetPath: "/movies/Movie.mkv", Name: video.Name, Status: "done"}}}
	doc, _ := json.Marshal(map[string]any{"version": 1, "jobs": []service.OrganizeJob{job}})
	if err := store.Save(string(doc)); err != nil {
		t.Fatal(err)
	}
	drive := &subtitleAPIDrive{files: []p115.ShareFile{video}}
	s, err := service.NewManualOrganize(store, accounts, drive, nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterManualOrganize(router.Group("/api/admin"), s)
	data := bytes.Repeat([]byte("subtitle\n"), 70000)
	digest := sha256.Sum256(data)
	body, _ := json.Marshal(service.SubtitlePreviewInput{AccountID: id, JobID: "job", Files: []service.SubtitleSelection{{LocalID: "1", OriginalName: "a.srt", VideoID: "101", Language: "zh-CN", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}}})
	response := accountRequest(t, router, "POST", "/manual-organize/subtitles/preview", string(body), 200)
	var plan service.SubtitleImport
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	for _, duplicate := range []bool{true, false} {
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		if err := writer.WriteField("accountId", id); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("previewId", plan.ID); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 1+map[bool]int{true: 1}[duplicate]; i++ {
			part, err := writer.CreateFormFile("1", "a.srt")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "/api/admin/p115/manual-organize/subtitles/import", &payload)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if duplicate {
			if recorder.Code != 400 || drive.uploads != 0 {
				t.Fatal("duplicate multipart accepted")
			}
			continue
		}
		if recorder.Code != 200 || drive.uploads != 1 {
			t.Fatalf("status=%d uploads=%d body=%s", recorder.Code, drive.uploads, recorder.Body.String())
		}
		var result service.SubtitleImport
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != "completed" || result.Items[0].Name != "Movie.zh-CN.srt" || time.Since(result.CreatedAt) > time.Minute {
			t.Fatalf("result=%+v", result)
		}
	}
}
