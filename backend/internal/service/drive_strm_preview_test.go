package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/model"
)

func TestDriveSTRMPreviewReconciliationSkipsAndOriginalModeQueuesReplacement(t *testing.T) {
	s, db, client, mapping, root := driveSTRMTestSetup(t)
	ctx := context.Background()
	file := driveSTRMTestPoster()
	file.Size = 1024
	client.files["100"] = []p115.ShareFile{file}
	preview := []byte("compressed-preview")
	if err := os.MkdirAll(filepath.Join(root, "strm"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "strm", file.Name), preview, 0644); err != nil {
		t.Fatal(err)
	}
	record := model.DriveSTRMFile{MappingID: mapping.ID, FileID: file.ID, ParentCID: "100", Path: file.Name, Signature: driveSTRMSignature(file), Digest: driveSTRMDigest(preview), MetadataPreview: true, MetadataSize: int64(len(preview))}
	if err := db.SaveDriveSTRMFile(ctx, record); err != nil {
		t.Fatal(err)
	}
	stats, err := driveSTRMTestAttempt(t, s)
	if err != nil || stats.Unchanged != 1 {
		t.Fatal("压缩预览不应按原文件大小重复下载", stats, err)
	}
	counts, err := db.DriveSTRMAttachmentCounts(ctx)
	if err != nil || counts[mapping.ID][0] != 0 || client.metadataCalls != 0 {
		t.Fatal("未变化预览产生了下载请求", counts, err)
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.MetadataImageMode = "original"
	if _, err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(ctx, mapping.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	counts, err = db.DriveSTRMAttachmentCounts(ctx)
	if err != nil || counts[mapping.ID][0] != 1 {
		t.Fatal("原文件策略错误沿用了压缩预览", counts, err)
	}
}

func TestDriveSTRMPreviewRenameReusesLocalBytesWithoutRequests(t *testing.T) {
	s, db, client, mapping, root := driveSTRMTestSetup(t)
	ctx := context.Background()
	file := driveSTRMTestPoster()
	file.Size = 1024
	preview := []byte("compressed-preview")
	directory := filepath.Join(root, "strm")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, file.Name), preview, 0644); err != nil {
		t.Fatal(err)
	}
	record := model.DriveSTRMFile{MappingID: mapping.ID, FileID: file.ID, ParentCID: "100", Path: file.Name, Signature: driveSTRMSignature(file), Digest: driveSTRMDigest(preview), MetadataPreview: true, MetadataSize: int64(len(preview))}
	if err := db.SaveDriveSTRMFile(ctx, record); err != nil {
		t.Fatal(err)
	}
	file.Name = "renamed-poster.jpg"
	client.files["100"] = []p115.ShareFile{file}
	if _, err := driveSTRMTestAttempt(t, s); err != nil {
		t.Fatal(err)
	}
	driveSTRMTestDrainAttachments(t, s)
	data, err := os.ReadFile(filepath.Join(directory, file.Name))
	if err != nil || string(data) != string(preview) || client.metadataCalls != 0 {
		t.Fatal("预览改名未复用已核验字节", len(data), client.metadataCalls, err)
	}
	records, err := db.ListDriveSTRMFilesByIDs(ctx, mapping.ID, []string{file.ID})
	if err != nil || len(records) != 1 || !records[0].MetadataPreview || records[0].MetadataSize != int64(len(preview)) || records[0].Signature != record.Signature {
		t.Fatal("改名丢失预览与原文件的身份区别", records, err)
	}
}
