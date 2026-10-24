package service

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"strm115/backend/internal/animeparse"
)

var organizeFileRange = regexp.MustCompile(`(?i)^(\d{1,4})\s*([-~～〜至到+&])\s*E?(\d{1,4})$`)
var organizeFileSeason = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])S(\d{1,3})\s*E`)
var organizeCompactEpisode = regexp.MustCompile(`(?i)-(\d{1,4}(?:v\d{1,3})?)$`)
var organizeSpecialFile = regexp.MustCompile(`(?i)(?:^|[\[ ._(\-])(?:NCOP|NCED|OP|ED|OVA|OAD|SP|PV|CM|SPECIALS?|BONUS|EXTRAS?|FILM|MOVIE)(?:[0-9]|$|[\] ._)\-])|特典|番外|特别篇|特別篇|劇場版|剧场版`)

// Only a video's own numbering is evidence. Parent pack ranges never number
// its children; labelled extras and ambiguous expressions remain editable.
func organizeFileNumbering(file *OrganizeFile) {
	if file.MediaType != "video" || organizeSpecialFile.MatchString(file.Name) || organizeSpecialFile.MatchString(path.Base(path.Dir(file.SourcePath))) {
		return
	}
	name := strings.TrimSuffix(file.Name, path.Ext(file.Name))
	// File-only normalization: title-01 uses the existing dash grammar while
	// retaining its metadata, range and ambiguity checks. RSS parsing is unchanged.
	name = organizeCompactEpisode.ReplaceAllString(name, " - ${1}")
	parsed, err := animeparse.Title(name)
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
	if m := organizeFileSeason.FindStringSubmatch(file.Name); m != nil {
		file.Season, _ = strconv.Atoi(m[1])
	}
}
