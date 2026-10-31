package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tmdb"
)

type classificationMetadata struct {
	*mediaRecognitionFake
	details map[int]tmdb.Detail
	calls   int
}

func (m *classificationMetadata) Details(_ context.Context, kind string, id int) (*tmdb.Detail, error) {
	m.calls++
	detail, ok := m.details[id]
	if !ok || detail.Kind != kind {
		return nil, errors.New("unexpected metadata request")
	}
	return &detail, nil
}

func TestMediaOrganizerClassificationLocalFiles(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	settings, _, initial := classificationTestStore(t)
	const source = `movie:
  fallback: 其他
  rules:
    - name: 动画
      match:
        genres: [16]
    - name: 华语
      match:
        languages: [zh, cn]
    - name: 日本制作
      match:
        countries: [JP]
tv:
  fallback: 其他
  rules:
    - name: 日本动画
      match:
        genres: [16]
        countries: [JP]
    - name: 其他动画
      match:
        genres: [16]
    - name: 国产
      match:
        countries: [CN]
`
	if _, err := settings.Save(context.Background(), source, initial.Revision); err != nil {
		t.Fatal(err)
	}
	s.classification = settings
	metadata := &classificationMetadata{mediaRecognitionFake: &mediaRecognitionFake{}, details: make(map[int]tmdb.Detail)}
	s.metadata = metadata
	cases := []struct {
		name, kind, language string
		genres               []tmdb.Genre
		origin, production   []string
		category             string
	}{
		{"Animation", "movie", "zh", []tmdb.Genre{{ID: 16}}, nil, nil, "电影/动画"},
		{"Chinese", "movie", "cn", nil, nil, nil, "电影/华语"},
		{"Production", "movie", "en", nil, []string{"US"}, []string{"JP"}, "电影/日本制作"},
		{"OriginOnly", "movie", "ja", nil, []string{"JP"}, nil, "电影/其他"},
		{"OriginConflict", "movie", "ja", nil, []string{"JP"}, []string{"US"}, "电影/其他"},
		{"Japanese", "tv", "ja", []tmdb.Genre{{ID: 16}}, []string{"JP"}, []string{"US"}, "剧集/日本动画"},
		{"UnknownOrigin", "tv", "ja", []tmdb.Genre{{ID: 16}}, nil, []string{"JP"}, "剧集/其他动画"},
		{"Coproduction", "tv", "en", nil, []string{"JP", "CN"}, nil, "剧集/国产"},
		{"ProductionConflict", "tv", "en", nil, []string{"US"}, []string{"CN"}, "剧集/其他"},
		{"Fallback", "tv", "", nil, nil, nil, "剧集/其他"},
	}
	for i, tc := range cases {
		item := tmdb.Item{ID: i + 1, Kind: tc.kind, Title: tc.name, OriginalTitle: tc.name, Date: "2024-01-01"}
		metadata.items = append(metadata.items, item)
		metadata.details[item.ID] = tmdb.Detail{Item: item, Genres: tc.genres, OriginCountryCodes: tc.origin, CountryCodes: tc.production, OriginalLanguage: tc.language}
		stem := tc.name + ".2024"
		if tc.kind == "tv" {
			stem += ".S01E01"
		}
		for _, suffix := range []string{".strm", ".ass", "-poster.jpg"} {
			mediaTestFile(t, rule.Source, stem+suffix, "original "+stem+suffix)
		}
	}
	rule.Classify = true
	var err error
	if rule, err = s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "completed" || len(run.Entries) != len(cases)*3 || metadata.calls != len(cases)*2 {
		t.Fatal("classification lost files or added metadata requests", run.Status, len(run.Entries), metadata.calls)
	}
	for _, e := range run.Entries {
		var category string
		for _, group := range run.Groups {
			if group.ID == e.Group {
				category = cases[group.TMDBID-1].category
				if category != group.Category {
					t.Fatal("incorrect TMDB facts/category mapping", group)
				}
			}
		}
		body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(e.Relative)))
		if category == "" || !strings.HasPrefix(e.Relative, category+"/") || e.Status != "done" || err != nil || string(body) != "original "+e.File.Name {
			t.Fatal("media and companions must share the category and retain content", e, err)
		}
		if _, err := os.Stat(filepath.Join(rule.Source, e.File.ID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("organized source remains", e, err)
		}
	}
}

func TestMediaOrganizerClassificationPinnedForCorrectionAndRestart(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	settings, _, initial := classificationTestStore(t)
	s.classification = settings
	rule.Classify, rule.Failure = true, t.TempDir()
	var err error
	if rule, err = s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Work.S01E01.strm", "Work.S01E01.ass", "Work.S01E02.strm"} {
		mediaTestFile(t, rule.Source, name, "original "+name)
	}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(strings.ReplaceAll(initial.YAML, "其他剧集", "新其他"), "name: 华语电影", "name: 新华语")
	if _, err := settings.Save(context.Background(), changed, initial.Revision); err != nil {
		t.Fatal(err)
	}
	// Change global rules before the queued batch has even begun recognition.
	mediaTestProcess(t, s, &run, "scan")
	for _, e := range run.Entries {
		if !strings.HasPrefix(e.Relative, "剧集/其他剧集/") {
			t.Fatal("queued run used subsequently changed rules", e)
		}
		if e.File.ID == "Work.S01E01.strm" {
			mediaTestFile(t, rule.Target, e.Relative, "existing file")
		}
	}
	mediaTestProcess(t, s, &run, "execute")
	other, err := s.Task(run.ID, "Work.S01E02.strm")
	if err != nil || other.Status != "done" {
		t.Fatal(other, err)
	}
	if _, err := s.Retry(run.ID, MediaOrganizeCorrection{FileID: "Work.S01E01.strm", Kind: "movie", TMDBID: 43}); err != nil {
		t.Fatal(err)
	}
	// The serialized batch restores without a global classification service.
	restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := restarted.Snapshot(run.ID)
	if snapshot.Status != "interrupted" || snapshot.Classification.YAML != initial.YAML || snapshot.Classification.Revision != initial.Revision || snapshot.Classification.Classifier != nil {
		t.Fatal("restart did not retain only the original serialized rules", snapshot)
	}
	if _, err := restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: "Work.S01E01.strm"}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = restarted.Snapshot(run.ID)
	mediaTestProcess(t, restarted, &snapshot, "retry")
	task, err := restarted.Task(run.ID, "Work.S01E01.strm")
	if err != nil || task.Status != "done" || task.Group.Category != "电影/华语电影" || len(task.Entries) != 2 {
		t.Fatal("correction must classify the new kind with the old rules", task, err)
	}
	for _, e := range task.Entries {
		body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(e.Relative)))
		if !strings.HasPrefix(e.Relative, "电影/华语电影/") || err != nil || string(body) != "original "+e.File.Name {
			t.Fatal("corrected companions used new global rules or lost content", e, err)
		}
	}
	unchanged, _ := restarted.Task(run.ID, other.FileID)
	want, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(unchanged)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(got) {
		t.Fatal("single task correction changed another task", other, unchanged)
	}
	// A new batch uses the new active configuration.
	restarted.classification = settings
	mediaTestFile(t, rule.Source, "Work.S01E03.strm", "new episode")
	next, err := restarted.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, restarted, &next, "auto")
	if next.Classification.Revision == initial.Revision || next.Status != "completed" || !strings.HasPrefix(next.Entries[0].Relative, "剧集/新其他/") {
		t.Fatal("new batch failed to use updated rules", next)
	}
}

func TestMediaOrganizerClassificationUnavailableDoesNotStopOtherMonitoring(t *testing.T) {
	s, _, _, rule := newMediaOrganizeTest(t)
	_, configPath, initial := classificationTestStore(t)
	if err := os.WriteFile(configPath, []byte("broken: yaml"), 0600); err != nil {
		t.Fatal(err)
	}
	unavailable, err := OpenMediaClassification(context.Background(), configPath)
	if err == nil {
		t.Fatal("expected a broken startup configuration")
	}
	s.classification = unavailable
	rule.Classify, rule.Enabled = true, true
	if rule, err = s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "keep")
	mediaTestFile(t, rule.Source, "ads.txt", "keep until classification works")
	if _, err := s.Start(rule.ID); !errors.Is(err, ErrClassificationUnavailable) || len(s.doc.Runs) != 0 || !s.doc.Rules[0].LastScan.IsZero() {
		t.Fatal("unavailable configuration must not create a batch", err, s.doc)
	}
	normal := rule
	normal.ID, normal.Name, normal.Classify = "", "without classification", false
	normal.Source, normal.Target = t.TempDir(), t.TempDir()
	if normal, err = s.SaveRule(normal); err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, normal.Source, "Work.S01E01.strm", "normal")
	if err := s.schedule(); err != nil || len(s.doc.Runs) != 1 || s.doc.Runs[0].Rule.ID != normal.ID {
		t.Fatal("bad classification stopped other scheduled rules", err, s.doc)
	}
	run := s.doc.Runs[0]
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "completed" || run.Classification.YAML != "" {
		t.Fatal("unclassified monitoring was affected", run)
	}
	for _, name := range []string{"Work.S01E01.strm", "ads.txt"} {
		if _, err := os.Stat(filepath.Join(rule.Source, name)); err != nil {
			t.Fatal("blocked classification touched its source", err)
		}
	}
	document := classificationTestRead(t, unavailable)
	if _, err := unavailable.Save(context.Background(), initial.YAML, document.Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.schedule(); err != nil || len(s.doc.Runs) != 2 || s.doc.Runs[0].Rule.ID != rule.ID {
		t.Fatal("monitoring did not recover after configuration repair", err, s.doc)
	}
}

func TestMediaOrganizerClassificationRejectsCorruptBatchBeforeWrites(t *testing.T) {
	s, _, store, rule := newMediaOrganizeTest(t)
	settings, _, _ := classificationTestStore(t)
	s.classification = settings
	rule.Classify = true
	var err error
	if rule, err = s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	mediaTestFile(t, rule.Source, "Work.S01E01.strm", "original")
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "scan")
	valid := store.Value()
	for _, corrupt := range []ClassificationSnapshot{
		{}, {YAML: run.Classification.YAML, Revision: "changed"},
		{YAML: "broken: yaml", Revision: classificationRevision("broken: yaml")},
	} {
		var document mediaOrganizeDocument
		if err := json.Unmarshal([]byte(valid), &document); err != nil {
			t.Fatal(err)
		}
		document.Runs[0].Classification = corrupt
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(string(raw)); err != nil {
			t.Fatal(err)
		}
		restarted, err := NewMediaOrganizer(store, s.metadata, s.templates, settings, nil, nil, nil)
		if err != nil {
			t.Fatal("a corrupt historical snapshot must not prevent reading settings", err)
		}
		before, _ := restarted.Snapshot(run.ID)
		if _, err := restarted.Retry(run.ID, MediaOrganizeCorrection{FileID: "Work.S01E01.strm"}); err == nil || len(restarted.actions) != 0 {
			t.Fatal("corrupt snapshot borrowed current global rules", err)
		}
		after, _ := restarted.Snapshot(run.ID)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("rejected retry modified its task")
		}
		if err := restarted.process(context.Background(), &after, "execute"); err == nil {
			t.Fatal("corrupt snapshot executed a saved target")
		}
	}
	if _, err := os.Stat(filepath.Join(rule.Source, "Work.S01E01.strm")); err != nil {
		t.Fatal("corrupt batch moved its source", err)
	}
}

func TestMediaOrganizerClassificationCloudDestination(t *testing.T) {
	s, drive, rule := newMediaCleanupCloud(t)
	settings, _, _ := classificationTestStore(t)
	s.classification = settings
	rule.Classify = true
	var err error
	if rule, err = s.SaveRule(rule); err != nil {
		t.Fatal(err)
	}
	drive.dirs["10"] = []p115.ShareFile{{ID: "30", Name: "Work.S01E01.strm", Size: 10}}
	s.metadata = &mediaRecognitionFake{items: []tmdb.Item{{ID: 42, Kind: "tv", Title: "Work", Date: "2024-01-01"}}}
	run, err := s.Start(rule.ID)
	if err != nil {
		t.Fatal(err)
	}
	mediaTestProcess(t, s, &run, "auto")
	if run.Status != "completed" || len(run.Entries) != 1 || !strings.HasPrefix(run.Entries[0].Relative, "剧集/其他剧集/") || run.Entries[0].Target != path.Join(rule.Target, run.Entries[0].Relative) {
		t.Fatal("115 classification destination mismatch", run)
	}
	if drive.dirs["20"][0].Name != "剧集" || drive.creates != 4 || drive.moves != 1 {
		t.Fatal("115 category directory was not created or moved once", drive.dirs, drive.moves)
	}
}
