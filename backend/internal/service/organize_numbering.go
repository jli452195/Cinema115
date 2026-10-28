package service

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/releaseparse"
)

var organizeFileRange = regexp.MustCompile(`(?i)^(\d{1,4})\s*([-~～〜至到+&])\s*E?(\d{1,4})$`)
var organizeFileSeason = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])S(\d{1,4})\s*E`)
var organizeCompactEpisode = regexp.MustCompile(`(?i)-(\d{1,4}(?:v\d{1,3})?)$`)
var organizeSPNumber = regexp.MustCompile(`(?i)(^|[^a-z0-9])SP[ ._-]*#?[ ._-]*([0-9])`)
var organizeSPMarker = regexp.MustCompile(`(?i)(^|[^a-z0-9])SP($|[\[\]【】()（） ._-])`)
var organizeSpecialFile = regexp.MustCompile(`(?i)(?:^|[\[【 ._(（\-])(?:NCOP|NCED|OP|ED|OVA|OAD|SP|PV|CM|SPECIALS?|BONUS|EXTRAS?|FILM|MOVIE|MENU|MINI[ ._-]*ANIME|TOKUTEN)(?:[0-9]|$|[\]】 ._)）\-])|特典|番外|特别篇|特別篇|劇場版|剧场版`)
var organizeSpecialDirectory = regexp.MustCompile(`(?i)^(?:NCOP|NCED|OP|ED|OVA|OAD|SP|PV|CM|SPECIALS?|BONUS|EXTRAS?|FILM|MOVIE|MENU|MINI[ ._-]*ANIME|TOKUTEN|特典(?:映像)?|番外|特别篇|特別篇|菜单|菜單)(?:[ ._-]?[0-9]{1,4})?$`)

// Only a video's own numbering is evidence. Parent pack ranges never number
// its children. Explicit SP numbering suggests season zero; other extras and
// ambiguous expressions remain editable without an inferred episode.
func organizeFileNumbering(file *OrganizeFile) {
	if file.MediaType != "video" {
		return
	}
	directory := strings.Trim(path.Base(path.Dir(file.SourcePath)), " ._-[]【】()（）")
	original := strings.TrimSuffix(file.Name, path.Ext(file.Name))
	// Reuse the ordinary numbering/ambiguity checks after normalizing only SP
	// markers. Keep the original filename and RSS parser's special classification.
	name := organizeSPNumber.ReplaceAllString(original, "${1}E${2}")
	name = organizeSPMarker.ReplaceAllString(name, "${1}${2}")
	spDirectory := strings.EqualFold(directory, "SP")
	special := name != original || spDirectory
	if organizeSpecialFile.MatchString(name) || organizeSpecialDirectory.MatchString(directory) && !spDirectory {
		return
	}
	season := file.Season
	if m := organizeFileSeason.FindStringSubmatch(file.Name); m != nil {
		season, _ = strconv.Atoi(m[1])
		if special && season != 0 {
			return
		}
	}
	if special {
		season = 0
	}
	if numbered, found := mediafacts.StandardNumbering(name); found {
		if numbered.Episode == 0 || special && numbered.Season != 0 {
			return
		}
		file.Season, file.Episode, file.EndEpisode = numbered.Season, numbered.Episode, numbered.EndEpisode
		return
	}
	// Treat filename underscores as separators (Title_-_03_(BD...)), then reuse
	// the existing dash, metadata and ambiguity checks. Keep the source untouched.
	name = strings.ReplaceAll(name, "_", " ")
	name = organizeCompactEpisode.ReplaceAllString(name, " - ${1}")
	parsed, err := releaseparse.Title(name)
	if err != nil {
		return
	}
	if parsed.Kind == "episode" && parsed.Episode != nil && *parsed.Episode > 0 && *parsed.Episode <= 10000 {
		file.Episode = *parsed.Episode
	} else if parsed.Kind == "batch" {
		m := organizeFileRange.FindStringSubmatch(parsed.RawEpisode)
		if m == nil {
			return
		}
		start, _ := strconv.Atoi(m[1])
		end, _ := strconv.Atoi(m[3])
		if start < 1 || end <= start || end > 10000 || start >= 1900 && start <= 2199 && end >= 1900 && end <= 2199 || (m[2] == "+" || m[2] == "&") && end != start+1 {
			return
		}
		file.Episode, file.EndEpisode = start, end
	} else {
		return
	}
	file.Season = season
}
