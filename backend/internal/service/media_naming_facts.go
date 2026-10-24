package service

import (
	"path"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
)

func mediaFileValues(name string, hint DownloadFileSpec) map[naming.Field]string {
	if hint.Fansub == "" {
		hint.Fansub = downloadFansub(name)
	}
	return mediafacts.Values(name, hint.Spec, hint.Fansub)
}

// Only an already-enumerated directory containing one video may supply missing
// subtitle facts. Never copy a pack's labels to several videos or search ancestors.
func mediaDirectorySubtitleValues(files []OrganizeFile) {
	counts := make(map[string]int, len(files))
	for _, file := range files {
		if file.MediaType == "video" {
			counts[file.ParentID]++
		}
	}
	for i := range files {
		file := &files[i]
		if file.MediaType != "video" || file.TitleOnly || counts[file.ParentID] != 1 {
			continue
		}
		if file.NamingValues == nil {
			file.NamingValues = mediaFileValues(file.Name, DownloadFileSpec{})
		}
		mediafacts.InheritSubtitles(file.NamingValues, path.Base(path.Dir(file.SourcePath)))
	}
}
