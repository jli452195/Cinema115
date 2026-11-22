package preview

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/model"
	"strm115/backend/internal/releaseparse"
	"strm115/backend/internal/utils/torrentmeta"
)

// BatchEpisodeMap 按原始文件路径与大小记录已确认的来源集号。
type BatchEpisodeMap map[torrentmeta.File]int

var (
	specialBatchRange = regexp.MustCompile(`^0{0,4}1\s*-\s*([0-9]{1,5})$`)
	specialBatchPart  = regexp.MustCompile(`(?i)(?:^|[ ._-])special[ ._-]+part[ ._-]+([0-9]{1,3})`)
)

// SpecialBatchSourceEpisodes 仅在完整清单的分部号和季集号都匹配时确认来源编号。
func SpecialBatchSourceEpisodes(rules model.AnimeRules, release releaseparse.Result, plan *model.AnimeNamingPlan, sourceID string, files []torrentmeta.File) BatchEpisodeMap {
	if !rules.SpecialChapter || rules.EpisodePattern != "" || release.Kind != "batch" || plan == nil || plan.Target.Season != 0 || len(files) > 512 {
		return nil
	}
	match := specialBatchRange.FindStringSubmatch(release.RawEpisode)
	if match == nil {
		return nil
	}
	last, _ := strconv.Atoi(match[1])
	if last < 2 || last > len(files) {
		return nil
	}
	var mapping *model.AnimeSourceMapping
	for i := range plan.Sources {
		if plan.Sources[i].SourceRef == sourceID {
			mapping = &plan.Sources[i]
			break
		}
	}
	if mapping == nil || len(mapping.Segments) != 1 || len(mapping.Overrides) != 0 {
		return nil
	}
	segment := mapping.Segments[0]
	if segment.TargetSeason != 0 || segment.SourceBase == segment.TargetBase || segment.First > 1 || segment.Last != nil && *segment.Last < last {
		return nil
	}

	episodes := make(BatchEpisodeMap, last)
	seen := make(map[int]bool, last)
	for _, file := range files {
		if !batchVideo(file.Path) {
			continue
		}
		name := path.Base(file.Path)
		stem := strings.TrimSuffix(name, path.Ext(name))
		part := specialPartEpisode(stem)
		if part < 1 || part > last || seen[part] {
			return nil
		}
		coordinate := mediafacts.ParseStandardCoordinates(stem)
		if !coordinate.Valid || coordinate.Numbering.Season != 0 || coordinate.Numbering.EndEpisode != 0 {
			return nil
		}
		parsed, err := releaseparse.TitleForChapter(name, true)
		if err != nil || parsed.Kind != "episode" || parsed.Episode == nil || *parsed.Episode != coordinate.Numbering.Episode {
			return nil
		}
		projection := plan.Project(sourceID, releaseparse.Result{Kind: "episode", Episode: &part})
		if projection.Status != "mapped" || projection.Episode == nil || *projection.Episode != coordinate.Numbering.Episode {
			return nil
		}
		seen[part] = true
		episodes[file] = part
	}
	if len(episodes) != last {
		return nil
	}
	return episodes
}

func specialPartEpisode(stem string) int {
	matches := specialBatchPart.FindAllStringSubmatchIndex(stem, 2)
	if len(matches) != 1 {
		return 0
	}
	match := matches[0]
	tail := stem[match[3]:]
	if tail != "" && !strings.ContainsRune(" ._-", rune(tail[0])) {
		return 0
	}
	// 排除分部范围和小数；允许紧跟画质规格，例如 Part.1.1080p。
	tail = strings.TrimLeft(tail, " ._-")
	if tail != "" && tail[0] >= '0' && tail[0] <= '9' {
		quality := batchQualityToken.FindStringIndex(tail)
		if quality == nil || quality[0] != 0 {
			return 0
		}
	}
	part, _ := strconv.Atoi(stem[match[2]:match[3]])
	return part
}

// FrozenSpecialBatchSourceEpisodes 从已确认的任务文件中恢复来源集号。
func FrozenSpecialBatchSourceEpisodes(files []model.AnimeBatchFile) BatchEpisodeMap {
	var episodes BatchEpisodeMap
	for _, file := range files {
		if !file.SourceEpisodeNormalized || file.Parse.Kind != "episode" || file.Parse.Episode == nil || *file.Parse.Episode < 1 || *file.Parse.Episode > 512 {
			continue
		}
		if episodes == nil {
			episodes = make(BatchEpisodeMap, len(files))
		}
		episodes[torrentmeta.File{Path: file.Path, Size: file.Size}] = *file.Parse.Episode
	}
	return episodes
}
