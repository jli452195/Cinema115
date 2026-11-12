package service

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/releaseparse"
)

var mediaIdentityNumberTail = regexp.MustCompile(`(?i)(?:^|[ ._-])([0-9]{1,4})(?:\s*v[0-9]{1,3})?(?:[ ._-]+(?:END|FIN|完结|完結|完))*[ ._-]*$`)
var mediaIdentityDescriptionTag = regexp.MustCompile(`\[(?:原创|原創|主线|主線)\]|【(?:原创|原創|主线|主線)】`)

// 描述词表只影响自动整理的作品身份；位于集号前的片名及方括号编号均保留。
func mediaOrganizeFilenameFacts(name string) mediafacts.FilenameFacts {
	facts := mediafacts.ParseFilename(name)
	stem := strings.TrimSuffix(name, path.Ext(name))
	matches := mediaIdentityDescriptionTag.FindAllStringIndex(stem, -1)
	if len(matches) == 0 {
		return facts
	}
	parsed, err := releaseparse.Title(stem)
	if err != nil || parsed.Kind != "episode" || parsed.Episode == nil {
		return facts
	}
	var cleaned strings.Builder
	start := 0
	for _, match := range matches {
		prefix, err := releaseparse.Title(stem[:match[0]])
		if err != nil || prefix.Kind != "episode" || prefix.Episode == nil || *prefix.Episode != *parsed.Episode {
			continue
		}
		cleaned.WriteString(stem[start:match[0]])
		start = match[1]
	}
	if start != 0 {
		cleaned.WriteString(stem[start:])
		// 坐标、特殊集及冲突仍使用原文事实，不因忽略描述标签改变编号。
		facts.Identity = mediafacts.ParseFilename(cleaned.String() + ".mediafacts").Identity
	}
	return facts
}

// 日志上下文只包含结果与定位字段，不保存候选或阶段历史。
type MediaRecognitionLogDetails struct {
	RunID      string `json:"runId"`
	GroupID    string `json:"groupId"`
	FileID     string `json:"fileId,omitempty"`
	Status     string `json:"status"`
	ResultCode string `json:"resultCode"`
	Name       string `json:"name,omitempty"`
	SourcePath string `json:"sourcePath,omitempty"`
	TargetPath string `json:"targetPath,omitempty"`
	WorkKey    string `json:"workKey,omitempty"`
	Season     *int   `json:"season,omitempty"`
	Episode    *int   `json:"episode,omitempty"`
	EndEpisode *int   `json:"endEpisode,omitempty"`
}

type mediaRecognitionLogEvent struct {
	details MediaRecognitionLogDetails
	level   string
	message string
}

func mediaOptionalNumber(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func mediaEntryWaiting(entry MediaOrganizeEntry) bool {
	return entry.Status != "done" && entry.Status != "retained" && (entry.P115Move != nil || entry.Failure != nil && entry.Failure.State == "moving" || entry.AttachmentKeep != nil && entry.AttachmentKeep.Phase == "recycling" || entry.VideoID != "" && entry.ResultCode == "WAITING_MAIN_FILE")
}

func mediaIdentityHasBareEpisode(identity mediafacts.Identity, file OrganizeFile) bool {
	match := mediaIdentityNumberTail.FindStringSubmatch(identity.Name)
	if match == nil {
		return false
	}
	number, err := strconv.Atoi(match[1])
	if err != nil || number != file.Episode {
		return false
	}
	facts := file.filenameFacts
	if facts == nil {
		parsed := mediafacts.ParseFilename(file.Name)
		facts = &parsed
	}
	if facts.Coordinate.Found {
		return false
	}
	stem := strings.TrimSuffix(file.Name, path.Ext(file.Name))
	prefix, ok := releaseparse.EpisodePrefix(organizeNumberingStem(stem))
	if !ok {
		return false
	}
	// 必须对应原文件中的发布编号，不能把坐标或破折号前的正式数字片名再次截短。
	base := mediafacts.ParseFilename(prefix + ".mediafacts").Identity.Name
	return mediafacts.NameKey(base) == mediafacts.NameKey(mediaIdentityEpisodeBase(identity.Name))
}

func mediaIdentityEpisodeBase(name string) string {
	match := mediaIdentityNumberTail.FindStringSubmatchIndex(name)
	if match == nil {
		return name
	}
	return strings.TrimRight(name[:match[0]], " ._-")
}

func mediaIdentityNumberedTitle(name string) string {
	match := mediaIdentityNumberTail.FindStringSubmatchIndex(name)
	if match == nil {
		return name
	}
	return strings.TrimRight(name[:match[3]], " ._-")
}

// 裸数字只有在同作品的剧集证据成立后才从片名中剥离，避免误改数字电影名。
func mediaResolveEpisodeIdentity(identity mediafacts.Identity, file *OrganizeFile, contexts map[string]bool) mediafacts.Identity {
	if identity.Conflict != "" || identity.Kind == "movie" {
		return identity
	}
	if mediaIdentityHasBareEpisode(identity, *file) {
		base := mediaIdentityEpisodeBase(identity.Name)
		if identity.Kind == "tv" || contexts[file.ParentID+":"+mediafacts.NameKey(base)] {
			identity.Name, identity.NameSource = base, "directory:numbering"
			identity.Kind, identity.KindSource = "tv", "directory:numbering"
		} else {
			identity.Name = mediaIdentityNumberedTitle(identity.Name)
			file.Episode, file.EndEpisode = 0, 0
		}
	} else {
		identity.Kind = "tv"
		if identity.KindSource == "" {
			identity.KindSource = "filename:numbering"
		}
	}
	return identity
}

// 同目录已明确的发布编号可补强其余集号，单独的数字片名仍保留电影解释。
func mediaScanIdentities(run *MediaOrganizeRun, entries []MediaOrganizeEntry) (map[string]mediafacts.Identity, map[string]bool) {
	identities := make(map[string]mediafacts.Identity, len(entries))
	contexts := make(map[string]bool, len(entries))
	weakNumbers := make(map[string]map[int]bool, len(entries))
	directories := make(map[string]mediafacts.DirectoryContext)
	directoryErrors := make(map[string]error)
	for _, entry := range entries {
		if entry.File.MediaType != "video" {
			continue
		}
		root, source := run.Rule.Source, entry.File.SourcePath
		if root == "" {
			root = "."
		}
		if source == "" {
			source = entry.File.Name
		}
		directory := path.Dir(strings.ReplaceAll(source, "\\", "/"))
		ctx, cached := directories[directory]
		if !cached {
			var err error
			ctx, err = mediafacts.ParseDirectoryContext(root, source)
			directories[directory], directoryErrors[directory] = ctx, err
		}
		var facts mediafacts.FilenameFacts
		if entry.File.filenameFacts != nil {
			facts = *entry.File.filenameFacts
		} else {
			facts = mediaOrganizeFilenameFacts(entry.File.Name)
		}
		identity := mediafacts.ResolveFilenameIdentity(facts, ctx)
		if directoryErrors[directory] != nil {
			identity.Conflict = "DIRECTORY_CONTEXT_INVALID"
		}
		identities[entry.File.ID] = identity
		if identity.Conflict != "" || identity.Kind == "movie" {
			continue
		}
		if mediaOrganizeHasNumbering(entry.File) && !mediaIdentityHasBareEpisode(identity, entry.File) {
			contexts[entry.File.ParentID+":"+mediafacts.NameKey(identity.Name)] = true
		} else if mediaOrganizeHasNumbering(entry.File) && identity.Kind == "tv" {
			contexts[entry.File.ParentID+":"+mediafacts.NameKey(mediaIdentityEpisodeBase(identity.Name))] = true
		} else if entry.File.Episode > 0 && identity.Kind == "" {
			key := entry.File.ParentID + ":" + mediafacts.NameKey(mediaIdentityEpisodeBase(identity.Name))
			if weakNumbers[key] == nil {
				weakNumbers[key] = make(map[int]bool)
			}
			weakNumbers[key][entry.File.Episode] = true
		}
	}
	// 没有前九集的半季包也可由连续编号互相印证，同一集多个修订版本不构成证据。
	for key, numbers := range weakNumbers {
		for number := range numbers {
			if numbers[number+1] {
				contexts[key] = true
				break
			}
		}
	}
	groups := make(map[string]MediaOrganizeGroup, len(run.Groups))
	for _, group := range run.Groups {
		groups[group.ID] = group
	}
	for _, entry := range run.Entries {
		group := groups[entry.Group]
		if entry.File.MediaType == "video" && mediaOrganizeHasNumbering(entry.File) && group.Identity.Kind == "tv" && group.Kind != "movie" {
			contexts[entry.File.ParentID+":"+mediafacts.NameKey(group.Identity.Name)] = true
		}
	}
	return identities, contexts
}

func cloneMediaRecognitionLogDetails(source MediaRecognitionLogDetails) MediaRecognitionLogDetails {
	source.Season = mediaOptionalNumber(source.Season)
	source.Episode = mediaOptionalNumber(source.Episode)
	source.EndEpisode = mediaOptionalNumber(source.EndEpisode)
	return source
}

// 当前结果使用可比较标量检测变化，不生成阶段对象。
type mediaEntryResultState struct {
	Group, VideoID, Status, Message, Target, Relative, Code string
	Kind, Title, Year                                       string
	TMDBID, Season, Episode, EndEpisode                     int
	ResultStatus, ResultMessage, Action                     string
	FailureState, FailureMessage                            string
	Pending                                                 bool
}

func mediaEntryState(entry MediaOrganizeEntry, group MediaOrganizeGroup) mediaEntryResultState {
	state := mediaEntryResultState{Group: entry.Group, VideoID: entry.VideoID, Status: entry.Status, Message: entry.Message, Target: entry.Target, Relative: entry.Relative, Code: entry.ResultCode, Kind: group.Kind, Title: group.Title, Year: group.Year, TMDBID: group.TMDBID, Season: entry.File.Season, Episode: entry.File.Episode, EndEpisode: entry.File.EndEpisode, Pending: mediaEntryWaiting(entry)}
	if entry.Result != nil {
		state.ResultStatus, state.ResultMessage, state.Action = entry.Result.Status, entry.Result.Message, entry.Result.Action
	}
	if entry.Failure != nil {
		state.FailureState, state.FailureMessage = entry.Failure.State, entry.Failure.Message
	}
	if state.Code == "" {
		state.Code = group.ResultCode
	}
	return state
}

func mediaQueueResultLog(run *MediaOrganizeRun, entry MediaOrganizeEntry, group MediaOrganizeGroup, code, level, message string) {
	source := path.Join(strings.ReplaceAll(run.Rule.Source, "\\", "/"), strings.ReplaceAll(entry.File.SourcePath, "\\", "/"))
	details := MediaRecognitionLogDetails{RunID: run.ID, GroupID: entry.Group, FileID: entry.File.ID, Status: "failed", ResultCode: code, Name: entry.File.Name, SourcePath: source, TargetPath: entry.Target}
	if group.TMDBID > 0 {
		details.WorkKey = group.Kind + ":" + strconv.Itoa(group.TMDBID)
	}
	if mediaOrganizeHasNumbering(entry.File) {
		details.Season, details.Episode = mediaOptionalNumber(&entry.File.Season), mediaOptionalNumber(&entry.File.Episode)
	}
	if entry.File.EndEpisode > 0 {
		details.EndEpisode = mediaOptionalNumber(&entry.File.EndEpisode)
	}
	run.recognitionEvents = append(run.recognitionEvents, mediaRecognitionLogEvent{details: details, level: level, message: message + " | 文件=" + entry.File.Name})
}

// 文件时间只随结果变化更新，最终失败日志在批次结束且事务提交后发布。
func mediaCheckpointResults(run *MediaOrganizeRun, previous MediaOrganizeRun) {
	groups := make(map[string]MediaOrganizeGroup, len(run.Groups))
	oldGroups := make(map[string]MediaOrganizeGroup, len(previous.Groups))
	oldEntries := make(map[string]MediaOrganizeEntry, len(previous.Entries))
	for _, group := range run.Groups {
		groups[group.ID] = group
	}
	for _, group := range previous.Groups {
		oldGroups[group.ID] = group
	}
	for _, entry := range previous.Entries {
		oldEntries[entry.File.ID] = entry
	}
	finished := !mediaRunActive(run.Status) && mediaRunActive(previous.Status)
	for i := range run.Entries {
		entry := &run.Entries[i]
		group := groups[entry.Group]
		old, exists := oldEntries[entry.File.ID]
		current := mediaEntryState(*entry, group)
		before := mediaEntryState(old, oldGroups[old.Group])
		changed := !exists || current != before
		if !changed {
			if entry.UpdatedAt.IsZero() {
				entry.UpdatedAt = old.UpdatedAt
			}
		} else {
			entry.UpdatedAt = run.UpdatedAt
		}
		if mediaRunActive(run.Status) || !mediaSelectedTask(run, *entry) || entry.File.MediaType == "cleanup" || entry.Reference {
			continue
		}
		if current.Status == "done" || current.Status == "retained" || current.Status == "ready" || current.Status == "pending" {
			continue
		}
		// 主文件依赖和未选中的附件没有发起写入，不单独制造失败日志。
		if entry.VideoID != "" && entry.ResultCode == "WAITING_MAIN_FILE" || entry.AttachmentKeep != nil && entry.AttachmentKeep.Phase == "waiting" {
			continue
		}
		if !finished && (!changed || exists && current.Status == before.Status && current.Message == before.Message && current.Code == before.Code && current.Pending == before.Pending && current.FailureState == before.FailureState && current.FailureMessage == before.FailureMessage) {
			continue
		}
		code, level, message := entry.ResultCode, "WARNING", entry.Message
		switch {
		case entry.P115Move != nil:
			code = "MOVE_UNCONFIRMED"
		case entry.Failure != nil && entry.Failure.State == "moving":
			code = "FAILURE_MOVE_UNCONFIRMED"
			if entry.Failure.Message != "" {
				message += "；" + entry.Failure.Message
			}
		case entry.AttachmentKeep != nil && entry.AttachmentKeep.Phase == "recycling":
			code = "ATTACHMENT_RECYCLE_UNCONFIRMED"
		case code != "":
		case entry.Status == "needs_match":
			code = group.ResultCode
			if code == "" {
				code = "MATCH_NOT_FOUND"
			}
			if group.Message != "" {
				message = group.Message
			}
		case entry.Status == "invalid":
			code = "NAMING_FAILED"
		case entry.Status == "conflict":
			code = "TARGET_EXISTS"
		case entry.Status == "unmatched":
			code = "VIDEO_ASSOCIATION_AMBIGUOUS"
		default:
			code, level = "MOVE_FAILED", "ERROR"
		}
		if message == "" {
			message = "文件处理暂未完成"
		}
		mediaQueueResultLog(run, *entry, group, code, level, message)
	}
	if run.Status == "failed" && run.Message != "" && (previous.Status != run.Status || previous.Message != run.Message) {
		details := MediaRecognitionLogDetails{RunID: run.ID, Status: "failed", ResultCode: "BATCH_FAILED"}
		run.recognitionEvents = append(run.recognitionEvents, mediaRecognitionLogEvent{details: details, level: "ERROR", message: run.Message})
	}
}
