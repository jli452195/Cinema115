package service

import (
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/model"
)

type ResourceSearchSpec = mediafacts.SearchSpec

func resourceSearchSpec(title string) ResourceSearchSpec { return mediafacts.Search(title) }
func resourceSpecification(title string) model.AnimeSpec { return mediafacts.Specification(title) }

func mikanResourceSpecs(items []mikan.Item) map[string]ResourceSearchSpec {
	specs := make(map[string]ResourceSearchSpec, len(items))
	for _, item := range items {
		specs[item.ID] = resourceSearchSpec(item.Title)
	}
	return specs
}
