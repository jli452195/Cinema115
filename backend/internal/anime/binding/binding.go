// Package binding evaluates one explicitly confirmed publisher numbering
// space. It does not infer chapter identity or share numbering between feeds.
package binding

import "strm115/backend/internal/model"

type Projection struct {
	Status         string `json:"status"` // in_scope, out_of_scope, needs_confirmation
	ReasonCode     string `json:"reasonCode,omitempty"`
	SourceEpisode  *int   `json:"sourceEpisode"`
	ChapterEpisode *int   `json:"chapterEpisode"`
}

// Project preserves source numbering. A correspondence is display metadata,
// independent of a download minimum or any optional TMDB naming mapping.
func Project(binding model.AnimeBinding, parsed model.AnimeReleaseParse) (Projection, error) {
	out := Projection{Status: "needs_confirmation", ReasonCode: "SOURCE_BINDING_UNCONFIRMED"}
	if err := binding.Validate(false); err != nil {
		return out, err
	}
	if err := parsed.Validate(); err != nil {
		return out, err
	}
	if parsed.Kind == "episode" && parsed.Episode != nil {
		n := *parsed.Episode
		out.SourceEpisode = &n
	}
	if !binding.Confirmed || binding.Scope == nil {
		return out, nil
	}
	if out.SourceEpisode == nil {
		out.ReasonCode = "EPISODE_NUMBER_UNRESOLVED"
		return out, nil
	}
	n := *out.SourceEpisode
	if scope := binding.Scope; scope.Kind == "range" && (n < *scope.First || n > *scope.Last) {
		out.Status, out.ReasonCode = "out_of_scope", "OUTSIDE_CHAPTER_SCOPE"
		return out, nil
	}
	if c := binding.ChapterCorrespondence; c != nil {
		chapter := n - c.SourceEpisode + c.ChapterEpisode
		if chapter < 0 || chapter > 99999 {
			out.ReasonCode = "CHAPTER_NUMBER_UNRESOLVED"
			return out, nil
		}
		out.ChapterEpisode = &chapter
	}
	out.Status, out.ReasonCode = "in_scope", ""
	return out, nil
}
