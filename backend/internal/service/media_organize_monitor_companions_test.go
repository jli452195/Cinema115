package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"strm115/backend/internal/client/tmdb"
)

type mediaCompanionArrivalRecognition struct {
	*mediaRecognitionFake
	arrive func()
	once   sync.Once
}

func (m *mediaCompanionArrivalRecognition) Browse(ctx context.Context, options tmdb.Options) (*tmdb.Page, error) {
	m.once.Do(m.arrive)
	return m.mediaRecognitionFake.Browse(ctx, options)
}

func TestMediaLocalMonitorCompanionsSettleTogether(t *testing.T) {
	for _, scenario := range []struct {
		name, incoming, completed string
		duringRecognition         bool
		preserveMtime             bool
	}{
		{"subtitle", "Work.S01E01.zh-CN.ass", "Work.S01E01.zh-CN.ass", false, false},
		{"metadata", "Work.S01E01.nfo", "Work.S01E01.nfo", false, false},
		{"temporary subtitle", "Work.S01E01.zh-CN.ass.part", "Work.S01E01.zh-CN.ass", false, false},
		{"late subtitle", "Work.S01E01.zh-CN.ass", "Work.S01E01.zh-CN.ass", true, false},
		{"late metadata", "Work.S01E01.nfo", "Work.S01E01.nfo", true, false},
		{"late temporary subtitle", "Work.S01E01.zh-CN.ass.part", "Work.S01E01.zh-CN.ass", true, false},
		{"late preserved mtime", "Work.S01E01.zh-CN.ass", "Work.S01E01.zh-CN.ass", true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, metadata, _, rule := newMediaOrganizeTest(t)
			const video = "Work.S01E01.strm"
			const other = "Work.S01E02.strm"
			const videoBody = "http://original.invalid/episode-one\r\n"
			const otherBody = "http://original.invalid/episode-two\r\n"
			const sidecarBody = "original sidecar bytes\r\n"
			mediaTestFile(t, rule.Source, video, videoBody)
			mediaTestFile(t, rule.Source, other, otherBody)
			incoming := filepath.Join(rule.Source, scenario.incoming)
			arrived := false
			arrive := func() {
				if err := os.WriteFile(incoming, []byte(sidecarBody), 0644); err != nil {
					t.Fatal(err)
				}
				if scenario.preserveMtime {
					old := time.Now().Add(-time.Minute)
					if err := os.Chtimes(incoming, old, old); err != nil {
						t.Fatal(err)
					}
				}
				arrived = true
			}
			if scenario.duringRecognition {
				s.metadata = &mediaCompanionArrivalRecognition{mediaRecognitionFake: metadata, arrive: arrive}
			} else {
				arrive()
			}

			first, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &first, "auto")
			if !arrived {
				t.Fatal("fixture did not reach the metadata stage where the companion arrives")
			}
			for name, want := range map[string]string{video: videoBody, scenario.incoming: sidecarBody} {
				body, err := os.ReadFile(filepath.Join(rule.Source, name))
				if err != nil || string(body) != want {
					t.Fatalf("video and its unfinished companion must remain intact: %s: %v", name, err)
				}
			}
			otherMoved := false
			for _, entry := range first.Entries {
				if entry.File.ID != other {
					continue
				}
				body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(entry.Relative)))
				if entry.Status != "done" || err != nil || string(body) != otherBody {
					t.Fatal("unrelated settled video did not move intact", entry, err)
				}
				otherMoved = true
			}
			if !otherMoved {
				t.Fatal("an unfinished companion blocked an unrelated video in the same directory")
			}

			completed := filepath.Join(rule.Source, scenario.completed)
			if incoming != completed {
				if err := os.Rename(incoming, completed); err != nil {
					t.Fatal(err)
				}
			}
			old := time.Now().Add(-time.Minute)
			if err := os.Chtimes(completed, old, old); err != nil {
				t.Fatal(err)
			}
			second, err := s.Start(rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			mediaTestProcess(t, s, &second, "auto")
			want := map[string]string{video: videoBody, scenario.completed: sidecarBody}
			for _, entry := range second.Entries {
				expected, ok := want[entry.File.ID]
				if !ok {
					continue
				}
				body, err := os.ReadFile(filepath.Join(rule.Target, filepath.FromSlash(entry.Relative)))
				if entry.Status != "done" || err != nil || string(body) != expected {
					t.Fatal("settled video and companion did not move together intact", entry, err)
				}
				delete(want, entry.File.ID)
			}
			if len(want) != 0 {
				t.Fatal("settled companions were omitted from the next batch", want)
			}
			for _, name := range []string{video, scenario.completed} {
				if _, err := os.Stat(filepath.Join(rule.Source, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("completed source was not moved", name, err)
				}
			}
		})
	}
}
