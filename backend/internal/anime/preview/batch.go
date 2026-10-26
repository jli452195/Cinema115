package preview

import (
	"path"
	"strings"
	"unicode"

	"strm115/backend/internal/model"
	"strm115/backend/internal/utils/torrentmeta"
)

// MatchBatchFiles uses the ordinary RSS evaluator on file names, never the
// enclosing post/directory's quality labels. Only unambiguous videos survive.
func MatchBatchFiles(binding model.AnimeBinding, rules model.AnimeRules, files []torrentmeta.File) ([]model.AnimeBatchFile, error) {
	if len(files) == 0 || len(files) > 512 {
		return nil, ErrInput
	}
	compiled, err := CompileRules(rules)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(files))
	seen := make(map[string]bool, len(files))
	directories := make(map[string]bool)
	for _, file := range files {
		if file.Path == "" || len(file.Path) > 4096 || strings.HasPrefix(file.Path, "/") || strings.HasPrefix(file.Path, "../") || path.Clean(file.Path) != file.Path || file.Path == "." || file.Path == ".." || strings.ContainsAny(file.Path, "\\:") || strings.ContainsFunc(file.Path, unicode.IsControl) || file.Size < 0 || seen[file.Path] {
			return nil, ErrInput
		}
		seen[file.Path] = true
		// Include the task root and torrent directories in the same bounded tree
		// budget used by the download worker, before submitting anything to 115.
		if strings.Count(file.Path, "/") > 8 {
			return nil, ErrInput
		}
		for dir := path.Dir(file.Path); dir != "."; dir = path.Dir(dir) {
			directories[dir] = true
		}
		if len(directories)+1 > 32 || len(files)+len(directories)+1 > 512 {
			return nil, ErrInput
		}
		switch strings.ToLower(path.Ext(file.Path)) {
		case ".mkv", ".mp4", ".avi", ".ts", ".m2ts", ".mov", ".wmv", ".flv", ".webm", ".mpg", ".mpeg":
		default:
			continue
		}
		name := path.Base(file.Path)
		parsed, err := compiled.Parse(name)
		if err != nil {
			return nil, err
		}
		if parsed.Kind != "episode" || parsed.Episode == nil {
			continue
		}
		item := Item{Release: model.AnimeRelease{PostKey: file.Path, Title: name, Parse: parsed}}
		if err := evaluateCompiled(binding, rules, compiled, &item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	PreferCurrentVersions(items)
	counts := make(map[int]int, len(items))
	ambiguous := make(map[int]bool)
	for _, item := range items {
		// Unlike duplicate RSS posts, two physical files at the same winning
		// revision cannot be collapsed by choosing an arbitrary path.
		if item.ReasonCode == "RELEASE_VERSION_DUPLICATE" {
			ambiguous[*item.Release.Parse.Episode] = true
		}
		if item.Selectable {
			counts[*item.Release.Parse.Episode]++
		}
	}
	byPath := make(map[string]int64, len(files))
	for _, file := range files {
		byPath[file.Path] = file.Size
	}
	out := make([]model.AnimeBatchFile, 0, len(items))
	for _, item := range items {
		if item.Selectable && counts[*item.Release.Parse.Episode] == 1 && !ambiguous[*item.Release.Parse.Episode] {
			out = append(out, model.AnimeBatchFile{Path: item.Release.PostKey, Size: byPath[item.Release.PostKey], Parse: item.Release.Parse})
		}
	}
	return out, nil
}
