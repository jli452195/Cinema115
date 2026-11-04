package service

import (
	"context"
	"fmt"
	"path"
	"strings"

	"strm115/backend/internal/client/p115"
)

func media115SameSHA1(e MediaOrganizeEntry, target p115.ShareFile) bool {
	const digits = "0123456789abcdefABCDEF"
	return !e.File.Directory && !target.Directory && e.File.ID != "" && target.ID != "" && e.File.ID != target.ID &&
		len(e.File.SHA1) == 40 && strings.Trim(e.File.SHA1, digits) == "" &&
		len(target.SHA1) == 40 && strings.Trim(target.SHA1, digits) == "" && strings.EqualFold(e.File.SHA1, target.SHA1)
}

func (f *mediaOrganizeFiles) delete115Duplicate(ctx context.Context, e MediaOrganizeEntry, targetCID string, targetFile p115.ShareFile) error {
	if !f.rule.DeleteDuplicates || !media115SameSHA1(e, targetFile) || !strings.EqualFold(targetFile.Name, path.Base(e.Relative)) {
		return errMediaTargetExists
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	drive, ok := f.drive.(mediaOrganizeDrive)
	if !ok {
		return fmt.Errorf("115 目录核验服务不可用")
	}
	recycle, ok := f.drive.(organizeRecycleDrive)
	if !ok {
		return fmt.Errorf("115 回收站服务不可用")
	}
	for _, relative := range []string{e.File.SourcePath, e.Relative} {
		if relative == "" || path.IsAbs(relative) || path.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, "../") {
			return fmt.Errorf("115 去重文件路径无效，请重新扫描")
		}
	}
	for _, selected := range []struct{ cid, full string }{
		{e.File.ParentID, path.Join(f.rule.Source, path.Dir(e.File.SourcePath))},
		{targetCID, path.Join(f.rule.Target, path.Dir(e.Relative))},
	} {
		dir, err := drive.ReadDirectory(ctx, selected.cid, f.cookie)
		if err != nil {
			return fmt.Errorf("核验 115 去重目录: %w", err)
		}
		if selected.cid == "" || dir.CID != selected.cid || dir.Path != selected.full {
			return fmt.Errorf("115 去重目录已变化，请重新扫描")
		}
	}
	// 删除前刷新双方身份；判重仅使用完整 SHA1，大小只核对来源是否仍为扫描时的文件。
	delete(f.lists, e.File.ParentID)
	delete(f.lists, targetCID)
	source, err := f.list(ctx, e.File.ParentID)
	if err != nil {
		return fmt.Errorf("核验 115 去重来源: %w", err)
	}
	var actual *p115.ShareFile
	for i := range source {
		if source[i].ID == e.File.ID {
			actual = &source[i]
			break
		}
	}
	recovering := actual == nil && e.Failure != nil && e.Failure.State == "moving" && f.rule.Target == f.rule.Failure && f.rule.TargetID == f.rule.FailureID
	if actual == nil && !recovering || actual != nil && (actual.Directory || actual.Size != e.File.Size ||
		(actual.Name != e.File.Name && actual.Name != path.Base(e.Relative)) || !strings.EqualFold(actual.SHA1, e.File.SHA1)) {
		return fmt.Errorf("115 来源文件已变化，请重新扫描")
	}
	target, err := f.list(ctx, targetCID)
	if err != nil {
		return fmt.Errorf("核验 115 去重目标: %w", err)
	}
	confirmed, names := false, 0
	for _, file := range target {
		if strings.EqualFold(file.Name, targetFile.Name) {
			names++
		}
		if file.ID == targetFile.ID && file.Name == targetFile.Name && file.Size == targetFile.Size && media115SameSHA1(e, file) {
			confirmed = true
		}
	}
	if !confirmed || recovering && names != 1 {
		return fmt.Errorf("115 目标文件已变化，保留来源")
	}
	if actual != nil {
		if err := recycle.DeleteEntries(ctx, []string{e.File.ID}, f.cookie); err != nil {
			delete(f.lists, e.File.ParentID)
			delete(f.lists, targetCID)
			return fmt.Errorf("115 来源重复文件回收结果待核实: %w", err)
		}
	}
	// 只核验本次回收结果；请求失败或结果不明时不自动再次发起删除。
	delete(f.lists, e.File.ParentID)
	delete(f.lists, targetCID)
	source, err = f.list(ctx, e.File.ParentID)
	if err != nil {
		return fmt.Errorf("核验 115 重复文件回收结果: %w", err)
	}
	for _, file := range source {
		if file.ID == e.File.ID {
			return fmt.Errorf("115 来源重复文件仍存在，回收结果待核实")
		}
	}
	target, err = f.list(ctx, targetCID)
	if err != nil {
		return fmt.Errorf("核验 115 去重保留目标: %w", err)
	}
	for _, file := range target {
		if file.ID == targetFile.ID && file.Name == targetFile.Name && file.Size == targetFile.Size && media115SameSHA1(e, file) {
			return &mediaDuplicateReceipt{fileID: file.ID, parentID: targetCID, size: file.Size}
		}
	}
	return fmt.Errorf("115 去重保留目标无法确认，请核实文件")
}
