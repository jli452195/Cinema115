package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"strm115/backend/internal/mediacategory"
)

func classificationTestStore(t *testing.T) (*MediaClassification, string, ClassificationDocument) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config", "classification.yaml")
	s, err := OpenMediaClassification(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	document, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s, path, document
}

func classificationTestRead(t *testing.T, s *MediaClassification) ClassificationDocument {
	t.Helper()
	document, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func classificationTestNoTemporaryFiles(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".classification-") {
			t.Fatal("temporary YAML file was left behind", entry.Name())
		}
	}
}

func TestMediaClassificationPersistsExactYAMLAndSnapshots(t *testing.T) {
	s, path, original := classificationTestStore(t)
	if original.YAML != mediacategory.DefaultYAML() || len(original.Revision) != 64 || original.ActiveRevision != original.Revision || original.LoadError != "" {
		t.Fatal("default file was not initialized", original)
	}
	pinned, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	source := "# keep this editor comment\r\n" + strings.ReplaceAll(strings.Replace(original.YAML, "电影/其他电影", "电影/新目录", 1), "\n", "\r\n")
	saved, err := s.Save(context.Background(), source, original.Revision)
	if err != nil || saved.YAML != source || saved.Revision == original.Revision || saved.ActiveRevision != saved.Revision {
		t.Fatal("save failed", saved, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != source {
		t.Fatal("saving changed source bytes or comments", string(raw), err)
	}
	oldResult, _ := pinned.Classifier.Classify(mediacategory.Facts{Kind: "movie"})
	current, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	newResult, _ := current.Classifier.Classify(mediacategory.Facts{Kind: "movie"})
	if oldResult.Path != "电影/其他电影" || newResult.Path != "电影/新目录" || pinned.YAML != original.YAML {
		t.Fatal("an already captured snapshot changed", oldResult, newResult)
	}
	current.YAML, current.Revision, current.Classifier = "changed", "changed", nil
	unchanged, _ := s.Snapshot()
	if unchanged.YAML != source || unchanged.Revision != saved.Revision || unchanged.Classifier == nil {
		t.Fatal("returned snapshot exposed mutable store state", unchanged)
	}
	reopened, err := OpenMediaClassification(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	restored, _ := reopened.Snapshot()
	if restored.YAML != source || restored.Revision != saved.Revision {
		t.Fatal("restart lost saved YAML or its version", restored)
	}
	classificationTestNoTemporaryFiles(t, path)
}

func TestMediaClassificationConflictsAndExternalReload(t *testing.T) {
	s, path, original := classificationTestStore(t)
	external := strings.Replace(original.YAML, "剧集/其他剧集", "剧集/外部修改", 1)
	if err := os.WriteFile(path, []byte(external), 0600); err != nil {
		t.Fatal(err)
	}
	document := classificationTestRead(t, s)
	pinned, _ := s.Snapshot()
	if document.YAML != external || document.Revision == document.ActiveRevision || pinned.YAML != original.YAML {
		t.Fatal("reading external edits must not silently activate them", document, pinned)
	}
	proposed := strings.Replace(original.YAML, "剧集/其他剧集", "剧集/页面修改", 1)
	if _, err := s.Save(context.Background(), proposed, original.Revision); !errors.Is(err, ErrClassificationConflict) {
		t.Fatal("stale page overwrote external edits", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != external {
		t.Fatal("conflict changed the disk file", err)
	}
	loaded, err := s.Reload(context.Background())
	if err != nil || loaded.YAML != external || loaded.ActiveRevision != document.Revision {
		t.Fatal("reload did not activate the edited file", loaded, err)
	}
	snapshot, _ := s.Snapshot()
	result, err := snapshot.Classifier.Classify(mediacategory.Facts{Kind: "tv"})
	if err != nil || result.Path != "剧集/外部修改" {
		t.Fatal("reloaded classifier did not change", result, err)
	}
	if _, err := s.Save(context.Background(), proposed, original.Revision); !errors.Is(err, ErrClassificationConflict) {
		t.Fatal("reload made an old page revision valid", err)
	}
	classificationTestNoTemporaryFiles(t, path)
}

func TestMediaClassificationInvalidFileRecovery(t *testing.T) {
	s, path, original := classificationTestStore(t)
	invalid := strings.Replace(original.YAML, "genres: [16]", "genres: []", 1)
	if _, err := s.Save(context.Background(), invalid, original.Revision); !errors.Is(err, mediacategory.ErrConfig) {
		t.Fatal("invalid save was accepted", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != original.YAML {
		t.Fatal("invalid save replaced valid YAML", err)
	}
	if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reload(context.Background()); !errors.Is(err, mediacategory.ErrConfig) {
		t.Fatal("invalid file activated", err)
	}
	active, _ := s.Snapshot()
	if active.YAML != original.YAML {
		t.Fatal("failed reload discarded the last good snapshot")
	}
	document := classificationTestRead(t, s)
	if document.YAML != invalid || document.LoadError == "" || document.Diagnostic == nil || document.Revision == original.Revision {
		t.Fatal("invalid file must remain readable for repair", document)
	}
	restarted, err := OpenMediaClassification(context.Background(), path)
	if restarted == nil || !errors.Is(err, mediacategory.ErrConfig) {
		t.Fatal("bad startup must leave a repairable manager", err)
	}
	if _, err := restarted.Snapshot(); !errors.Is(err, ErrClassificationUnavailable) {
		t.Fatal("bad startup silently activated defaults", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != invalid {
		t.Fatal("bad startup overwrote the original file", err)
	}
	if _, err := restarted.Save(context.Background(), original.YAML, original.Revision); !errors.Is(err, ErrClassificationConflict) {
		t.Fatal("repair ignored the current invalid file revision", err)
	}
	repaired, err := restarted.Save(context.Background(), original.YAML, document.Revision)
	if err != nil || repaired.LoadError != "" || repaired.ActiveRevision != original.Revision {
		t.Fatal("explicit repair failed", repaired, err)
	}
	classificationTestNoTemporaryFiles(t, path)
}

func TestMediaClassificationDeletionAndReplay(t *testing.T) {
	s, path, original := classificationTestStore(t)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reload(context.Background()); !errors.Is(err, mediacategory.ErrConfig) {
		t.Fatal("missing file silently recreated during reload", err)
	}
	if _, err := s.Save(context.Background(), original.YAML, original.Revision); !errors.Is(err, ErrClassificationConflict) {
		t.Fatal("deletion did not invalidate a stale editor", err)
	}
	if snapshot, err := s.Snapshot(); err != nil || snapshot.YAML != original.YAML {
		t.Fatal("in-memory snapshot depended on the deleted file", err)
	}
	document := classificationTestRead(t, s)
	if document.Revision != "" || document.YAML != "" || document.LoadError == "" {
		t.Fatal("missing file state cannot be explicitly restored", document)
	}
	saved, err := s.Save(context.Background(), original.YAML, document.Revision)
	if err != nil || saved.Revision != original.Revision {
		t.Fatal("explicit restoration failed", saved, err)
	}
	changed := "# saved once\n" + original.YAML
	first, err := s.Save(context.Background(), changed, saved.Revision)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.Save(context.Background(), changed, saved.Revision)
	if err != nil || replayed != first {
		t.Fatal("identical retry did not confirm the original save", replayed, err)
	}
	classificationTestNoTemporaryFiles(t, path)
}

func TestMediaClassificationConcurrentSaves(t *testing.T) {
	s, path, original := classificationTestStore(t)
	var workers sync.WaitGroup
	var successes atomic.Int32
	for i := range 8 {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			source := fmt.Sprintf("# editor %d\n", index) + original.YAML
			_, err := s.Save(context.Background(), source, original.Revision)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrClassificationConflict) {
				t.Error(err)
			}
		}(i)
	}
	workers.Wait()
	current := classificationTestRead(t, s)
	snapshot, err := s.Snapshot()
	if successes.Load() != 1 || err != nil || current.YAML != snapshot.YAML || current.Revision != snapshot.Revision {
		t.Fatal("concurrent pages overwrote each other", successes.Load(), current, snapshot, err)
	}
	classificationTestNoTemporaryFiles(t, path)
}

func TestMediaClassificationFailedWritesAndCancellation(t *testing.T) {
	s, path, original := classificationTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Save(ctx, "# canceled\n"+original.YAML, original.Revision); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled save executed", err)
	}
	if _, err := s.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("read ignored cancellation", err)
	}
	if _, err := s.Reload(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("reload ignored cancellation", err)
	}
	if err := writeClassification(ctx, path, original.YAML, original.Revision); !errors.Is(err, context.Canceled) {
		t.Fatal("write ignored cancellation", err)
	}
	if err := writeClassification(context.Background(), path, original.YAML, "wrong-version"); !errors.Is(err, ErrClassificationConflict) {
		t.Fatal("final file version check was skipped", err)
	}
	classificationTestNoTemporaryFiles(t, path)
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(context.Background(), original.YAML, original.Revision); err == nil {
		t.Fatal("non-file configuration path was accepted")
	}
	if err := writeClassification(context.Background(), path, original.YAML, original.Revision); err == nil {
		t.Fatal("write to a non-file target succeeded")
	}
	if snapshot, err := s.Snapshot(); err != nil || snapshot.Revision != original.Revision {
		t.Fatal("storage failure discarded active rules", snapshot, err)
	}
	classificationTestNoTemporaryFiles(t, path)
}

func TestMediaClassificationOversizedDiskFile(t *testing.T) {
	s, path, original := classificationTestStore(t)
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", mediacategory.MaxYAMLBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background()); !errors.Is(err, mediacategory.ErrConfig) {
		t.Fatal("oversized disk file was not bounded", err)
	}
	if _, err := s.Reload(context.Background()); !errors.Is(err, mediacategory.ErrConfig) {
		t.Fatal("oversized file activated", err)
	}
	if snapshot, err := s.Snapshot(); err != nil || snapshot.Revision != original.Revision {
		t.Fatal("oversized reload discarded the active rules", err)
	}
}
