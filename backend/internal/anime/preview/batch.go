package preview

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
	"strm115/backend/internal/utils/torrentmeta"
)

// The shared parser leaves conflicting/unsupported quality tokens unknown.
// Such a label cannot supply a missing requirement to another file.
var batchQualityToken = regexp.MustCompile(`(?i)\b(?:[0-9]{3,4}p|4k|[0-9]{3,4}[x×][0-9]{3,4})\b`)

// MatchBatchFiles checks the actual episode files. Missing publisher labels may
// come from the post or directories, but explicit file specifications win.
func MatchBatchFiles(binding model.AnimeBinding, rules model.AnimeRules, title string, files []torrentmeta.File) ([]model.AnimeBatchFile, error) {
	if len(files) == 0 || len(files) > 512 {
		return nil, ErrInput
	}
	compiled, err := CompileRules(rules)
	if err != nil {
		return nil, err
	}
	fileRules := compiled
	fileRules.match, fileRules.exclude = nil, nil // Patterns are checked with their batch context below.
	exclusions := compiled
	exclusions.match = nil
	titleParse := releaseparse.Result{}
	if title != "" {
		titleParse, err = releaseparse.Title(title)
		if err != nil {
			return nil, err
		}
	}
	contextParses := make(map[string]releaseparse.Result, 32)
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
		if parsed.Spec.Resolution == "" && batchQualityToken.MatchString(name) {
			continue
		}
		fileSpec := parsed.Spec
		fileSpec.Container = strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
		// Exclusions apply to every layer, even when a conflicting specification
		// prevents that layer from supplying missing match labels.
		if exclusions.Filter(title, file.Path) != "" {
			continue
		}
		contexts := []string{name}
		for dir := path.Dir(file.Path); dir != "."; dir = path.Dir(dir) {
			label := path.Base(dir)
			context, found := contextParses[dir]
			if !found {
				context, err = releaseparse.Title("[" + label + "]")
				if err != nil {
					return nil, err
				}
				contextParses[dir] = context
			}
			if compatibleBatchSpec(fileSpec, context, label) {
				contexts = append(contexts, label)
			}
		}
		if compatibleBatchSpec(fileSpec, titleParse, title) {
			contexts = append(contexts, title)
		}
		if compiled.Filter(contexts...) != "" {
			continue
		}
		item := Item{Release: model.AnimeRelease{PostKey: file.Path, Title: name, Parse: parsed}}
		if err := evaluateCompiled(binding, rules, fileRules, &item); err != nil {
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

func compatibleBatchSpec(file releaseparse.Spec, parsed releaseparse.Result, label string) bool {
	context := parsed.Spec
	if parsed.SpecAmbiguous || context.Resolution == "" && batchQualityToken.MatchString(label) {
		return false
	}
	for _, pair := range [][2]string{{file.Resolution, context.Resolution}, {file.Codec, context.Codec}, {file.Container, context.Container}, {file.BitDepth, context.BitDepth}, {file.SubtitleMode, context.SubtitleMode}} {
		if pair[0] != "" && pair[1] != "" && pair[0] != pair[1] {
			return false
		}
	}
	if len(context.Languages) > 0 {
		for _, language := range file.Languages {
			if !slices.Contains(context.Languages, language) {
				return false
			}
		}
	}
	return true
}
