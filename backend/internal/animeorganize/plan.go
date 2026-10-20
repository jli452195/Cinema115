// Package animeorganize plans names using saved facts only. It performs no I/O.
package animeorganize

import (
	"fmt"
	"path"
	"strings"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/model"
)

type Item struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	OriginalPath string `json:"originalPath"`
	TargetPath   string `json:"targetPath"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
	VideoID      string `json:"videoId,omitempty"`
}
type Plan struct {
	Mode                 string `json:"mode"`
	Revision             int64  `json:"revision"`
	Items                []Item `json:"items"`
	Ready                int    `json:"ready"`
	NeedsReview          int    `json:"needsReview"`
	CloudConflictChecked bool   `json:"cloudConflictChecked"`
}

func base(v string) string { return strings.TrimSuffix(v, path.Ext(v)) }
func targetName(n model.AnimeNamingPlan, p model.AnimeNamingProjection, group, ext string) string {
	return model.AnimeNamingPath(n.Target, *p.Season, *p.Episode, group, ext)
}
func isVideo(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mkv", ".mp4", ".avi", ".ts", ".m2ts", ".mov", ".wmv", ".flv", ".webm", ".mpg", ".mpeg":
		return true
	}
	return false
}
func isSubtitle(ext string) bool {
	switch strings.ToLower(ext) {
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx", ".sup":
		return true
	}
	return false
}
func subtitleSuffix(sub, video string) (string, bool) {
	subRunes, videoRunes := []rune(sub), []rune(video)
	if len(subRunes) < len(videoRunes) || !strings.EqualFold(string(subRunes[:len(videoRunes)]), video) {
		return "", false
	}
	suffix := string(subRunes[len(videoRunes):])
	if suffix == "" {
		return "", true
	}
	if suffix[0] != '.' {
		return "", false
	}
	for _, tag := range strings.Split(strings.ToLower(suffix[1:]), ".") {
		switch tag {
		case "chs", "cht", "sc", "tc", "zh", "zh-cn", "zh-tw", "zh-hans", "zh-hant", "ja", "jp", "jpn", "en", "eng", "forced", "sdh", "default", "简体", "繁体", "简日", "繁日":
		default:
			return "", false
		}
	}
	return suffix, true
}

// Build accepts an already validated manifest from the fixed task directory.
// Unmatched attachments are preserved. Ambiguous files never get write plans.
func Build(job model.AnimeDownloadJob) (Plan, error) {
	out := Plan{Mode: "original", Items: []Item{}}
	if job.Snapshot.Organization == nil {
		return out, nil
	}
	o := job.Snapshot.Organization
	if o.Validate(job.Snapshot.Target) != nil || job.Snapshot.Naming == nil || job.Snapshot.Naming.Validate() != nil {
		return out, fmt.Errorf("invalid frozen organization plan")
	}
	out.Mode, out.Revision = "standard", o.PlanRevision
	if job.Files == nil {
		return out, nil
	}
	if err := job.Files.Validate(job); err != nil {
		return out, fmt.Errorf("invalid file manifest: %w", err)
	}
	entries := make(map[string]model.AnimeDriveEntry, len(job.Files.Entries))
	videoCount := 0
	for _, e := range job.Files.Entries {
		entries[e.ID] = e
		if e.Kind == "file" && isVideo(path.Ext(e.Name)) {
			videoCount++
		}
	}
	rootPath := strings.TrimRight(job.Snapshot.Target.ParentPath, "/") + "/" + job.DirectoryName
	original := func(e model.AnimeDriveEntry) string {
		parts := []string{e.Name}
		parent := e.ParentCID
		for parent != *job.DirectoryCID {
			p, ok := entries[parent]
			if !ok {
				return ""
			}
			parts = append([]string{p.Name}, parts...)
			parent = p.ParentCID
		}
		return rootPath + "/" + strings.Join(parts, "/")
	}
	videoIndices := make([]int, 0, videoCount)
	for _, e := range job.Files.Entries {
		if e.Kind != "file" || !isVideo(path.Ext(e.Name)) {
			continue
		}
		item := Item{ID: e.ID, Kind: "video", OriginalPath: original(e), Status: "needs_review", Reason: "unmapped"}
		parsed, err := animeparse.Title(e.Name)
		// A single unnamed video may use its trusted release title. Explicit
		// batch/composite/SP evidence must never be replaced by that fallback.
		if err == nil && parsed.Kind == "unknown" && parsed.RawEpisode == "" && videoCount == 1 {
			parsed = job.Snapshot.Release.Parse
		}
		projection := job.Snapshot.Naming.Project(job.Snapshot.Source.SourceID, parsed, job.Snapshot.Source.GroupName)
		item.Reason = projection.Status
		if projection.Status == "mapped" {
			item.TargetPath = path.Join(o.Target.ParentPath, targetName(*job.Snapshot.Naming, projection, job.Snapshot.Source.GroupName, path.Ext(e.Name)))
			item.Status, item.Reason = "ready", "mapped"
		} else if projection.Status == "ignored" {
			item.Status = "preserved"
		}
		videoIndices = append(videoIndices, len(out.Items))
		out.Items = append(out.Items, item)
	}
	for _, e := range job.Files.Entries {
		if e.Kind != "file" || isVideo(path.Ext(e.Name)) {
			continue
		}
		item := Item{ID: e.ID, Kind: "attachment", OriginalPath: original(e), Status: "preserved", Reason: "attachment_preserved"}
		if isSubtitle(path.Ext(e.Name)) {
			item.Kind, item.Status, item.Reason = "subtitle", "needs_review", "subtitle_unmatched"
			matched, suffix := -1, ""
			for _, index := range videoIndices {
				v := out.Items[index]
				entry := entries[v.ID]
				s, ok := subtitleSuffix(base(e.Name), base(entry.Name))
				if entry.ParentCID != e.ParentCID || !ok {
					continue
				}
				if matched >= 0 {
					matched = -2
					break
				}
				matched, suffix = index, s
			}
			if matched >= 0 {
				v := out.Items[matched]
				item.VideoID = v.ID
				if v.Status == "ready" {
					item.TargetPath = base(v.TargetPath) + suffix + path.Ext(e.Name)
					item.Status, item.Reason = "ready", "subtitle_matched"
				} else {
					item.Reason = "video_needs_review"
				}
			}
		}
		out.Items = append(out.Items, item)
	}
	paths := make(map[string]int, len(out.Items))
	for i := range out.Items {
		item := &out.Items[i]
		if item.TargetPath == "" {
			continue
		}
		if len(path.Base(item.TargetPath)) > 255 || len(item.TargetPath) > 16384 {
			item.Status, item.Reason = "needs_review", "name_too_long"
			continue
		}
		key := strings.ToLower(item.TargetPath)
		if previous, ok := paths[key]; ok {
			out.Items[previous].Status, out.Items[previous].Reason = "conflict", "duplicate_target"
			item.Status, item.Reason = "conflict", "duplicate_target"
		} else {
			paths[key] = i
		}
	}
	for i := range out.Items {
		item := &out.Items[i]
		if item.VideoID != "" {
			for _, v := range out.Items {
				if v.ID == item.VideoID && v.Status != "ready" {
					item.Status, item.Reason = "needs_review", "video_needs_review"
				}
			}
		}
	}
	for _, item := range out.Items {
		if item.Status == "ready" {
			out.Ready++
		} else if item.Status != "preserved" {
			out.NeedsReview++
		}
	}
	return out, nil
}
