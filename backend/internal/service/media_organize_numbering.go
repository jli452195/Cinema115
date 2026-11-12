package service

import "strm115/backend/internal/mediafacts"

// 自动整理采用各季完整标准坐标，描述话数不替代坐标；无标准声明时保留原有弱编号处理。
func mediaOrganizeFileNumbering(file *OrganizeFile) {
	if file.MediaType != "video" {
		return
	}
	facts := mediaOrganizeFilenameFacts(file.Name)
	file.filenameFacts = &facts
	claim := facts.Coordinate
	if claim.Found {
		if claim.Valid {
			file.Season, file.Episode, file.EndEpisode = claim.Numbering.Season, claim.Numbering.Episode, claim.Numbering.EndEpisode
			file.EpisodeExplicit = true
		}
		return
	}
	organizeFileNumbering(file)
}

// 零集号必须有明确声明或人工修正，不能将未识别出的默认零值当作 E00。
func mediaOrganizeHasNumbering(file OrganizeFile) bool {
	if file.Episode > 0 || file.Episode == 0 && file.EpisodeExplicit {
		return true
	}
	if file.Episode != 0 {
		return false
	}
	facts := file.filenameFacts
	if facts == nil {
		parsed := mediafacts.ParseFilename(file.Name)
		facts = &parsed
	}
	return facts.Coordinate.Valid && facts.Coordinate.Numbering.Episode == 0
}

// 仅在目标季集无效时解释编号原因，人工填写的有效坐标优先，不重复保存文件事实。
func mediaOrganizeNumberingError(file OrganizeFile) (code, message string) {
	if file.Season < 0 || file.Season > 1000 || file.Episode < 0 || file.Episode > 10000 {
		return "NUMBERING_INVALID", "目标季集超出有效范围，请修正季集"
	}
	if mediaOrganizeHasNumbering(file) {
		return "", ""
	}
	facts := file.filenameFacts
	if facts == nil {
		parsed := mediafacts.ParseFilename(file.Name)
		facts = &parsed
	}
	switch facts.Coordinate.Code {
	case "NUMBERING_CONFLICT":
		return "NUMBERING_CONFLICT", "文件中存在相互冲突的季集声明，请修正季集"
	case "NUMBERING_INVALID":
		return "NUMBERING_INVALID", "文件中的季集格式无效，请修正季集"
	default:
		return "NUMBERING_MISSING", "缺少有效集号，请填写目标季集"
	}
}
