package service

import "context"

type organizeRecycleDrive interface {
	DeleteEntries(context.Context, []string, string) error
}

type organizeRecycleBatchDrive interface {
	DeleteEntriesBatch(context.Context, string, []string, string) error
}

const MaxSubtitleImportBytes = 64 << 20

type subtitleUploader interface {
	UploadSubtitle(context.Context, string, string, []byte, string) error
}
