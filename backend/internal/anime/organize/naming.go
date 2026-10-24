package organize

import (
	"fmt"

	"strm115/backend/internal/model"
	"strm115/backend/internal/naming"
)

// Render uses subscription-owned numbering and metadata. Both release previews
// and the file planner supply facts to this path; neither consults global TV numbering.
func Render(renderer *naming.Renderer, target model.AnimeNamingTarget, projection model.AnimeNamingProjection, name string, titleOnly bool, values map[naming.Field]string) (naming.Result, error) {
	if projection.Status != "mapped" || projection.Season == nil || projection.Episode == nil {
		return naming.Result{}, fmt.Errorf("未设置对应季集")
	}
	if values == nil {
		values = make(map[naming.Field]string, 6)
	}
	values[naming.Title], values[naming.OriginalTitle], values[naming.EnglishTitle] = target.Title, target.OriginalTitle, target.EnglishTitle
	values[naming.Year], values[naming.TMDBID], values[naming.EpisodeTitle] = target.Year, target.TMDBID, projection.EpisodeName
	return renderer.Render(naming.Data{Values: values, Season: projection.Season, Episode: projection.Episode, OriginalName: name, TitleOnly: titleOnly})
}
