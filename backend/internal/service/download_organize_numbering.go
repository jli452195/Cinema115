package service

import (
	"path"
	"regexp"
	"strings"
	"unicode"

	"strm115/backend/internal/releaseparse"
)

var downloadEpisodeWithTitle = regexp.MustCompile(`(?i)^(.+?)\s+([0-9]{1,4}(?:v[0-9]{1,3})?)\s+-\s+(.+?)\s+(\([^()]+\)|\[[^\[\]]+\])(?:\s+\[[0-9a-f]{8}\])?$`)
var downloadCompactSeason = regexp.MustCompile(`(?i)\bseason([0-9]{1,4})\b`)
var downloadSeasonRange = regexp.MustCompile(`^\s*[-+&~,～〜至到]\s*[0-9]`)
var downloadFanDisc = regexp.MustCompile(`(?i)^fan[ ._-]+disc$|\[fan[ ._-]+disc\]`)

const (
	downloadSeasonPending  = -1
	downloadWorkDelimiters = " \t._-[]()（）【】"
)

type downloadSeasonFolder struct {
	work             string
	season           int
	special          bool
	firstEpisode     int
	distinctEpisodes bool
}

// 下载整理读取文件和直接父目录的季集声明，编号偏移沿用现有编辑流程。
func completeDownloadFileNumbering(files []OrganizeFile) {
	type candidate struct {
		index, episode int
		folder         *downloadSeasonFolder
	}
	folders := make(map[string]*downloadSeasonFolder)
	candidates := make([]candidate, 0)
	for i, file := range files {
		if file.TitleOnly || file.Season != 1 || file.EpisodeExplicit {
			continue
		}
		stem := strings.TrimSuffix(file.Name, path.Ext(file.Name))
		if organizeFileSeason.MatchString(stem) || organizeSpecialFile.MatchString(stem) || downloadFanDisc.MatchString(stem) {
			continue
		}
		directory := path.Dir(file.SourcePath)
		folder := folders[directory]
		if folder == nil {
			folder = downloadNumberingSeasonFolder(directory)
			folders[directory] = folder
		}
		if folder.special {
			continue
		}
		if file.Episode > 0 {
			if season := downloadSeasonForKnownEpisode(stem, folder); season != 0 {
				files[i].Season = season
			}
			continue
		}
		if file.Episode != 0 || folder.season == 0 || len(files) < 2 {
			continue
		}
		episode := downloadEpisodeFromTitledFile(stem, folder.work)
		if episode == 0 {
			continue
		}
		if folder.firstEpisode == 0 {
			folder.firstEpisode = episode
		} else if episode != folder.firstEpisode {
			folder.distinctEpisodes = true
		}
		candidates = append(candidates, candidate{index: i, episode: episode, folder: folder})
	}
	for _, match := range candidates {
		if match.folder.distinctEpisodes {
			files[match.index].Season, files[match.index].Episode = match.folder.season, match.episode
		}
	}
}

func downloadNumberingSeasonFolder(directory string) *downloadSeasonFolder {
	name := path.Base(directory)
	if organizeSpecialFile.MatchString(name) || downloadFanDisc.MatchString(name) {
		return &downloadSeasonFolder{special: true}
	}
	// 直接父目录提供季号，祖先目录只用于排除特典子树。
	for parent := path.Dir(directory); parent != "." && parent != "/"; parent = path.Dir(parent) {
		base := path.Base(parent)
		if organizeSpecialDirectory.MatchString(strings.Trim(base, " ._-[]【】()（）")) || downloadFanDisc.MatchString(base) {
			return &downloadSeasonFolder{special: true}
		}
	}
	work, season := downloadSeasonLabel(name)
	if work == "" || season < 1 {
		return &downloadSeasonFolder{}
	}
	return &downloadSeasonFolder{work: work, season: season}
}

func downloadSeasonLabel(name string) (work string, season int) {
	// 连写季标只在下载整理的解析副本中规范化，沿用已有季标语法。
	name = downloadCompactSeason.ReplaceAllString(name, "Season ${1}")
	markers := animeSeasonMarker.FindAllStringIndex(name, 2)
	if len(markers) == 0 {
		return "", 0
	}
	if len(markers) > 1 {
		return "", downloadSeasonPending
	}
	start, end := markers[0][0], markers[0][1]
	if downloadSeasonRange.MatchString(name[end:]) {
		return "", downloadSeasonPending
	}
	if animePartMarker.MatchString(name[end:]) {
		return "", 0
	}
	work = strings.TrimRight(name[:start], downloadWorkDelimiters)
	return work, animeSeasonHint([]string{name[start:end]})
}

func downloadSeasonForKnownEpisode(stem string, folder *downloadSeasonFolder) int {
	if folder.season == 0 && !animeSeasonMarker.MatchString(stem) && !downloadCompactSeason.MatchString(stem) {
		return 0
	}
	// 只读发布集号之前的作品字段，分集标题或技术字段里的词不决定季号。
	prefix, ok := releaseparse.EpisodePrefix(organizeNumberingStem(stem))
	if !ok {
		return 0
	}
	prefix = strings.TrimRight(prefix, downloadWorkDelimiters)
	work, season := downloadSeasonLabel(prefix)
	if season < 0 {
		return season
	}
	if season == 0 {
		work = prefix
	}
	if folder.season == 0 || !strings.EqualFold(work, folder.work) {
		return season
	}
	if season != 0 && season != folder.season {
		return downloadSeasonPending
	}
	return folder.season
}

func downloadEpisodeFromTitledFile(stem, work string) int {
	match := downloadEpisodeWithTitle.FindStringSubmatch(stem)
	if match == nil || !strings.EqualFold(strings.TrimRight(match[1], downloadWorkDelimiters), work) || !strings.ContainsFunc(match[3], unicode.IsLetter) {
		return 0
	}
	spec := resourceSpecification(match[4])
	if spec.Resolution == "" && spec.Codec == "" {
		return 0
	}
	// 只在解析副本里标明候选集号，继续由共享解析器排除其他编号、年份和技术数字。
	parsed, err := releaseparse.Title(match[1] + " [" + match[2] + "] - " + match[3] + " " + match[4])
	if err != nil || parsed.Kind != "episode" || parsed.Episode == nil || parsed.RawEpisode != match[2] {
		return 0
	}
	return *parsed.Episode
}
