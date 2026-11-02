package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMediaLocalDirectoriesBrowseAndNormalize(t *testing.T) {
	s := &MediaOrganizer{}
	root := t.TempDir()
	for _, name := range []string{"z-last", "A-first", "中文目录"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "video.mkv"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := s.LocalDirectories(context.Background(), filepath.Join(root, "A-first", ".."))
	if err != nil {
		t.Fatal(err)
	}
	want, err := mediaLocalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if out.Path != want || out.ParentPath == nil || *out.ParentPath != filepath.Dir(want) || out.Truncated {
		t.Fatal("unexpected location", out)
	}
	if len(out.Directories) != 3 || out.Directories[0].Name != "A-first" || out.Directories[1].Name != "z-last" || out.Directories[2].Name != "中文目录" {
		t.Fatal("directories are missing, unsorted, or include files", out.Directories)
	}
	for _, directory := range out.Directories {
		if directory.Path != filepath.Join(want, directory.Name) {
			t.Fatal("entry does not contain a full server path", directory)
		}
	}
	empty, err := s.LocalDirectories(context.Background(), out.Directories[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(empty)
	if err != nil || empty.Directories == nil || !strings.Contains(string(raw), `"directories":[]`) {
		t.Fatal("empty directories must be an array", string(raw), err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 4 {
		t.Fatal("browsing changed directory contents", err, entries)
	}
}

func TestMediaLocalDirectoriesRoots(t *testing.T) {
	s := &MediaOrganizer{}
	out, err := s.LocalDirectories(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if out.ParentPath != nil || out.Directories == nil {
		t.Fatal("top level must have no parent and an array of directories", out)
	}
	if runtime.GOOS != "windows" {
		if out.Path != string(filepath.Separator) {
			t.Fatal("Unix initial location is not filesystem root", out)
		}
		return
	}
	if out.Path != "" || len(out.Directories) == 0 {
		t.Fatal("Windows initial location is not the drive list", out)
	}
	volume := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	found := false
	for _, directory := range out.Directories {
		if strings.EqualFold(directory.Path, volume) {
			found = true
		}
	}
	if !found {
		t.Fatal("temporary directory drive is missing", out.Directories)
	}
	drive, err := s.LocalDirectories(context.Background(), volume)
	if err != nil || drive.ParentPath == nil || *drive.ParentPath != "" {
		t.Fatal("drive root must return to the virtual drive list", drive, err)
	}
}

func TestMediaLocalDirectoriesInvalidAndCancelled(t *testing.T) {
	s := &MediaOrganizer{}
	root := t.TempDir()
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"relative/path", file, filepath.Join(root, "not-mounted")} {
		if _, err := s.LocalDirectories(context.Background(), value); err == nil {
			t.Fatalf("invalid directory accepted: %q", value)
		}
	}
	if _, err := s.LocalDirectories(context.Background(), filepath.Join(root, "not-mounted")); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "目录不存在或未挂载") {
		t.Fatal("missing mount message does not preserve the cause", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.LocalDirectories(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled listing did not stop", err)
	}
	if _, err := s.LocalDirectories(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled initial listing did not stop", err)
	}
}

func TestMediaLocalDirectoriesLimit(t *testing.T) {
	s := &MediaOrganizer{}
	root := t.TempDir()
	for index := 0; index < mediaLocalDirectoryLimit+1; index++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("directory-%04d", index)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.LocalDirectories(context.Background(), root)
	if err != nil || !out.Truncated || len(out.Directories) != mediaLocalDirectoryLimit {
		t.Fatal("directory limit not reported", len(out.Directories), out.Truncated, err)
	}
}

func TestMediaLocalDirectoriesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "linked")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	s := &MediaOrganizer{}
	out, err := s.LocalDirectories(context.Background(), root)
	if err != nil || len(out.Directories) != 2 || out.Directories[0].Name != "linked" {
		t.Fatal("directory symlink is not browsable", out, err)
	}
	selected, err := s.LocalDirectories(context.Background(), link)
	want, wantErr := mediaLocalRoot(target)
	if err != nil || wantErr != nil || selected.Path != want {
		t.Fatal("symlink location was not normalized", selected, err, wantErr)
	}
}

func TestMediaLocalDirectoriesPermissionMessage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission fixture")
	}
	root := filepath.Join(t.TempDir(), "denied")
	if err := os.Mkdir(root, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0700) })
	_, err := (&MediaOrganizer{}).LocalDirectories(context.Background(), root)
	if err == nil {
		t.Skip("current process can read mode-000 directories")
	}
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "没有权限读取此目录") {
		t.Fatal("permission failure is not actionable", err)
	}
}
