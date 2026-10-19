package httpapi

import (
	"github.com/gin-gonic/gin"
	"strm115/backend/internal/animepreview"
	"strm115/backend/internal/model"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
	"time"
)

type animePreviewView struct {
	SelectionNotice string                      `json:"selectionNotice,omitempty"`
	BatchID         string                      `json:"batchId"`
	Fingerprint     string                      `json:"fingerprint"`
	Budget          animepreview.Budget         `json:"budget"`
	Sources         []animepreview.Summary      `json:"sources"`
	Issues          []service.AnimePreviewIssue `json:"issues"`
	Page            *animepreview.Page          `json:"page,omitempty"`
	DataVersion     string                      `json:"dataVersion"`
}

func animePreviewResponse(c *gin.Context, v service.AnimePreviewResult, ref string) {
	out := animePreviewView{Budget: v.Budget, Sources: v.Sources, Issues: v.Issues, SelectionNotice: v.SelectionNotice}
	if v.Batch != nil {
		out.BatchID, out.Fingerprint = v.Batch.ID, v.Batch.Fingerprint
		for _, source := range v.Batch.Sources {
			if source.Ref == ref {
				p, err := source.Releases(1, 50, 0)
				if err != nil {
					animeError(c, err)
					return
				}
				for _, summary := range v.Sources {
					if summary.SourceRef == ref {
						p.Summary = summary
					}
				}
				out.Page = &p
			}
		}
	}
	version, err := animepreview.Digest(out)
	if err != nil {
		animeError(c, err)
		return
	}
	out.DataVersion = version
	c.JSON(200, out)
}

func registerAnimePreviews(r *gin.RouterGroup, s *service.AnimeAdminService) {
	r.POST("/subscriptions/preview", func(c *gin.Context) {
		var in service.AnimePreviewInput
		if !animeBody(c, &in, true) {
			return
		}
		for _, source := range in.Config.Sources {
			if source.Status == "enabled" {
				animeError(c, storage.ErrAnimeCapability)
				return
			}
		}
		v, err := s.Previews.Generate(c.Request.Context(), animeActor(c), in)
		if err != nil {
			animeError(c, err)
			return
		}
		animePreviewResponse(c, v, in.SourceRef)
	})
	r.POST("/previews/:id/load", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		var in struct {
			SourceRef            string `json:"sourceRef"`
			SourcePreviewID      string `json:"sourcePreviewId"`
			ExpectedLoadRevision int64  `json:"expectedLoadRevision"`
			NextTake             int    `json:"nextTake"`
		}
		if !animeBody(c, &in, false) {
			return
		}
		if !validAnimePreviewRef(in.SourceRef, in.SourcePreviewID, in.ExpectedLoadRevision) {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		v, err := s.Previews.Load(c.Request.Context(), animeActor(c), storage.AnimePreviewRef{BatchID: id, SourceRef: in.SourceRef, PreviewID: in.SourcePreviewID, LoadRevision: in.ExpectedLoadRevision}, in.NextTake)
		if err != nil {
			animeError(c, err)
			return
		}
		animePreviewResponse(c, v, in.SourceRef)
	})
	r.GET("/previews/:id/releases", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		page, size, ok := animePage(c)
		if !ok {
			return
		}
		ref := c.Query("sourceRef")
		if !model.ValidAnimeID(ref) {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		v, err := s.Previews.Releases(c.Request.Context(), animeActor(c), id, ref, page, size)
		if err != nil {
			animeError(c, err)
			return
		}
		version, err := animepreview.Digest(v)
		if err != nil {
			animeError(c, err)
			return
		}
		c.JSON(200, struct {
			animepreview.Page
			DataVersion string `json:"dataVersion"`
		}{v, version})
	})
	// Selections only alter the preview snapshot, never release decisions.
	r.POST("/previews/:id/selections", func(c *gin.Context) {
		id, ok := animeID(c)
		if !ok {
			return
		}
		var in struct {
			SourceRef            string   `json:"sourceRef"`
			SourcePreviewID      string   `json:"sourcePreviewId"`
			ExpectedLoadRevision int64    `json:"expectedLoadRevision"`
			SelectedReleaseIDs   []string `json:"selectedReleaseIds"`
			DismissedReleaseIDs  []string `json:"dismissedReleaseIds"`
		}
		if !animeBody(c, &in, true) {
			return
		}
		if !validAnimePreviewRef(in.SourceRef, in.SourcePreviewID, in.ExpectedLoadRevision) || in.SelectedReleaseIDs == nil || in.DismissedReleaseIDs == nil {
			animeError(c, storage.ErrAnimeInvalid)
			return
		}
		b, err := s.Previews.Select(c.Request.Context(), animeActor(c), storage.AnimePreviewRef{BatchID: id, SourceRef: in.SourceRef, PreviewID: in.SourcePreviewID, LoadRevision: in.ExpectedLoadRevision}, in.SelectedReleaseIDs, in.DismissedReleaseIDs)
		if err != nil {
			animeError(c, err)
			return
		}
		v := service.AnimePreviewResult{Batch: &b, Budget: b.Budget(), Sources: []animepreview.Summary{}, Issues: []service.AnimePreviewIssue{}}
		for _, source := range b.Sources {
			v.Sources = append(v.Sources, source.Summary(time.Now().UTC().UnixMilli()))
		}
		animePreviewResponse(c, v, in.SourceRef)
	})
}
func validAnimePreviewRef(ref, id string, rev int64) bool {
	return model.ValidAnimeID(ref) && model.ValidAnimeID(id) && rev > 0 && rev <= model.AnimeMaxRevision
}
