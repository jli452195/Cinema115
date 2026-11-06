package service

import (
	"context"
	"path"
	"slices"
	"strings"

	"strm115/backend/internal/client/p115"
)

func mediaLocalCandidate(id, name string, size int64) OrganizeFile {
	return OrganizeFile{ShareFile: p115.ShareFile{ID: id, Name: name, Size: size}, ParentID: path.Dir(id), SourcePath: id, MediaType: mediaFileType(name)}
}

func mediaTemporaryBase(name string) string {
	for {
		before := name
		for _, suffix := range []string{".part", ".partial", ".tmp", ".temp", ".!qb", ".crdownload", ".download", ".filepart", ".aria2"} {
			if strings.HasSuffix(strings.ToLower(name), suffix) {
				name = name[:len(name)-len(suffix)]
				break
			}
		}
		if name == before {
			return name
		}
	}
}

// The complete candidate listing includes writing files and temporary sidecars.
// Reuse the ordinary ownership rules, including ambiguous candidate sets, so
// removing a waiting video never assigns its sidecars to a different video.
func mediaLocalCompanionOwners(files []OrganizeFile) map[string][]string {
	files = mediaAttachmentMatchFiles(files)
	owners := make(map[string][]string)
	matcher := organizeSubtitleMatcher(files)
	for _, file := range files {
		switch file.MediaType {
		case "subtitle":
			owners[file.ID], _ = mediaOrganizeSubtitleCandidates(file, files, matcher)
		case "attachment":
			owner, longest, _ := mediaAttachmentVideo(file, files)
			if owner >= 0 {
				owners[file.ID] = []string{files[owner].ID}
				continue
			}
			if longest >= 0 {
				for _, video := range files {
					if _, length, _ := mediaAttachmentVideo(file, []OrganizeFile{video}); length == longest {
						owners[file.ID] = append(owners[file.ID], video.ID)
					}
				}
				continue
			}
			parent, _, shared := mediaAttachmentFolder(file)
			if shared {
				for _, video := range files {
					if mediaAttachmentInScope(video, parent) {
						owners[file.ID] = append(owners[file.ID], video.ID)
					}
				}
			}
		}
	}
	for _, ids := range owners {
		slices.Sort(ids)
	}
	return owners
}

func (f *mediaOrganizeFiles) holdLocalCompanions(entries []MediaOrganizeEntry, pending map[string]bool) []MediaOrganizeEntry {
	files := f.localCandidates
	blocked := make(map[string]bool)
	owners := mediaLocalCompanionOwners(files)
	for _, file := range files {
		if file.MediaType == "video" && pending[file.ID] {
			blocked[file.ID] = true
		}
	}
	for id, waiting := range pending {
		if waiting {
			for _, owner := range owners[id] {
				blocked[owner] = true
			}
		}
	}
	for _, file := range files {
		if file.MediaType == "video" && blocked[file.ID] {
			f.settlingVideos[file.ParentID] = true
		}
	}
	return slices.DeleteFunc(entries, func(entry MediaOrganizeEntry) bool {
		if entry.File.MediaType == "video" {
			return blocked[entry.File.ID]
		}
		for _, owner := range owners[entry.File.ID] {
			if blocked[owner] {
				return true
			}
		}
		return false
	})
}

// 本地文件可能仍在写入，识别后复查一次；云端主文件由原生写入预检核验，晚到附件下一轮补齐。
// 复查快照仅用于判断稳定性，清理仍以最初扫描记录为准。
func (f *mediaOrganizeFiles) recheckLocalCompanions(ctx context.Context, run *MediaOrganizeRun) error {
	if f.rule.Storage != "local" || !slices.ContainsFunc(run.Entries, func(e MediaOrganizeEntry) bool {
		return e.File.MediaType == "video" && e.Relative != "" && (e.Status == "ready" || e.Status == "error")
	}) {
		return nil
	}
	current := &mediaOrganizeFiles{rule: f.rule, localReady: f.localReady}
	if _, err := current.scan(ctx); err != nil {
		return &mediaLocalScanError{err}
	}
	beforeOwners, currentOwners := mediaLocalCompanionOwners(f.localCandidates), mediaLocalCompanionOwners(current.localCandidates)
	before, after := make(map[string]OrganizeFile), make(map[string]OrganizeFile)
	for _, file := range f.localCandidates {
		before[file.ID] = file
	}
	for _, file := range current.localCandidates {
		after[file.ID] = file
	}
	blocked := make(map[string]bool)
	changed := func(id string) bool {
		a, existsBefore := before[id]
		b, existsAfter := after[id]
		if !existsBefore || !existsAfter || a.Name != b.Name || a.Size != b.Size || a.MediaType != b.MediaType || current.localPending[id] {
			return true
		}
		x, y := f.localSnapshots[id], current.localSnapshots[id]
		return x == nil || y == nil || !x.ModTime().Equal(y.ModTime()) || !slices.Equal(beforeOwners[id], currentOwners[id])
	}
	for _, files := range []map[string]OrganizeFile{before, after} {
		for id, file := range files {
			if !changed(id) {
				continue
			}
			if file.MediaType == "video" {
				blocked[id] = true
			}
			for _, owner := range beforeOwners[id] {
				blocked[owner] = true
			}
			for _, owner := range currentOwners[id] {
				blocked[owner] = true
			}
		}
	}
	for i := range run.Entries {
		e := &run.Entries[i]
		waiting := blocked[e.File.ID]
		for _, owners := range []map[string][]string{beforeOwners, currentOwners} {
			for _, owner := range owners[e.File.ID] {
				waiting = waiting || blocked[owner]
			}
		}
		if waiting && e.Status != "done" && mediaInRunTask(run, *e) {
			e.Status, e.Message = "retained", "视频或关联文件仍在变化，保留来源待下一轮一起整理"
			e.Relative, e.Target, e.Result = "", "", nil
		}
	}
	// An initially orphaned companion may now belong to a newly arrived video.
	// Keep it in the source instead of using the earlier orphan cleanup decision.
	f.unrelated = slices.DeleteFunc(f.unrelated, func(e MediaOrganizeEntry) bool {
		return len(currentOwners[e.File.ID]) > 0
	})
	return nil
}
