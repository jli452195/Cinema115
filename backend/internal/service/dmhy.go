package service

import (
	"context"
	"time"

	"strm115/backend/internal/btresource"
	"strm115/backend/internal/client/dmhy"
)

type DmhySearchInput struct {
	Query    string `json:"query"`
	Fallback string `json:"fallback"`
	Group    string `json:"group"`
	Page     int    `json:"page"`
	Refresh  bool   `json:"refresh"`
}

type DmhySearchResult struct {
	Items     []dmhy.Item                   `json:"items"`
	Groups    []dmhy.Group                  `json:"groups"`
	Specs     map[string]ResourceSearchSpec `json:"specs"`
	Query     string                        `json:"query"`
	Group     string                        `json:"group"`
	Page      int                           `json:"page"`
	HasMore   bool                          `json:"hasMore"`
	Attempts  []MikanAttempt                `json:"attempts"`
	Cached    bool                          `json:"cached"`
	FetchedAt time.Time                     `json:"fetchedAt"`
	ExpiresAt time.Time                     `json:"expiresAt"`
}

type DmhyMagnetResult struct {
	ID        string    `json:"id"`
	Magnet    string    `json:"magnet"`
	Cached    bool      `json:"cached"`
	FetchedAt time.Time `json:"fetchedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type DmhyService struct {
	*animeSiteCache
	Logs          *GenerationLogs
	TorrentReader btresource.TorrentReader
	torrents      *animeSiteCache
}

func NewDmhyService(client animeSiteTransport) *DmhyService {
	s := &DmhyService{animeSiteCache: newAnimeSiteCache(client, "dmhy"), TorrentReader: &btresource.HTTPReader{ProxyScope: "dmhy"}}
	s.torrents = newAnimeSiteCache(dmhyTorrentTransport{site: s}, "dmhy")
	return s
}

// 搜索保留原站所有分类，范围筛选只影响前端展示，不改变缓存与分页结果。
func (s *DmhyService) Search(ctx context.Context, in DmhySearchInput) (DmhySearchResult, error) {
	var out DmhySearchResult
	query, err := dmhy.NormalizeQuery(in.Query)
	if err != nil || !dmhy.ValidGroup(in.Group) {
		return out, dmhy.ErrInput
	}
	if in.Fallback != "" {
		in.Fallback, err = dmhy.NormalizeQuery(in.Fallback)
		if err != nil {
			return out, dmhy.ErrInput
		}
	}
	if in.Page == 0 {
		in.Page = 1
	}
	endpoint, err := dmhy.SearchPath(query, in.Group, in.Page)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	attempts := make([]MikanAttempt, 0, 2)
	for {
		var result dmhy.Result
		read, cached, err := s.read(ctx, endpoint, 2*time.Minute, in.Refresh, nil, func(body []byte) error {
			var parseErr error
			result, parseErr = dmhy.ParseSearch(body, in.Page)
			return parseErr
		})
		if err != nil {
			return out, err
		}
		// 首次读取复用入缓存时的校验结果，缓存及合并请求仍各自解析。
		if cached {
			result, err = dmhy.ParseSearch(read.body, in.Page)
			if err != nil {
				return out, err
			}
		}
		attempts = append(attempts, MikanAttempt{Query: query, Count: result.RawCount, Cached: cached})
		// 无磁力、未知分类或本页无影视都不能触发自动改词，只有真正空结果才兜底。
		if result.RawCount == 0 && in.Page == 1 && in.Group == "" && in.Fallback != "" && in.Fallback != query {
			query, in.Fallback = in.Fallback, ""
			endpoint, err = dmhy.SearchPath(query, in.Group, in.Page)
			if err != nil {
				return out, err
			}
			continue
		}
		specs := make(map[string]ResourceSearchSpec, len(result.Items))
		for _, item := range result.Items {
			specs[item.ID] = resourceSearchSpec(item.Title)
		}
		return DmhySearchResult{
			Items: result.Items, Groups: result.Groups, Specs: specs,
			Query: query, Group: in.Group, Page: in.Page, HasMore: result.HasMore,
			Attempts: attempts, Cached: cached, FetchedAt: read.observed, ExpiresAt: read.expires,
		}, nil
	}
}

// 仅在用户请求时补读单条原帖磁力，不请求种子或访问下载账号。
func (s *DmhyService) ResolveMagnet(ctx context.Context, id string) (DmhyMagnetResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	detail, read, cached, err := s.readDetail(ctx, id)
	if err != nil {
		return DmhyMagnetResult{}, err
	}
	return DmhyMagnetResult{
		ID: detail.ID, Magnet: detail.Magnet, Cached: cached,
		FetchedAt: read.observed, ExpiresAt: read.expires,
	}, nil
}

func (s *DmhyService) readDetail(ctx context.Context, id string) (dmhy.Detail, animeSiteResponse, bool, error) {
	endpoint, err := dmhy.DetailPath(id)
	if err != nil {
		return dmhy.Detail{}, animeSiteResponse{}, false, err
	}
	var detail dmhy.Detail
	read, cached, err := s.read(ctx, endpoint, 30*time.Minute, false, nil, func(body []byte) error {
		var parseErr error
		detail, parseErr = dmhy.ParseDetail(body, id)
		return parseErr
	})
	if err != nil {
		return dmhy.Detail{}, animeSiteResponse{}, false, err
	}
	if cached {
		detail, err = dmhy.ParseDetail(read.body, id)
	}
	return detail, read, cached, err
}
