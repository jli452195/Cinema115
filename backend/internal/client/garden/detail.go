package garden

import (
	"encoding/json"
	"path"
	"strings"
	"unicode"

	"strm115/backend/internal/client/animefeed"
)

type DetailFile struct {
	Name string `json:"name"`
	Size string `json:"size"` // Upstream display size, never used as exact byte identity.
}

type ResourceDetail struct {
	Status   string   `json:"status"`
	Resource Resource `json:"resource"`
	Detail   struct {
		Files        []DetailFile `json:"files"`
		HasMoreFiles bool         `json:"hasMoreFiles"`
	} `json:"detail"`
	IsDeleted bool `json:"isDeleted"`
}

func DetailPath(provider, id string) (string, error) {
	if originNames[provider] == "" || !originID.MatchString(id) {
		return "", animefeed.ErrInput
	}
	return "/detail/" + provider + "/" + id, nil
}

func ParseResourceDetail(body []byte, provider, id string) (ResourceDetail, error) {
	var out ResourceDetail
	if json.Unmarshal(body, &out) != nil || out.Status != "OK" || out.Resource.Provider != provider || out.Resource.ProviderID != id || len(out.Detail.Files) > 5000 {
		return out, ErrInvalid
	}
	seen := make(map[string]bool)
	for _, f := range out.Detail.Files {
		if f.Name == "" || len(f.Name) > 4096 || strings.HasPrefix(f.Name, "/") || strings.ContainsAny(f.Name, "\\:") || strings.ContainsFunc(f.Name, unicode.IsControl) || path.Clean(f.Name) != f.Name || f.Name == "." || f.Name == ".." || strings.HasPrefix(f.Name, "../") || seen[f.Name] {
			return out, ErrInvalid
		}
		seen[f.Name] = true
	}
	return out, nil
}
