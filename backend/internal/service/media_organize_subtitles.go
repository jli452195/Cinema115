package service

import "strm115/backend/internal/mediafacts"

func mediaOrganizeSubtitles(files []OrganizeFile) []OrganizeSubtitle {
	return organizeSubtitlesWithCandidates(files, mediaOrganizeSubtitleCandidates)
}

// 未就绪的视频也参与字幕唯一性判断，来源身份和实际父目录保持不变。
func mediaIncludeVideoCandidates(files, candidates []OrganizeFile) []OrganizeFile {
	present := make(map[string]bool, len(files)+len(candidates))
	for _, file := range files {
		present[file.ID] = true
	}
	for _, file := range candidates {
		if file.MediaType == "video" && !present[file.ID] {
			files = append(files, file)
			present[file.ID] = true
		}
	}
	return files
}

// 自动整理保留同目录匹配优先级，跨目录仅接受完整同名字幕。
func mediaOrganizeSubtitleCandidates(sub OrganizeFile, files []OrganizeFile, matcher *mediafacts.SubtitleMatcher) ([]string, string) {
	owners, tail := organizeSubtitleCandidates(sub, files, matcher)
	if len(owners) > 0 {
		return owners, tail
	}
	return organizeSubtitleStemCandidates(sub, files, false)
}
