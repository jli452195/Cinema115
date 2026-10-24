package storage

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"strm115/backend/internal/naming"
)

func TestNamingTemplatePersistenceAndReplay(t *testing.T) {
	s, file := testStore(t)
	ctx := context.Background()
	movie, err := s.NamingTemplate(ctx, naming.Movie)
	if err != nil || movie.Revision != 0 {
		t.Fatalf("default: %+v, %v", movie, err)
	}
	movie.Template.Filename[0].Prefix = "电影 "
	saved, err := s.SaveNamingTemplate(ctx, movie.Template, 0)
	if err != nil || saved.Revision != 1 {
		t.Fatalf("save: %+v, %v", saved, err)
	}
	replayed, err := s.SaveNamingTemplate(ctx, movie.Template, 0)
	if err != nil || !reflect.DeepEqual(saved, replayed) {
		t.Fatalf("lost response replay: %+v, %v", replayed, err)
	}
	tv, err := s.NamingTemplate(ctx, naming.TV)
	if err != nil || tv.Revision != 0 || tv.Template.Filename[0].Prefix != "" {
		t.Fatalf("movie changed TV: %+v, %v", tv, err)
	}
	reopened, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	read, err := reopened.NamingTemplate(ctx, naming.Movie)
	if err != nil || !reflect.DeepEqual(saved, read) {
		t.Fatalf("reopen: %+v, %v", read, err)
	}
	read.Template.Filename[0].Prefix = "另一个配置 "
	if _, err := s.SaveNamingTemplate(ctx, read.Template, 0); !errors.Is(err, ErrNamingConflict) {
		t.Fatalf("stale overwrite: %v", err)
	}
	if _, err := s.SaveNamingTemplate(ctx, read.Template, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveNamingTemplate(ctx, movie.Template, 0); !errors.Is(err, ErrNamingConflict) {
		t.Fatalf("old replay after another save: %v", err)
	}
	read.Template.Filename = nil
	if _, err := s.SaveNamingTemplate(ctx, read.Template, 2); !errors.Is(err, naming.ErrTemplate) {
		t.Fatalf("invalid template: %v", err)
	}
	if _, err := s.SaveNamingTemplate(ctx, movie.Template, -1); !errors.Is(err, naming.ErrInput) {
		t.Fatalf("invalid revision: %v", err)
	}
	read, err = s.NamingTemplate(ctx, naming.Movie)
	if err != nil || read.Revision != 2 {
		t.Fatalf("rejected writes changed record: %+v, %v", read, err)
	}
}

func TestNamingTemplateConcurrentSaves(t *testing.T) {
	s, _ := testStore(t)
	var wg sync.WaitGroup
	errorsOut := make(chan error, 2)
	for _, prefix := range []string{"A", "B"} {
		wg.Add(1)
		go func(prefix string) {
			defer wg.Done()
			template, err := naming.Default(naming.TV)
			if err == nil {
				template.Filename[0].Prefix = prefix
				_, err = s.SaveNamingTemplate(context.Background(), template, 0)
			}
			errorsOut <- err
		}(prefix)
	}
	wg.Wait()
	close(errorsOut)
	successes, conflicts := 0, 0
	for err := range errorsOut {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrNamingConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", successes, conflicts)
	}
}

func TestNamingTemplateRejectsIncompleteStoredJSON(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, raw := range []string{`{}`, `{"kind":"movie"}`, `{"kind":"tv","filename":[{"field":"title"}]}`} {
		if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO naming_templates(kind,revision,template_json) VALUES('movie',1,?)`, raw); err != nil {
			t.Fatal(err)
		}
		if _, err := s.NamingTemplate(ctx, naming.Movie); err == nil {
			t.Fatalf("accepted incomplete or mismatched stored template: %s", raw)
		}
	}
}
