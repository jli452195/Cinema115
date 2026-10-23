package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/service"
)

type subtitleSourceAPIDrive struct {
	*subtitleAPIDrive
	reads int
}

func (d *subtitleSourceAPIDrive) ReadSubtitleSource(ctx context.Context, parent string, file p115.ShareFile, cookie string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if file.ID != "600" || parent != "10" || cookie == "" {
		return nil, fmt.Errorf("incorrect source identity")
	}
	d.reads++
	return []byte("archive"), nil
}

func TestManualOrganizeSubtitleSourceBinaryAndRelease(t *testing.T) {
	router, accounts, id := setupAccountsAPI(t, nil, nil)
	store, err := service.NewSessionStore(filepath.Join(t.TempDir(), "organize.enc"), "secret")
	if err != nil {
		t.Fatal(err)
	}
	video := p115.ShareFile{ID: "101", Name: "Movie.mkv", Size: 100}
	archive := p115.ShareFile{ID: "600", Name: "subtitles.zip", Size: 7}
	job := service.OrganizeJob{ID: "job", Config: service.OrganizeConfig{AccountID: id}, Status: "completed", Items: []service.OrganizeItem{{File: service.OrganizeFile{ShareFile: video, MediaType: "video"}, TargetCID: "10", TargetPath: "/movies/Movie.mkv", Status: "done"}}}
	doc, err := json.Marshal(map[string]any{"version": 1, "jobs": []service.OrganizeJob{job}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(string(doc)); err != nil {
		t.Fatal(err)
	}
	drive := &subtitleSourceAPIDrive{subtitleAPIDrive: &subtitleAPIDrive{files: []p115.ShareFile{video, archive}}}
	organizer, err := newHTTPManualOrganize(store, accounts, drive, nil)
	if err != nil {
		t.Fatal(err)
	}
	RegisterManualOrganize(router.Group("/api/admin"), organizer)
	body, err := json.Marshal(service.SubtitleSourcesInput{AccountID: id, JobID: "job", Files: []service.SubtitleSourceFile{{ShareFile: archive, ParentID: "10"}}})
	if err != nil {
		t.Fatal(err)
	}
	response := accountRequest(t, router, "POST", "/manual-organize/subtitles/sources", string(body), 200)
	var plan service.SubtitleSources
	if err := json.Unmarshal(response.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	endpoint := "/manual-organize/subtitles/sources/" + plan.ID
	for range 3 {
		response = accountRequest(t, router, "GET", endpoint+"/files/600?accountId="+id, "", 200)
		if response.Body.String() != "archive" || response.Header().Get("Content-Type") != "application/octet-stream" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("binary response or privacy headers invalid")
		}
	}
	accountRequest(t, router, "GET", endpoint+"/files/601?accountId="+id, "", 409)
	accountRequest(t, router, "DELETE", endpoint+"?accountId="+id, "", 204)
	accountRequest(t, router, "GET", endpoint+"/files/600?accountId="+id, "", 409)
	if drive.reads != 3 || drive.uploads != 0 {
		t.Fatal("source read caused unexpected download or write")
	}
}

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
	s, err := newHTTPManualOrganize(store, accounts, drive, nil)
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
