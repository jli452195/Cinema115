package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/clouddrive2"
	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/naming"
)

func (s *MediaOrganizer) process(ctx context.Context, run *MediaOrganizeRun, action string) (processErr error) {
	if run.RecognitionVersion == "" {
		run.RecognitionVersion = MediaRecognitionPolicyVersion
	}
	if run.RecognitionVersion != MediaRecognitionPolicyVersion {
		return fmt.Errorf("整理识别策略版本不可用，请重新扫描来源目录")
	}
	if err := run.prepareClassification(); err != nil {
		return err
	}
	files, err := s.files(ctx, run.Rule)
	if err != nil {
		return err
	}
	defer files.close()
	if action == "auto" {
		defer func() {
			if err := ctx.Err(); err != nil {
				processErr = errors.Join(processErr, err)
				return
			}
			// 收尾检查点不能提前发布终态，目录清理结束后再一次性公布完成结果。
			status := run.Status
			run.Status = "running"
			processErr = errors.Join(processErr, s.cleanupEmptyDirectories(ctx, files, run))
			if processErr == nil {
				run.Status = status
				processErr = s.checkpoint(run)
			}
		}()
	}
	s.bindCD2Checkpoint(files, run)
	if action == "auto" && run.Rule.Storage == "cd2" {
		return s.processCD2Auto(ctx, files, run)
	}
	if action == "auto" && run.Rule.Storage == "115" {
		if _, supported := files.drive.(media115DirectoryDrive); supported {
			return s.process115Auto(ctx, files, run)
		}
	}
	if action == "auto" {
		if err = s.scan(ctx, files, run); err != nil {
			return err
		}
		mediaDiscardOrphanSidecars(files, run, files.scanned)
		if err = files.recheckLocalCompanions(ctx, run); err != nil {
			return err
		}
		if err = s.prepareOrphanCleanup(files, run); err != nil {
			return err
		}
		if run.Rule.Storage == "local" {
			s.localScanSucceeded(run.Rule)
		}
	} else if action == "retry" {
		if err = s.recognize(ctx, run, nil); err != nil {
			return err
		}
		if err = s.render(ctx, files, run); err != nil {
			return err
		}
	}
	if err = s.execute(ctx, files, run); err != nil {
		return err
	}
	if action == "auto" && len(files.unrelated) > 0 {
		status, message, count := run.Status, run.Message, len(run.Entries)
		run.Status = "running"
		if err = s.cleanup(ctx, files, run); err != nil {
			return err
		}
		run.Status, run.Message = status, message
		cleaned := 0
		for _, e := range run.Entries[count:] {
			if e.Status == "done" {
				cleaned++
			}
		}
		if cleaned > 0 {
			run.Message = fmt.Sprintf("已清理 %d 项（无关文件、未归属字幕与附件及空文件夹）", cleaned)
			if count > 0 {
				run.Message = message + "；" + run.Message
			}
		}
		for _, e := range run.Entries[count:] {
			if e.Result != nil && e.Result.Status == "error" {
				run.Status = "partial"
			}
		}
	}
	if action == "auto" {
		return nil
	}
	return s.checkpoint(run)
}

func (s *MediaOrganizer) scan(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	var err error
	run.Status = "scanning"
	if err = s.checkpoint(run); err != nil {
		return err
	}
	entries, err := files.scan(ctx)
	if err != nil {
		if run.Rule.Storage == "local" {
			return &mediaLocalScanError{err}
		}
		return err
	}
	files.scanned = entries
	mediaAppendScanEntries(run, entries)
	if err = s.recognize(ctx, run, nil); err != nil {
		return err
	}
	if err = s.render(ctx, files, run); err != nil {
		return err
	}
	return nil
}

func mediaGroupIdentityKey(identity mediafacts.Identity) mediafacts.Identity {
	identity.Name, identity.ParentName = mediafacts.NameKey(identity.Name), mediafacts.NameKey(identity.ParentName)
	if identity.ParentName == identity.Name {
		identity.ParentName = ""
	}
	identity.AlternativeName = mediafacts.NameKey(identity.AlternativeName)
	identity.NameSource, identity.YearSource, identity.KindSource = "", "", ""
	identity.TMDBIDSource, identity.SeasonSource, identity.ContentTypeSource = "", "", ""
	return identity
}

func mediaAppendScanEntries(run *MediaOrganizeRun, entries []MediaOrganizeEntry) {
	indices := make(map[mediafacts.Identity]int)
	identities, episodeContexts := mediaScanIdentities(run, entries)
	for i, group := range run.Groups {
		indices[mediaGroupIdentityKey(group.Identity)] = i
	}
	for _, e := range entries {
		if e.File.MediaType != "video" {
			run.Entries = append(run.Entries, e)
			continue
		}
		identity := identities[e.File.ID]
		if e.File.Episode > 0 {
			// 片名仍保留尾部数字时，该数字只是弱编号，不能把电影强制改为剧集。
			if identity.Kind == "" && mediaIdentityEndsWithEpisode(identity.Name, e.File.Episode) {
				base := mediaIdentityEpisodeBase(identity.Name)
				if episodeContexts[e.File.ParentID+":"+mediafacts.NameKey(base)] {
					identity.Name, identity.NameSource = base, "directory:numbering"
					identity.Kind, identity.KindSource = "tv", "directory:numbering"
				} else {
					identity.Name = mediaIdentityNumberedTitle(identity.Name)
					e.File.Episode, e.File.EndEpisode = 0, 0
				}
			} else if identity.Kind != "movie" {
				identity.Kind = "tv"
				if identity.KindSource == "" {
					identity.KindSource = "filename:numbering"
				}
			}
			if e.File.Season == 0 {
				identity.Season, identity.SeasonExplicit = 0, true
			}
			if e.File.Season != 0 && !organizeFileSeason.MatchString(e.File.Name) {
				e.File.Season = identity.Season
			}
		}
		key := mediaGroupIdentityKey(identity)
		if e.File.Episode == 0 && identity.Kind == "tv" && organizeSpecialFile.MatchString(e.File.Name) {
			key.Name += ":special"
		}
		if identity.Name == "" && identity.TMDBID == 0 {
			key.Name = e.File.ParentID
		}
		if _, ok := indices[key]; !ok {
			indices[key] = len(run.Groups)
			run.Groups = append(run.Groups, MediaOrganizeGroup{ID: strconv.Itoa(len(run.Groups) + 1), Identity: identity})
		}
		e.Group = run.Groups[indices[key]].ID
		run.Entries = append(run.Entries, e)
	}
}

func (s *MediaOrganizer) recognize(ctx context.Context, run *MediaOrganizeRun, groups []int) error {
	byGroup := make(map[string][]int, len(run.Groups))
	for i, entry := range run.Entries {
		if entry.File.MediaType == "video" && entry.Status != "done" && mediaInRunTask(run, entry) {
			byGroup[entry.Group] = append(byGroup[entry.Group], i)
		}
	}
	if groups == nil {
		groups = make([]int, len(run.Groups))
		for i := range groups {
			groups[i] = i
		}
	}
	for _, i := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i < 0 || i >= len(run.Groups) {
			return ErrMediaOrganizeInput
		}
		g := &run.Groups[i]
		indices := byGroup[g.ID]
		if len(indices) == 0 {
			continue
		}
		if g.TMDBID > 0 {
			continue
		}
		g.ResultCode = ""
		numbered, special := false, false
		for _, index := range indices {
			e := &run.Entries[index]
			numbered = numbered || e.File.Episode > 0
			special = special || organizeSpecialFile.MatchString(e.File.Name)
		}
		if special && !numbered && g.Identity.Kind == "tv" {
			g.Message = "特殊内容需确认对应作品与季集"
			g.ResultCode = "NUMBERING_MISSING"
			continue
		}
		lookup, cancel := context.WithTimeout(ctx, 25*time.Second)
		match, decision, err := recognizeMediaWithDecision(lookup, s.metadata, g.Identity)
		cancel()
		g.ResultCode = decision.ResultCode
		if err != nil {
			g.Message = "查询暂未完成：" + persistentLogText(err.Error())
			continue
		}
		if match == nil {
			g.Message = decision.Message
			if g.Message == "" {
				g.Message = "未找到合适匹配，请选择作品"
			}
			continue
		}
		g.Kind, g.TMDBID, g.Title = match.Detail.Kind, match.Detail.ID, match.Detail.Title
		g.ResultCode = "MATCHED"
		g.Year = mediaRecognitionYear(match.Detail.Date)
		g.Message = ""
		g.MatchedSeason = mediaOptionalNumber(&match.Season)
		if g.Kind == "tv" {
			for _, index := range indices {
				run.Entries[index].File.Season = match.Season
			}
		}
	}
	return nil
}

func (s *MediaOrganizer) render(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	prepared := make(map[string]preparedMediaNaming)
	if files.naming != nil {
		prepared = files.naming
	}
	selectedGroups := make(map[string]bool)
	for _, e := range run.Entries {
		if e.File.MediaType == "video" && e.Status != "done" && e.P115Move == nil && mediaInRunTask(run, e) {
			selectedGroups[e.Group] = true
		}
	}
	for i := range run.Groups {
		g := &run.Groups[i]
		if g.TMDBID < 1 || !selectedGroups[g.ID] {
			continue
		}
		p, cached := prepared[g.ID]
		var err error
		if !cached {
			// 不同来源文件夹可形成独立识别分组，已确认的同一作品仍复用命名资料。
			workKey := "work:" + g.Kind + ":" + strconv.Itoa(g.TMDBID)
			p, cached = prepared[workKey]
			if !cached {
				p, err = (MediaNaming{Templates: s.templates, Metadata: s.metadata}).prepare(ctx, g.Kind, g.TMDBID, nil)
				if err == nil {
					prepared[workKey] = p
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			g.Message = "查询暂未完成：" + err.Error()
			g.ResultCode = "METADATA_QUERY_FAILED"
			continue
		}
		prepared[g.ID] = p
		g.Title = p.detail.Title
		g.Year = mediaRecognitionYear(p.detail.Date)
		g.Category = ""
		g.Message = ""
		g.ResultCode = "MATCHED"
		if run.Rule.Classify {
			category, err := run.Classification.Classifier.Classify(mediaClassificationFacts(p.detail))
			if err != nil {
				return fmt.Errorf("计算整理分类: %w", err)
			}
			g.Category = category.Path
		}
	}
	results := make(map[string]naming.Result)
	for _, e := range run.Entries {
		if e.File.MediaType == "video" && e.Relative != "" {
			name := path.Base(e.Relative)
			results[e.File.ID] = naming.Result{Directory: path.Dir(e.Relative), Name: name, Stem: strings.TrimSuffix(name, path.Ext(name)), Extension: path.Ext(name), RelativePath: e.Relative}
		}
	}
	for i := range run.Entries {
		e := &run.Entries[i]
		if e.File.MediaType == "cleanup" || !mediaInRunTask(run, *e) {
			continue
		}
		// 未核实的外部写入继续使用持久化原计划，不能因模板或元数据变化改写目标。
		if e.P115Move != nil || e.AttachmentKeep != nil {
			continue
		}
		if e.File.MediaType != "video" && e.Status != "done" {
			if run.TaskFileID == "" {
				e.Group, e.VideoID = "", ""
			}
			e.Relative, e.Target, e.ResultCode = "", "", ""
			e.Status, e.Message = "unmatched", "未找到唯一对应的视频，保留来源"
		}
		if e.Status == "done" && e.File.MediaType == "video" {
			name := path.Base(e.Relative)
			results[e.File.ID] = naming.Result{Directory: path.Dir(e.Relative), Name: name, Stem: strings.TrimSuffix(name, path.Ext(name)), Extension: path.Ext(name), RelativePath: e.Relative}
			continue
		}
		if e.File.MediaType != "video" {
			continue
		}
		delete(results, e.File.ID)
		e.Relative, e.Target, e.Message, e.ResultCode = "", "", "", ""
		p, ok := prepared[e.Group]
		if !ok {
			e.Status = "needs_match"
			e.Message = "请选择对应作品"
			for _, group := range run.Groups {
				if group.ID == e.Group && group.ResultCode == "NUMBERING_MISSING" {
					if code, message := mediaOrganizeNumberingError(e.File); code != "" {
						e.ResultCode, e.Message = code, message
						if code == "NUMBERING_CONFLICT" || code == "NUMBERING_INVALID" {
							e.Status = "invalid"
						}
					}
					break
				}
			}
			continue
		}
		for _, group := range run.Groups {
			if group.ID == e.Group && group.MatchedSeason != nil && group.Kind == "tv" {
				e.File.Season = *group.MatchedSeason
				break
			}
		}
		result, err := p.render(MediaNamingFile{OrganizeSelection: OrganizeSelection{ID: e.File.ID, Season: e.File.Season, Episode: e.File.Episode}, Name: e.File.Name, NamingValues: e.File.NamingValues})
		if err != nil {
			e.Status = "invalid"
			e.Message = err.Error()
			e.ResultCode = "NAMING_FAILED"
			if p.snapshot.Template.Kind == naming.TV {
				if code, message := mediaOrganizeNumberingError(e.File); code != "" {
					e.ResultCode, e.Message = code, message
				}
			}
			continue
		}
		for _, g := range run.Groups {
			if g.ID == e.Group && g.Category != "" {
				result.Directory = path.Join(g.Category, result.Directory)
				result.RelativePath = path.Join(g.Category, result.RelativePath)
				break
			}
		}
		results[e.File.ID] = result
		e.Relative = result.RelativePath
		e.Target = path.Join(run.Rule.Target, e.Relative)
		e.Status = "ready"
	}
	original := make([]OrganizeFile, 0, len(run.Entries))
	positions := make(map[string]int, len(run.Entries))
	for i, e := range run.Entries {
		original = append(original, e.File)
		positions[e.File.ID] = i
	}
	for _, sub := range organizeSubtitles(mediaAttachmentMatchFiles(original)) {
		i := positions[sub.File.ID]
		e := &run.Entries[i]
		if e.Status == "done" || e.P115Move != nil || e.AttachmentKeep != nil || !mediaInRunTask(run, *e) {
			continue
		}
		video, ok := results[sub.VideoID]
		if sub.VideoID != "" {
			e.Group = run.Entries[positions[sub.VideoID]].Group
			e.VideoID = sub.VideoID
		}
		if !ok {
			if sub.VideoID != "" {
				e.Message = "对应视频尚未就绪，字幕保留来源"
				e.ResultCode = "WAITING_MAIN_FILE"
			}
			continue
		}
		result, err := naming.Companion(video, sub.Tail, sub.File.Name)
		if err != nil {
			e.Status = "invalid"
			e.Message = err.Error()
			continue
		}
		e.Group = run.Entries[positions[sub.VideoID]].Group
		e.Relative = result.RelativePath
		e.Target = path.Join(run.Rule.Target, e.Relative)
		e.Status = "ready"
		e.Message = ""
	}
	mediaOrganizeAttachments(run, results)
	mediaPlanAttachmentChoices(run)
	duplicates := make(map[string][]int)
	for i, e := range run.Entries {
		if e.Status != "ready" || e.P115Move != nil || e.AttachmentKeep != nil {
			continue
		}
		key := strings.ToLower(e.Relative)
		duplicates[key] = append(duplicates[key], i)
	}
	for _, indices := range duplicates {
		if len(indices) > 1 && !((run.Rule.Storage == "cd2" || run.Rule.Storage == "115") && run.Rule.DeleteDuplicates) {
			names := make([]string, 0, min(len(indices), 4))
			for _, index := range indices[:min(len(indices), 4)] {
				names = append(names, run.Entries[index].File.Name)
			}
			for _, i := range indices {
				if !mediaInRunTask(run, run.Entries[i]) {
					continue
				}
				run.Entries[i].Status = "conflict"
				run.Entries[i].ResultCode = "BATCH_TARGET_CONFLICT"
				run.Entries[i].Message = fmt.Sprintf("本批次 %d 个输入生成相同目标 %s：%s，请检查命名或作品季集", len(indices), run.Entries[i].Target, strings.Join(names, "、"))
			}
		}
	}
	for i := range run.Entries {
		e := &run.Entries[i]
		if e.Status != "ready" || e.P115Move != nil || e.AttachmentKeep != nil || !mediaInRunTask(run, *e) {
			continue
		}
		if err := files.conflict(ctx, *e); err != nil {
			e.Status = "error"
			if errors.Is(err, errMediaTargetExists) {
				e.Status = "conflict"
				e.ResultCode = "TARGET_EXISTS"
			}
			e.Message = err.Error()
		}
	}
	sort.SliceStable(run.Entries, func(i, j int) bool {
		if run.Entries[i].File.MediaType != run.Entries[j].File.MediaType {
			return run.Entries[i].File.MediaType == "video"
		}
		return run.Entries[i].File.SourcePath < run.Entries[j].File.SourcePath
	})
	return nil
}

func (s *MediaOrganizer) execute(ctx context.Context, files *mediaOrganizeFiles, run *MediaOrganizeRun) error {
	if run.Rule.Storage == "cd2" && !run.streaming {
		ctx = clouddrive2.WithDirectoryCache(ctx)
	}
	mediaValidateAttachmentOwners(run, mediaEntryFiles(run.Entries))
	if err := s.prepareAttachmentChoices(ctx, files, run); err != nil {
		return err
	}
	// 已选附件统一留到本轮末尾；普通路径中新发现的失败计划不在同轮重试。
	pendingAttachments := make(map[string]bool, len(run.Entries))
	for _, entry := range run.Entries {
		if entry.AttachmentKeep != nil {
			pendingAttachments[entry.File.ID] = true
		}
	}
	run.Status = "running"
	if err := s.checkpoint(run); err != nil {
		return err
	}
	if err := s.quarantine(ctx, files, run, true); err != nil {
		return err
	}
	failures := mediaFailureEntries(run)
	if bulk, ok := files.cd2.(clouddrive2.BatchFilesystem); run.Rule.Storage == "cd2" && ok {
		if err := s.executeCD2Batches(ctx, files, run, bulk, failures); err != nil {
			return err
		}
	} else if drive, ok := files.drive.(media115DirectoryDrive); run.Rule.Storage == "115" && ok {
		if err := s.execute115Batches(ctx, files, run, drive, failures); err != nil {
			return err
		}
		s.tryCleanup115EmptyDirectories(ctx, files, run)
	} else {
		for i := range run.Entries {
			if failures[run.Entries[i].File.ID] || run.Entries[i].AttachmentKeep != nil {
				continue
			}
			if err := s.executeMediaEntry(ctx, files, run, i); err != nil {
				return err
			}
		}
	}
	if err := s.executePendingAttachmentChoices(ctx, files, run, pendingAttachments); err != nil {
		return err
	}
	if err := s.quarantine(ctx, files, run, false); err != nil {
		return err
	}
	if run.streaming {
		return nil
	}
	mediaFinishRun(run)
	return nil
}

func mediaFinishRun(run *MediaOrganizeRun) {
	run.Status = "completed"
	run.Message = "整理完成"
	for i := range run.Entries {
		e := &run.Entries[i]
		if mediaInRunTask(run, *e) && e.Status != "done" && e.Status != "retained" {
			target := e.Target
			if e.Failure != nil && e.Failure.State == "stored" {
				target = path.Join(run.Rule.Failure, e.File.SourcePath)
			}
			if e.Result == nil || e.Result.Status != "error" || e.Result.Message != e.Message || e.Result.Target != target {
				e.Result = &MediaOrganizeResult{Status: "error", Message: e.Message, Target: target, At: time.Now()}
			}
		}
	}
	for _, e := range run.Entries {
		if e.Status != "done" && e.Status != "retained" {
			run.Status = "partial"
			run.Message = "已处理可整理文件，其余条目请核对"
			break
		}
	}
	if len(run.Entries) == 0 {
		run.Message = "没有新的可处理文件；新写入文件稳定 30 秒后再扫描"
		if run.Rule.Storage == "cd2" || run.Rule.Storage == "115" {
			run.Message = "没有新的可处理文件"
		}
	}
}
