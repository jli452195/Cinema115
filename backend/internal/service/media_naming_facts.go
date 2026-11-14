package service

import (
	"context"
	"path"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/recognition"
)

type namingRecognitionContextKey struct{}

// namingRecognition 在预览或任务边界固定词表，后续配置变化不影响同批次文件。
func namingRecognition(ctx context.Context) recognition.Snapshot {
	if snapshot, ok := ctx.Value(namingRecognitionContextKey{}).(recognition.Snapshot); ok {
		return snapshot
	}
	return recognition.Current()
}

func pinNamingRecognition(ctx context.Context, snapshot recognition.Snapshot) context.Context {
	return context.WithValue(ctx, namingRecognitionContextKey{}, snapshot)
}

// Only an already-enumerated directory containing one video may supply missing
// subtitle facts. Never copy a pack's labels to several videos or search ancestors.
func mediaDirectorySubtitleValues(files []OrganizeFile, rules recognition.Snapshot) error {
	counts := make(map[string]int, len(files))
	for _, file := range files {
		if file.MediaType == "video" {
			counts[file.ParentID]++
		}
	}
	for i := range files {
		file := &files[i]
		if file.MediaType != "video" || file.TitleOnly || counts[file.ParentID] != 1 {
			continue
		}
		if file.NamingValues == nil {
			values, err := mediafacts.ValuesWithRecognition(file.Name, DownloadFileSpec{}.Spec, rules)
			if err != nil {
				return err
			}
			file.NamingValues = values
		}
		mediafacts.InheritSubtitles(file.NamingValues, path.Base(path.Dir(file.SourcePath)))
	}
	return nil
}
