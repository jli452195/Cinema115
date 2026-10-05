package service

import (
	"context"
	"errors"
	"testing"

	"strm115/backend/internal/client/p115"
)

func TestSharePathResolutionAndFailures(t *testing.T) {
	for _, raw := range []string{"relative", "/../电影", "/电影//剧集", "/电影/./", "/a\\b", "/a\nb"} {
		if _, err := normalizeSharePath(raw); err == nil {
			t.Fatalf("accepted invalid path %q", raw)
		}
	}
	calls := 0
	list := func(_ context.Context, cid string, offset int) (*p115.SharePage, error) {
		calls++
		if cid == "0" && offset == 0 {
			return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "1", Name: "其他", Directory: true}}}, nil
		}
		if cid == "0" && offset == 1 {
			return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "2", Name: "电影", Directory: true}}}, nil
		}
		return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "3", Name: "2026", Directory: true}}}, nil
	}
	got, err := resolveSharePath(context.Background(), GenerateInput{SharePath: "/电影/2026/"}, list)
	if err != nil || got.CID != "3" || got.SharePath != "" || calls != 3 {
		t.Fatalf("resolution = %+v, %v, calls %d", got, err, calls)
	}
	for _, input := range []GenerateInput{{SharePath: "/"}, {CID: "9", SharePath: "/电影"}} {
		if _, err := resolveSharePath(context.Background(), input, list); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatal("root or explicit CID added requests")
	}
	if _, err := resolveSharePath(context.Background(), GenerateInput{SharePath: "/missing"}, list); err == nil {
		t.Fatal("missing directory fell back to root")
	}
	_, err = resolveSharePath(context.Background(), GenerateInput{SharePath: "/电影"}, func(context.Context, string, int) (*p115.SharePage, error) {
		return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "1", Name: "电影", Directory: true}, {ID: "2", Name: "电影", Directory: true}}}, nil
	})
	if err == nil {
		t.Fatal("ambiguous directory accepted")
	}
	_, err = resolveSharePath(context.Background(), GenerateInput{SharePath: "/电影"}, func(context.Context, string, int) (*p115.SharePage, error) { return nil, p115.ErrShareUnderReview })
	if !errors.Is(err, p115.ErrShareUnderReview) {
		t.Fatal("review error lost", err)
	}
}

func TestSharePathGenerationAndFingerprint(t *testing.T) {
	input := generationOptions(t)
	input.SharePath = "/电影"
	client := shareListFunc(func(_ context.Context, _ *p115.ShareInfo, cid string, _ int, cookie string) (*p115.SharePage, error) {
		if cookie != "pinned" {
			t.Fatal("credentials changed")
		}
		switch cid {
		case "0":
			return &p115.SharePage{Count: 2, Files: []p115.ShareFile{{ID: "10", Name: "电影", Directory: true}, {ID: "11", Name: "outside.mkv", SHA1: "0123456789ABCDEF0123456789ABCDEF01234567", Size: 2 << 20}}}, nil
		case "10":
			return &p115.SharePage{Count: 1, Files: []p115.ShareFile{{ID: "12", Name: "inside.mkv", SHA1: "0123456789ABCDEF0123456789ABCDEF01234567", Size: 2 << 20}}}, nil
		default:
			t.Fatalf("unexpected CID %s", cid)
			return nil, nil
		}
	})
	g := &STRMGenerator{Root: t.TempDir(), Client: client, Cookie: func() string { return "pinned" }}
	result, err := g.Generate(context.Background(), input)
	if err != nil || result.Written != 1 {
		t.Fatalf("generation %+v: %v", result, err)
	}
	first, err := shareRunFingerprint(AutomationSettings{}, "cookie", []GenerateInput{input})
	if err != nil {
		t.Fatal(err)
	}
	input.SharePath = "/"
	second, err := shareRunFingerprint(AutomationSettings{}, "cookie", []GenerateInput{input})
	if err != nil || first == second {
		t.Fatal("different scopes shared task cache", err)
	}
}

type pathTransferFake struct{ *transferFake }

func (f pathTransferFake) ReceiveShare(_ context.Context, _ *p115.ShareInfo, ids []string, cid, _ string) error {
	if len(ids) != 1 || ids[0] != "11" || cid != "999" {
		return errors.New("path transfer included files outside selected directory")
	}
	f.calls = append(f.calls, "receive")
	return nil
}

func TestSharePathWorkflowScopesTransferAndClearsPathForNewShare(t *testing.T) {
	fake := &transferFake{}
	w, input := setupWorkflow(t, fake)
	w.Client = pathTransferFake{fake}
	input.SharePath = "/剧集"
	base := w.Generator
	w.Generator = generateFunc(func(ctx context.Context, input GenerateInput) (*GenerationResult, error) {
		if input.SharePath != "" {
			t.Fatal("original path applied to newly created share")
		}
		return base.Generate(ctx, input)
	})
	if _, err := w.Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
}

func TestSharePathBatchOwnShareAndMissingPath(t *testing.T) {
	fake := &transferFake{ownerID: "42"}
	w, input := setupWorkflow(t, fake)
	input.SharePath = "/剧集"
	w.Generator = generateFunc(func(_ context.Context, input GenerateInput) (*GenerationResult, error) {
		if input.CID != "10" || input.SharePath != "" {
			t.Fatalf("unresolved original scope: %+v", input)
		}
		return &GenerationResult{Written: 1}, nil
	})
	results, err := w.GenerateBatch(context.Background(), []GenerateInput{input})
	if err != nil || len(results) != 1 || results[0].Err != nil {
		t.Fatalf("batch: %+v %v", results, err)
	}
	input.SharePath = "/不存在"
	if _, err := w.Generate(context.Background(), input); err == nil {
		t.Fatal("missing path accepted")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("path resolution performed mutations: %v", fake.calls)
	}
}
