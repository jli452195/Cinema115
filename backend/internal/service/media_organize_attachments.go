package service

import (
	"path"
	"regexp"
	"strings"

	"strm115/backend/internal/naming"
)

var mediaAttachmentExtensions = extensionSet("nfo,xml,json,jpg,jpeg,png,webp,tbn,bmp,gif,avif", "")
var mediaArtworkStem = regexp.MustCompile(`(?i)^(?:poster|fanart|banner|clearart|discart|disc|keyart|landscape|logo|clearlogo|folder|thumb|backdrop|background|cover)(?:[0-9]+)?$`)

func mediaAttachment(name string) bool {
	return mediaAttachmentExtensions[strings.ToLower(path.Ext(name))]
}

func mediaAttachmentMetadata(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".nfo" || ext == ".xml" || ext == ".json"
}

func mediaAttachmentVideo(file OrganizeFile, files []OrganizeFile) (owner, longest int, tail string) {
	owner, longest = -1, -1
	stem := strings.TrimSuffix(file.Name, path.Ext(file.Name))
	for i, video := range files {
		if video.MediaType != "video" || video.ParentID != file.ParentID {
			continue
		}
		vs := strings.TrimSuffix(video.Name, path.Ext(video.Name))
		if len(vs) > len(stem) || !strings.EqualFold(stem[:len(vs)], vs) || len(vs) < len(stem) && stem[len(vs)] != '.' && stem[len(vs)] != '-' {
			continue
		}
		suffix := stem[len(vs):]
		if suffix != "" && (mediaAttachmentMetadata(file.Name) || !mediaArtworkStem.MatchString(suffix[1:])) {
			continue
		}
		if len(vs) > longest {
			owner, longest, tail = i, len(vs), stem[len(vs):]
		} else if len(vs) == longest {
			owner = -1
		}
	}
	return owner, longest, tail
}

// Only conventional folder metadata may use folder scope. A named companion
// from another movie is never folder artwork, even when just one video remains.
func mediaAttachmentFolder(file OrganizeFile) (parent, subdir string, shared bool) {
	parent = path.Dir(file.SourcePath)
	stem := strings.ToLower(strings.TrimSuffix(file.Name, path.Ext(file.Name)))
	if mediaAttachmentMetadata(file.Name) {
		return parent, "", stem == "tvshow" || stem == "movie" || stem == "season"
	}
	if !mediaAttachment(file.Name) {
		return parent, "", false
	}
	if base := path.Base(parent); base == ".actors" || base == "extrafanart" {
		return path.Dir(parent), base, true
	}
	return parent, "", mediaArtworkStem.MatchString(stem)
}

func mediaAttachmentInScope(video OrganizeFile, parent string) bool {
	vp := path.Dir(video.SourcePath)
	return video.MediaType == "video" && (vp == parent || parent == "." || strings.HasPrefix(vp, parent+"/"))
}

func mediaAttachmentHasVideo(file OrganizeFile, files []OrganizeFile) bool {
	if _, longest, _ := mediaAttachmentVideo(file, files); longest >= 0 {
		return true // Ambiguous same-stem videos also require review, not deletion.
	}
	parent, _, shared := mediaAttachmentFolder(file)
	if shared {
		for _, video := range files {
			if mediaAttachmentInScope(video, parent) {
				return true
			}
		}
	}
	return false
}

func mediaEntryFiles(entries []MediaOrganizeEntry) []OrganizeFile {
	files := make([]OrganizeFile, len(entries))
	for i, e := range entries {
		files[i] = e.File
	}
	return files
}

// Recheck frozen plans too. A skipped source video must not turn shared artwork
// into a companion of the only video left in this run's execution list.
func mediaValidateAttachmentOwners(run *MediaOrganizeRun, files []OrganizeFile) {
	selected := make(map[string]bool)
	for _, e := range run.Entries {
		if e.File.MediaType == "video" {
			selected[e.File.ID] = true
		}
	}
	for i := range run.Entries {
		e := &run.Entries[i]
		if e.File.MediaType != "attachment" || e.Status == "done" || e.VideoID == "" || !mediaInTask(*e, run.TaskFileID) {
			continue
		}
		owner, longest, _ := mediaAttachmentVideo(e.File, files)
		valid := owner >= 0 && files[owner].ID == e.VideoID
		if longest < 0 {
			parent, _, shared := mediaAttachmentFolder(e.File)
			valid = shared
			for _, video := range files {
				if shared && mediaAttachmentInScope(video, parent) && !selected[video.ID] {
					valid = false
					break
				}
			}
		}
		if !valid {
			if run.TaskFileID != "" {
				e.Relative, e.Target = "", ""
				e.Status, e.Message = "retained", "附件未匹配对应视频，保留来源待核对"
				continue
			}
			e.Group, e.VideoID, e.Relative, e.Target = "", "", "", ""
			e.Status, e.Message = "retained", "附件未匹配对应视频或目录含未处理视频，保留来源"
		}
	}
}

// File companions follow the longest unambiguous video stem. Folder artwork
// keeps its name and follows only a unique work/destination, never a guess.
func mediaOrganizeAttachments(run *MediaOrganizeRun, results map[string]naming.Result) {
	groups := make(map[string]MediaOrganizeGroup, len(run.Groups))
	for _, g := range run.Groups {
		groups[g.ID] = g
	}
	files := mediaEntryFiles(run.Entries)
	for i := range run.Entries {
		e := &run.Entries[i]
		if e.File.MediaType != "attachment" || e.Status == "done" || !mediaInTask(*e, run.TaskFileID) {
			continue
		}
		originalGroup, originalVideo := e.Group, e.VideoID
		e.Status, e.Message = "retained", "无法确认附件归属，保留来源"
		e.Group, e.VideoID, e.Relative, e.Target = "", "", "", ""
		owner, longest, tail := mediaAttachmentVideo(e.File, files)
		if owner >= 0 {
			video := run.Entries[owner]
			e.Group, e.VideoID = video.Group, video.File.ID
			if result, ok := results[video.File.ID]; ok {
				companion, err := naming.Companion(result, tail, e.File.Name)
				if err == nil {
					e.Relative = companion.RelativePath
				}
			}
		} else if longest < 0 {
			// Shared folder metadata must belong to one work. Multiple episodes
			// in the same season are fine; mixed movies or seasons stay in place.
			parent, subdir, shared := mediaAttachmentFolder(e.File)
			if !shared {
				continue
			}
			group, destination, first := "", "", -1
			workID, workKind := 0, ""
			for j, video := range run.Entries {
				if !mediaAttachmentInScope(video.File, parent) {
					continue
				}
				vp := path.Dir(video.File.SourcePath)
				result, ok := results[video.File.ID]
				if !ok {
					first = -1
					break
				}
				dir := result.Directory
				g := groups[video.Group]
				kind, tmdbID := g.Kind, g.TMDBID
				if kind == "tv" && (vp != parent || strings.EqualFold(e.File.Name, "tvshow.nfo")) {
					dir = path.Dir(dir)
				}
				if first >= 0 && (workID != tmdbID || workKind != kind || destination != dir) {
					first = -1
					break
				}
				group, destination, first = video.Group, dir, j
				workID, workKind = tmdbID, kind
			}
			if first >= 0 {
				e.Group, e.VideoID = group, run.Entries[first].File.ID
				e.Relative = path.Join(destination, subdir, e.File.Name)
			}
		}
		if e.Relative != "" {
			e.Status, e.Message = "ready", ""
			e.Target = path.Join(run.Rule.Target, e.Relative)
		} else if e.VideoID != "" {
			e.Status, e.Message = "unmatched", "对应视频尚未匹配，附件保留来源"
		}
		if run.TaskFileID != "" && e.VideoID != originalVideo {
			e.Group, e.VideoID = originalGroup, originalVideo
			e.Relative, e.Target = "", ""
			e.Status, e.Message = "retained", "公共附件归属不再唯一，保留来源待核对"
		}
	}
}
