package mediafacts

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

type DirectoryRole string

const (
	DirectoryWork       DirectoryRole = "work"
	DirectorySeason     DirectoryRole = "season"
	DirectorySpecials   DirectoryRole = "specials"
	DirectoryCollection DirectoryRole = "collection"
	DirectoryCategory   DirectoryRole = "category"
	DirectoryTechnical  DirectoryRole = "technical"
	DirectoryContainer  DirectoryRole = "container"
)

type DirectoryFact struct {
	Name, Path string
	Role       DirectoryRole
	Identity   Identity
}

// DirectoryContext 仅保存本次计算需要的目录上下文，不读取附件或目录外的信息。
type DirectoryContext struct {
	Identity  Identity
	WorkPath  string
	Ancestors []DirectoryFact
}

var directorySeason = regexp.MustCompile(`(?i)^(?:S|SEASON[ ._-]*)([0-9]{1,5})$|^第\s*([0-9一二三四五六七八九十]+)\s*季$`)
var directorySpecial = regexp.MustCompile(`(?i)^(?:SPECIALS?|SP|OVA|OAD|EXTRAS?|BONUS)[ ._-]*[0-9]*$|^(?:特典|特别篇|特別篇|特殊内容|特殊內容|番外篇?|花絮)$`)
var directoryMixedKinds = regexp.MustCompile(`(?i)(?:TV|剧集|劇集).*[+＋].*(?:MOVIE|剧场版|劇場版)|(?:MOVIE|剧场版|劇場版).*[+＋].*(?:TV|剧集|劇集)`)
var directoryReleaseGroup = regexp.MustCompile(`(?i)^(?:SubsPlease|VCB(?:-S(?:tudio)?)?|Cleo|ANK|LoliHouse|Nix(?:-Raws)?)$|^[^\s/]{1,40}(?:字幕[组組]|[-_](?:SUBS?|RAWS?))$`)
var directoryTechnical = regexp.MustCompile(`(?i)[0-9]{3,4}\s*[x×]\s*[0-9]{3,4}|[0-9]{3,4}[pi]|[48]k|WEB[ ._-]?DL|WEBRIP|BLU[ ._-]?RAY|BDRIP|REMUX|HDTV|HEVC|AVC|X26[45]|H[ .]?26[45]|(?:AAC|FLAC|OPUS|TRUEHD|EAC3|AC3)(?:X[0-9]+)?|DTS(?:[ ._-]*HD)?(?:[ ._-]*MA)?|DDP|[0-9]\.[0-9]|10BIT|8BIT|DUAL[ ._-]?AUDIO|MULTI[ ._-]?(?:AUDIO|SUBS?)|CHS|CHT|SC_TC|BIG5|(?:ASS|SRT)(?:X[0-9]+)?|SUBS?|GB|BD|简繁中字|簡繁中字|简体字幕|簡體字幕|繁体字幕|繁體字幕|简中|簡中|繁中|中字|字幕|日语|日語|国语|國語|内封|內封|内嵌|內嵌|外挂|外掛|双语|雙語`)

// ParseDirectoryContext 在规则根内按角色查找祖先，根目录本身也可以是作品目录。
func ParseDirectoryContext(root, sourcePath string) (DirectoryContext, error) {
	out := DirectoryContext{Ancestors: make([]DirectoryFact, 0, 16)}
	root, err := directoryNormalizePath(root)
	if err != nil {
		return out, fmt.Errorf("解析整理根目录: %w", err)
	}
	sourcePath, err = directoryNormalizePath(sourcePath)
	if err != nil {
		return out, fmt.Errorf("解析来源路径: %w", err)
	}
	if directoryAbsolute(sourcePath) && !directoryAbsolute(root) {
		return out, fmt.Errorf("相对整理根目录不能接收绝对来源路径")
	}
	if !directoryAbsolute(sourcePath) {
		sourcePath = path.Join(root, sourcePath)
	}
	compareRoot, compareSource := root, sourcePath
	if directoryWindows(root) {
		compareRoot, compareSource = strings.ToLower(root), strings.ToLower(sourcePath)
	}
	if compareRoot != "." && (compareSource == compareRoot || !strings.HasPrefix(compareSource, strings.TrimRight(compareRoot, "/")+"/")) {
		return out, fmt.Errorf("来源文件不在整理根目录内")
	}
	directory := path.Dir(sourcePath)
	for {
		if len(out.Ancestors) >= 16 {
			return DirectoryContext{}, fmt.Errorf("来源目录超过 16 层继承上限")
		}
		name := path.Base(directory)
		if name != "." && name != "/" && !strings.HasSuffix(name, ":") {
			fact := directoryFact(name, directory, "parent:"+strconv.Itoa(len(out.Ancestors)+1))
			out.Ancestors = append(out.Ancestors, fact)
		}
		if directoryEqual(directory, root) || directory == "." || directory == "/" {
			break
		}
		next := path.Dir(directory)
		if next == directory {
			break
		}
		directory = next
	}
	out.resolve()
	return out, nil
}

// ResolveIdentity 为只需要身份结果的调用方复用文件事实提取。
func ResolveIdentity(name string, ctx DirectoryContext) Identity {
	return ResolveFilenameIdentity(ParseFilename(name), ctx)
}

// ResolveFilenameIdentity 仅按字段补缺；文件的明确声明始终优先，身份冲突保留可见。
func ResolveFilenameIdentity(facts FilenameFacts, ctx DirectoryContext) Identity {
	out, inherited := facts.Identity, ctx.Identity
	if out.Name == "" && utf8.RuneCountInString(NameKey(inherited.Name)) > 1 {
		out.Name, out.NameSource = inherited.Name, inherited.NameSource
		out.AlternativeName, out.AlternativeYear = inherited.AlternativeName, inherited.AlternativeYear
	} else if out.Name != "" && inherited.Name != "" && NameKey(out.Name) != NameKey(inherited.Name) {
		out.ParentName = inherited.Name
	}
	if out.Year == "" && inherited.Year != "" {
		out.Year, out.YearSource, out.YearFromParent = inherited.Year, inherited.YearSource, true
	}
	if out.TMDBID == 0 {
		out.TMDBID, out.TMDBIDSource = inherited.TMDBID, inherited.TMDBIDSource
	} else if inherited.TMDBID > 0 && out.TMDBID != inherited.TMDBID {
		identityAddConflict(&out, "TMDB_ID_CONFLICT")
	}
	if out.Kind == "" {
		out.Kind, out.KindSource = inherited.Kind, inherited.KindSource
	}
	if !out.SeasonExplicit && inherited.SeasonExplicit {
		out.Season, out.SeasonExplicit, out.SeasonSource = inherited.Season, true, inherited.SeasonSource
	}
	if out.ContentType == "" {
		out.ContentType, out.ContentTypeSource = inherited.ContentType, inherited.ContentTypeSource
	} else if inherited.ContentType != "" && out.ContentType != inherited.ContentType {
		identityAddConflict(&out, "CONTENT_TYPE_CONFLICT")
	}
	directoryMergeConflicts(&out, inherited)
	return out
}

func directoryNormalizePath(value string) (string, error) {
	if value == "" || len(value) > 8192 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("目录路径为空或格式无效")
	}
	value = strings.ReplaceAll(value, "\\", "/")
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("目录路径不能包含上级跳转")
		}
	}
	if len(value) >= 2 && value[1] == ':' && (len(value) == 2 || value[2] != '/') {
		return "", fmt.Errorf("不支持相对盘符路径")
	}
	cleaned := path.Clean(value)
	if len(cleaned) == 2 && cleaned[1] == ':' {
		cleaned += "/"
	}
	return cleaned, nil
}

func directoryWindows(value string) bool {
	return len(value) >= 3 && value[1] == ':' && value[2] == '/'
}

func directoryAbsolute(value string) bool {
	return strings.HasPrefix(value, "/") || directoryWindows(value)
}

func directoryEqual(a, b string) bool {
	return a == b || (directoryWindows(a) || directoryWindows(b)) && strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}

func directoryFact(name, fullPath, source string) DirectoryFact {
	fact := DirectoryFact{Name: name, Path: fullPath, Role: DirectoryWork, Identity: Identity{Season: 1}}
	trimmed := strings.TrimSpace(name)
	key := NameKey(trimmed)
	if identityCollection.MatchString(trimmed) || identityYearRange.MatchString(trimmed) || directoryMixedKinds.MatchString(trimmed) {
		fact.Role = DirectoryCollection
		return fact
	}
	if match := directorySeason.FindStringSubmatch(trimmed); match != nil {
		fact.Role = DirectorySeason
		season := chineseSeason(match[2])
		if match[1] != "" {
			season, _ = strconv.Atoi(match[1])
		}
		fact.Identity.Kind, fact.Identity.KindSource = "tv", source
		if season <= 1000 {
			fact.Identity.Season, fact.Identity.SeasonExplicit, fact.Identity.SeasonSource = season, true, source
		}
		return fact
	}
	if directorySpecial.MatchString(trimmed) {
		fact.Role = DirectorySpecials
		fact.Identity.Kind, fact.Identity.KindSource = "tv", source
		fact.Identity.Season, fact.Identity.SeasonExplicit, fact.Identity.SeasonSource = 0, true, source
		return fact
	}
	switch key {
	case "电影", "电影库", "movies", "movie", "films", "film":
		fact.Role, fact.Identity.Kind, fact.Identity.KindSource = DirectoryCategory, "movie", source
		return fact
	case "剧集", "电视剧", "电视", "tv", "tvshows", "tvshow", "series", "日剧", "美剧", "韩剧":
		fact.Role, fact.Identity.Kind, fact.Identity.KindSource = DirectoryCategory, "tv", source
		return fact
	case "日番", "新番", "番剧", "国漫", "动漫", "动画", "anime", "animation", "c115动漫":
		fact.Role, fact.Identity.ContentType, fact.Identity.ContentTypeSource = DirectoryCategory, "animation", source
		return fact
	case "动画电影", "动漫电影":
		fact.Role, fact.Identity.Kind, fact.Identity.KindSource = DirectoryCategory, "movie", source
		fact.Identity.ContentType, fact.Identity.ContentTypeSource = "animation", source
		return fact
	case "真人", "真人版", "真人剧", "真人电影", "liveaction":
		fact.Role, fact.Identity.ContentType, fact.Identity.ContentTypeSource = DirectoryCategory, "live_action", source
		return fact
	case "root", "source", "sources", "target", "destination", "inbox", "media", "library", "download", "downloads", "cinema115", "c115整理测试", "整理测试", "待整理目录", "整理完成目录", "整理失败目录", "c115待整理目录", "c115整理完成目录", "c115整理失败目录", "来源", "来源目录", "目标", "目标目录", "根目录":
		fact.Role = DirectoryContainer
		return fact
	}
	technical := strings.Trim(trimmed, " []【】()（）{}")
	if NameKey(directoryTechnical.ReplaceAllString(technical, "")) == "" || directoryReleaseGroup.MatchString(technical) {
		fact.Role = DirectoryTechnical
		return fact
	}
	if identityEpisodeCount.MatchString(name) {
		// 明确总集数提供原有剧集语境；按目录解析保留其年份，不把年份并入作品名。
		fact.Identity = ParseIdentity("", name)
	} else {
		fact.Identity = ParseIdentity(name + ".mediafacts")
	}
	directorySetSources(&fact.Identity, source)
	if fact.Identity.Name == "" {
		fact.Role = DirectoryContainer
	}
	return fact
}

func (ctx *DirectoryContext) resolve() {
	out := Identity{Season: 1}
	var season, category Identity
	blocked := false
	for _, fact := range ctx.Ancestors {
		switch fact.Role {
		case DirectoryCollection:
			blocked = true
		case DirectoryWork:
			if ctx.WorkPath == "" && !blocked {
				ctx.WorkPath, out = fact.Path, fact.Identity
			}
		case DirectorySeason, DirectorySpecials:
			if !blocked && !season.SeasonExplicit && ctx.WorkPath == "" {
				season = fact.Identity
			}
		case DirectoryCategory:
			if category.Kind == "" {
				category.Kind, category.KindSource = fact.Identity.Kind, fact.Identity.KindSource
			}
			if category.ContentType == "" {
				category.ContentType, category.ContentTypeSource = fact.Identity.ContentType, fact.Identity.ContentTypeSource
			} else if fact.Identity.ContentType != "" && category.ContentType != fact.Identity.ContentType {
				identityAddConflict(&category, "CONTENT_TYPE_CONFLICT")
			}
		}
	}
	if season.SeasonExplicit {
		out.Season, out.SeasonExplicit, out.SeasonSource = season.Season, true, season.SeasonSource
		if out.Kind == "" {
			out.Kind, out.KindSource = season.Kind, season.KindSource
		}
	}
	if out.Kind == "" {
		out.Kind, out.KindSource = category.Kind, category.KindSource
	}
	if out.ContentType == "" {
		out.ContentType, out.ContentTypeSource = category.ContentType, category.ContentTypeSource
	} else if category.ContentType != "" && out.ContentType != category.ContentType {
		identityAddConflict(&out, "CONTENT_TYPE_CONFLICT")
	}
	directoryMergeConflicts(&out, category)
	ctx.Identity = out
}

func directorySetSources(identity *Identity, source string) {
	for _, field := range []*string{&identity.NameSource, &identity.YearSource, &identity.KindSource, &identity.TMDBIDSource, &identity.SeasonSource, &identity.ContentTypeSource} {
		if *field != "" {
			*field = source
		}
	}
	identity.YearFromParent = identity.Year != ""
}

func directoryMergeConflicts(out *Identity, other Identity) {
	for _, code := range strings.Split(other.Conflict, ",") {
		if code != "" {
			identityAddConflict(out, code)
		}
	}
}
