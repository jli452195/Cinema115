package service

import (
	"regexp"
	"strconv"
	"strings"

	"strm115/backend/internal/animeparse"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/model"
)

// Search-only hints never change RSS specification identity or download rules.
type ResourceSearchSpec struct {
	model.AnimeSpec
	Bitrate string `json:"bitrate,omitempty"`
}

// Only explicitly marked bit rates count. File size, resolution and MB/s
// (bytes per second) are not evidence of a video's bit rate.
var resourceBitrate = regexp.MustCompile(`(?:^|[^a-zA-Z0-9.])([0-9]{1,6}(?:\.[0-9]{1,3})?)\s*([mMkK])b(?:[pP][sS]|/[sS])\b`)

func resourceSearchSpec(title string) ResourceSearchSpec {
	spec := ResourceSearchSpec{AnimeSpec: animeparse.Specification(title)}
	rates := make(map[string]bool)
	for _, match := range resourceBitrate.FindAllStringSubmatch(title, -1) {
		rate, _ := strconv.ParseFloat(match[1], 64)
		if strings.EqualFold(match[2], "k") {
			rate /= 1000
		}
		if rate > 0 {
			rates[strconv.FormatFloat(rate, 'f', -1, 64)+" Mbps"] = true
		}
	}
	if len(rates) == 1 {
		for rate := range rates {
			spec.Bitrate = rate
		}
	}
	return spec
}

func mikanResourceSpecs(items []mikan.Item) map[string]ResourceSearchSpec {
	specs := make(map[string]ResourceSearchSpec, len(items))
	for _, item := range items {
		specs[item.ID] = resourceSearchSpec(item.Title)
	}
	return specs
}
