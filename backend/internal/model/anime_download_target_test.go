package model

import "testing"

func TestAnimeDownloadTargetValidation(t *testing.T) {
	valid := AnimeDownloadTarget{SchemaVersion: 1, SettingsRevision: 2, AccountID: "main-account", ParentCID: "456", ParentPath: "/动漫/本季"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*AnimeDownloadTarget)
	}{
		{"schema", func(v *AnimeDownloadTarget) { v.SchemaVersion = 0 }},
		{"settings version", func(v *AnimeDownloadTarget) { v.SettingsRevision = 0 }},
		{"account ID", func(v *AnimeDownloadTarget) { v.AccountID = "" }},
		{"CID", func(v *AnimeDownloadTarget) { v.ParentCID = "" }},
		{"CID leading zero", func(v *AnimeDownloadTarget) { v.ParentCID = "01" }},
		{"relative path", func(v *AnimeDownloadTarget) { v.ParentPath = "动漫" }},
		{"empty path", func(v *AnimeDownloadTarget) { v.ParentPath = "" }},
		{"nonroot path", func(v *AnimeDownloadTarget) { v.ParentPath = "/" }},
		{"root identity", func(v *AnimeDownloadTarget) { v.ParentCID = "0" }},
		{"control", func(v *AnimeDownloadTarget) { v.ParentPath = "/动漫\n" }},
		{"backslash", func(v *AnimeDownloadTarget) { v.ParentPath = "/动漫\\本季" }},
		{"traversal", func(v *AnimeDownloadTarget) { v.ParentPath = "/动漫/../本季" }},
		{"empty segment", func(v *AnimeDownloadTarget) { v.ParentPath = "/动漫//本季" }},
		{"invalid UTF8", func(v *AnimeDownloadTarget) { v.ParentPath = "/\xff" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := valid
			tc.edit(&v)
			if err := v.Validate(); err == nil {
				t.Fatal("accepted invalid target")
			}
		})
	}
	valid.ParentCID, valid.ParentPath = "0", "/"
	if err := valid.Validate(); err != nil {
		t.Fatal("explicit root rejected", err)
	}
}
