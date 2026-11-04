package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/mediafacts"
)

func (s *TMDBService) AlternativeNames(ctx context.Context, kind string, id int) ([]string, error) {
	source, ok := s.source.(interface {
		AlternativeNames(context.Context, string, int) ([]string, error)
	})
	if !ok {
		return nil, nil
	}
	names, _, err := cachedTMDB[[]string](ctx, s, "recognition:names:"+kind+":"+strconv.Itoa(id), 30*24*time.Hour, func(ctx context.Context) (any, error) { return source.AlternativeNames(ctx, kind, id) })
	if err != nil {
		return nil, err
	}
	return *names, nil
}

type MediaRecognitionSource interface {
	OrganizeMetadata
	Browse(context.Context, tmdb.Options) (*tmdb.Page, error)
}

type mediaRecognition struct {
	Detail *tmdb.Detail
	Season int
}

func recognizeMedia(ctx context.Context, source MediaRecognitionSource, identity mediafacts.Identity) (*mediaRecognition, error) {
	match, _, err := recognizeMediaWithDecision(ctx, source, identity)
	return match, err
}

const (
	mediaRecognitionQueryLimit  = 4
	mediaRecognitionSearchLimit = 6
	mediaRecognitionPageLimit   = 2
	mediaRecognitionPageSize    = 20
	mediaRecognitionAliasLimit  = 6
	mediaRecognitionDetailLimit = 16
)

type mediaRecognitionQuery struct {
	name     string
	identity mediafacts.Identity
}

type mediaRecognitionObservation struct {
	item  tmdb.Item
	query mediaRecognitionQuery
	rank  int
}

type mediaRecognitionSearch struct {
	source       MediaRecognitionSource
	decision     *MediaRecognitionDecision
	observations []*mediaRecognitionObservation
	details      map[string]*tmdb.Detail
	detailErrors map[string]error
	aliases      map[string][]string
	aliasErrors  map[string]error
	detailCalls  int
	aliasCalls   int
	queryError   error
}

func recognizeMediaWithDecision(ctx context.Context, source MediaRecognitionSource, identity mediafacts.Identity) (match *mediaRecognition, decision MediaRecognitionDecision, err error) {
	decision = MediaRecognitionDecision{ResultCode: "NO_CANDIDATE", Message: "未找到作品候选"}
	defer func() {
		if err != nil {
			decision.ResultCode, decision.Message = "METADATA_QUERY_FAILED", "元数据查询暂未完成，可稍后重试"
		}
	}()
	if source == nil {
		return nil, decision, fmt.Errorf("TMDB 服务未配置")
	}
	if identity.Conflict != "" {
		decision.ResultCode, decision.Message = "DECLARATION_CONFLICT", "来源中的明确声明存在冲突，请核对作品信息"
		return nil, decision, nil
	}
	if identity.TMDBID > 0 && (identity.Kind == "movie" || identity.Kind == "tv") {
		detail, err := source.Details(ctx, identity.Kind, identity.TMDBID)
		if err != nil {
			if errors.Is(err, tmdb.ErrNotFound) {
				decision.ResultCode, decision.Message = "NO_CANDIDATE", "明确的 TMDB ID 未找到对应作品"
				return nil, decision, nil
			}
			return nil, decision, fmt.Errorf("查询明确作品详情: %w", err)
		}
		if detail == nil || detail.ID != identity.TMDBID || detail.Kind != identity.Kind {
			decision.ResultCode, decision.Message = "NO_ELIGIBLE_CANDIDATE", "TMDB 详情与声明的作品标识不一致"
			return nil, decision, nil
		}
		if code, message := mediaRecognitionContentType(identity.ContentType, detail); code != "" {
			decision.ResultCode, decision.Message = "DECLARATION_CONFLICT", message
			return nil, decision, nil
		}
		decision.ResultCode, decision.Message = "TMDB_ID", "已匹配来源声明的作品"
		return &mediaRecognition{Detail: detail, Season: identity.Season}, decision, nil
	}
	if identity.Name == "" {
		decision.ResultCode, decision.Message = "TITLE_MISSING", "没有可用于确定作品的名称或完整作品标识"
		return nil, decision, nil
	}
	kind := identity.Kind
	if kind == "" {
		kind = "multi"
	}
	queries := []mediaRecognitionQuery{{name: identity.Name, identity: identity}}
	if identity.AlternativeName != "" {
		alternative := identity
		alternative.Name, alternative.Year = identity.AlternativeName, identity.AlternativeYear
		alternative.AlternativeName, alternative.AlternativeYear = "", ""
		queries = append(queries, mediaRecognitionQuery{name: alternative.Name, identity: alternative})
	}
	if identity.ParentName != "" && mediafacts.NameKey(identity.ParentName) != mediafacts.NameKey(identity.Name) {
		queries = append(queries, mediaRecognitionQuery{name: identity.ParentName, identity: identity})
		// 父目录副标题只补充查询，仍需核对文件片名与季号，不能替换不相关的文件身份。
		if kind == "tv" && !identity.SeasonExplicit {
			if split := strings.LastIndexAny(identity.ParentName, " ：:"); split > 0 {
				queries = append(queries, mediaRecognitionQuery{name: strings.TrimSpace(identity.ParentName[:split]), identity: identity})
			}
		}
	}
	search := mediaRecognitionSearch{
		source: source, decision: &decision,
		observations: make([]*mediaRecognitionObservation, 0, mediaRecognitionQueryLimit*mediaRecognitionPageSize),
		details:      make(map[string]*tmdb.Detail), detailErrors: make(map[string]error),
		aliases: make(map[string][]string), aliasErrors: make(map[string]error),
	}
	queried := make(map[string]bool, len(queries))
	searchCalls := 0
	for _, query := range queries {
		queryKey := mediafacts.NameKey(query.name)
		if queryKey == "" || queried[queryKey] {
			continue
		}
		if len(queried) >= mediaRecognitionQueryLimit || searchCalls >= mediaRecognitionSearchLimit {
			break
		}
		queried[queryKey] = true
		seen := make(map[string]bool, mediaRecognitionPageSize)
		for pageNumber := 1; pageNumber <= mediaRecognitionPageLimit && searchCalls < mediaRecognitionSearchLimit; pageNumber++ {
			searchCalls++
			page, browseErr := source.Browse(ctx, tmdb.Options{Kind: kind, Query: query.name, Page: pageNumber})
			if browseErr != nil {
				return nil, decision, fmt.Errorf("搜索作品候选: %w", browseErr)
			}
			if page == nil {
				return nil, decision, fmt.Errorf("TMDB 未返回有效搜索结果")
			}
			observations := make([]*mediaRecognitionObservation, 0, min(len(page.Items), mediaRecognitionPageSize))
			for index, item := range page.Items[:min(len(page.Items), mediaRecognitionPageSize)] {
				key := item.Kind + ":" + strconv.Itoa(item.ID)
				if seen[key] {
					continue
				}
				seen[key] = true
				rank := (pageNumber-1)*mediaRecognitionPageSize + index + 1
				observation := &mediaRecognitionObservation{item: item, query: query, rank: rank}
				observations = append(observations, observation)
				search.observations = append(search.observations, observation)
			}
			if found, matchErr := search.choose(ctx, observations, "exact"); found != nil || matchErr != nil {
				return found, decision, matchErr
			}
			if page.TotalPages <= pageNumber {
				break
			}
		}
	}
	// 所有已搜索片名的直接结果优先于别名和模糊结果，避免近似首条遮住后续精确命中。
	for _, mode := range []string{"alias", "fuzzy"} {
		if found, matchErr := search.choose(ctx, search.observations, mode); found != nil || matchErr != nil {
			return found, decision, matchErr
		}
	}
	if search.queryError != nil {
		return nil, decision, search.queryError
	}
	if len(search.observations) > 0 {
		decision.ResultCode, decision.Message = "NO_ELIGIBLE_CANDIDATE", "搜索结果均未满足片名、类型、年份或季号要求"
	}
	return nil, decision, nil
}

func mediaRecognitionYear(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

func mediaRecognitionContentType(contentType string, detail *tmdb.Detail) (string, string) {
	if contentType == "" {
		return "", ""
	}
	animation := false
	for _, genre := range detail.Genres {
		animation = animation || genre.ID == 16
	}
	if contentType == "animation" && !animation || contentType == "live_action" && animation {
		return "CONTENT_TYPE_MISMATCH", "候选与明确声明的动画或真人类型不符"
	}
	if contentType == "live_action" && len(detail.Genres) == 0 {
		return "CONTENT_TYPE_UNKNOWN", "候选缺少类型信息，无法核对真人版声明"
	}
	return "", ""
}
