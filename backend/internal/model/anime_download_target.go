package model

import (
	"fmt"
	"strings"
	"unicode"
)

// AnimeDownloadTarget is the credential-free, value-only part of a download
// task snapshot. A directory is identified by account/CID, never its name.
// Organization has a separate execution snapshot in P3.
type AnimeDownloadTarget struct {
	SchemaVersion    int    `json:"schemaVersion"`
	SettingsRevision int64  `json:"settingsRevision"`
	AccountID        string `json:"accountId"`
	ParentCID        string `json:"parentCid"`
	ParentPath       string `json:"parentPath"`
}

func (v AnimeDownloadTarget) Validate() error {
	if v.SchemaVersion != AnimeSchemaVersion || v.SettingsRevision < 1 || v.SettingsRevision > AnimeMaxRevision {
		return fmt.Errorf("invalid download target schema/revision")
	}
	if !ValidAnimeID(v.AccountID) || v.ParentCID != "0" && !ValidAnimeExternalID(v.ParentCID) {
		return fmt.Errorf("invalid download target identity")
	}
	if !animeText(v.ParentPath, 16384, true) || !strings.HasPrefix(v.ParentPath, "/") || strings.ContainsAny(v.ParentPath, "\\") || strings.ContainsFunc(v.ParentPath, unicode.IsControl) {
		return fmt.Errorf("invalid download target path")
	}
	if v.ParentCID == "0" {
		if v.ParentPath != "/" {
			return fmt.Errorf("invalid download root path")
		}
		return nil
	}
	for _, part := range strings.Split(v.ParentPath[1:], "/") {
		if strings.TrimSpace(part) == "" || part == "." || part == ".." {
			return fmt.Errorf("invalid download target path segment")
		}
	}
	return nil
}
