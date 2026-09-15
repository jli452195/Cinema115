package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/model"
)

const followBotPageLimit = 10
const followBotResourceLimit = 100

type FollowBotSearcher interface {
	Search(context.Context, string, bool) (tguser.SearchResult, error)
	Select(context.Context, string) (tguser.SearchResult, error)
}

type followBotFinalError struct{ err error }

func (e *followBotFinalError) Error() string { return e.err.Error() }
func (e *followBotFinalError) Unwrap() error { return e.err }

var followYearPattern = regexp.MustCompile(`[（(]((?:19|20)\d{2})[）)]`)
var followTMDBPattern = regexp.MustCompile(`(?i)\{tmdb\s*[-=:：]\s*\d+\}`)
var followEpisodePattern = regexp.MustCompile(`(?i)(?:^|[^A-Z0-9])S\d{1,4}(?:E\d{1,4})?`)

func splitFollowTitleTags(value string) (string, []string) {
	value = strings.TrimSpace(value)
	tags := make([]string, 0, 2)
	for value != "" {
		opening, closing := "", ""
		if strings.HasPrefix(value, "[") {
			opening, closing = "[", "]"
		} else if strings.HasPrefix(value, "【") {
			opening, closing = "【", "】"
		} else {
			break
		}
		index := strings.Index(value, closing)
		if index < 0 {
			break
		}
		tags = append(tags, strings.TrimSpace(value[len(opening):index]))
		value = strings.TrimSpace(value[index+len(closing):])
	}
	return value, tags
}

func trimFollowTitleTags(value string) string {
	title, _ := splitFollowTitleTags(value)
	return title
}

func normalizeFollowTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return -1
	}, value)
}

func followResourceKind(raw string) string {
	_, tags := splitFollowTitleTags(raw)
	lower := strings.ToLower(strings.Join(tags, " "))
	for _, marker := range []string{"动画电影", "动漫电影", "韩电影", "外语电影", "电影"} {
		if strings.Contains(lower, marker) {
			return "movie"
		}
	}
	if followEpisodePattern.MatchString(raw) {
		return "tv"
	}
	for _, marker := range []string{"日番", "日剧", "欧美剧", "韩剧", "国产剧", "电视剧", "剧集", "动漫", "动画"} {
		if strings.Contains(lower, marker) {
			return "tv"
		}
	}
	return ""
}

func parseFollowResourceTitle(raw string) (title, year, kind string) {
	kind = followResourceKind(raw)
	end := len(raw)
	if location := followYearPattern.FindStringSubmatchIndex(raw); len(location) >= 4 {
		end = location[0]
		year = raw[location[2]:location[3]]
	} else if location := followTMDBPattern.FindStringIndex(raw); len(location) == 2 {
		end = location[0]
	}
	title = strings.TrimSpace(trimFollowTitleTags(raw[:end]))
	return title, year, kind
}

func followResourceMatches(job model.FollowBotJob, action tguser.Action) bool {
	title, year, kind := parseFollowResourceTitle(action.Title)
	if kind != "" && kind != job.Kind {
		return false
	}
	if action.TMDBID != "" {
		return action.TMDBID == strconv.Itoa(job.TMDBID)
	}
	if normalizeFollowTitle(title) == "" || normalizeFollowTitle(title) != normalizeFollowTitle(job.Title) {
		return false
	}
	targetYear := ""
	if len(job.Date) >= 4 {
		targetYear = job.Date[:4]
	}
	if year != "" && targetYear != "" && year != targetYear {
		return false
	}
	return year != "" && targetYear != "" && year == targetYear
}

func (s *FollowService) followBotLinks(ctx context.Context, job model.FollowBotJob) ([]string, error) {
	result, err := s.BotSearch.Search(ctx, job.Title, false)
	if err != nil {
		return nil, err
	}
	results := []tguser.SearchResult{result}
	actions := make([]tguser.Action, 0)
	seenPages := make(map[string]bool)
	for page := 0; page < len(results) && page < followBotPageLimit; page++ {
		current := results[page]
		var next string
		for _, action := range current.Items {
			switch action.Kind {
			case "resource":
				if len(actions) < followBotResourceLimit && followResourceMatches(job, action) {
					actions = append(actions, action)
				}
			case "page":
				if next == "" && strings.Contains(action.Title, "下一页") && !seenPages[action.ID] {
					next = action.ID
				}
			}
		}
		if next == "" || len(actions) >= followBotResourceLimit {
			break
		}
		seenPages[next] = true
		nextResult, err := s.BotSearch.Select(ctx, next)
		if err != nil {
			return nil, fmt.Errorf("读取 Bot 下一页: %w", err)
		}
		results = append(results, nextResult)
	}
	links := make([]string, 0, len(actions))
	seenLinks := make(map[string]bool)
	var selectionErrors error
	for _, action := range actions {
		selected, err := s.BotSearch.Select(ctx, action.ID)
		if err != nil {
			selectionErrors = errors.Join(selectionErrors, err)
			continue
		}
		for _, link := range selected.Links {
			if !seenLinks[link] {
				seenLinks[link] = true
				links = append(links, link)
			}
		}
	}
	if len(links) == 0 && selectionErrors != nil {
		return nil, fmt.Errorf("读取 Bot 匹配资源: %w", selectionErrors)
	}
	return links, nil
}

func (s *FollowService) processFollowBotJob(ctx context.Context, job model.FollowBotJob) error {
	settings, err := s.Store.FollowSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.AutoEnabled {
		return fmt.Errorf("追更自动资源处理未启用")
	}
	if s.BotSearch == nil || s.ShareRuns == nil || s.GenerationSettings == nil {
		return fmt.Errorf("追更自动资源服务尚未配置")
	}
	if err := s.GenerationSettings.ValidateCurrentAccount(); err != nil {
		return err
	}
	automation := s.GenerationSettings.Value()
	jobCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if s.Logs != nil {
		s.Logs.Append("订阅", fmt.Sprintf("%s:%d:%d", job.Kind, job.TMDBID, job.Cycle), "订阅", "INFO", "开始 Bot 搜索："+job.Title)
	}
	links, err := s.followBotLinks(jobCtx, job)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		if s.Logs != nil {
			s.Logs.Append("订阅", fmt.Sprintf("%s:%d:%d", job.Kind, job.TMDBID, job.Cycle), "订阅", "WARNING", "Bot 搜索完成，未找到精确匹配资源")
		}
		return s.Store.FinishFollowBotNoMatch(jobCtx, job)
	}
	active, err := s.Store.FollowBotJobActive(jobCtx, job)
	if err != nil {
		return err
	}
	if !active {
		return nil
	}
	ready, err := s.Store.SetFollowBotGenerating(jobCtx, job)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}
	generationCtx := context.WithValue(jobCtx, automationContextKey{}, automation)
	generationCtx = WithShareReplyManaged(WithGenerationSource(generationCtx, "订阅"))
	inputs := make([]GenerateInput, 0, len(links))
	for _, link := range links {
		input := automation.Generation
		input.URL, input.Password, input.CID, input.Mode = link, "", "0", ""
		inputs = append(inputs, input)
	}
	if s.Logs != nil {
		s.Logs.Append("订阅", fmt.Sprintf("%s:%d:%d", job.Kind, job.TMDBID, job.Cycle), "订阅", "INFO", fmt.Sprintf("匹配 %d 个分享链接，已提交公共批次队列", len(inputs)))
	}
	outcomes, generationErrors := s.ShareRuns.GenerateBatch(generationCtx, inputs)
	// 公共队列尚未接收任务时可以安全重试；一旦返回逐项结果则不重放可能发生过的云端操作。
	if len(outcomes) == 0 && generationErrors != nil {
		return generationErrors
	}
	generated := false
	for _, outcome := range outcomes {
		generationErrors = errors.Join(generationErrors, outcome.Err)
		if outcome.Result != nil && (outcome.Err == nil || outcome.Result.Written > 0) {
			generated = true
		}
	}
	if generated {
		if generationErrors != nil && s.Logs != nil {
			s.Logs.Append("订阅", fmt.Sprintf("%s:%d:%d", job.Kind, job.TMDBID, job.Cycle), "订阅", "WARNING", "部分匹配资源生成失败，已生成内容等待媒体库确认")
		}
		if err := s.Store.FinishFollowBotGenerated(generationCtx, job); err != nil {
			return &followBotFinalError{err}
		}
		if s.Logs != nil {
			s.Logs.Append("订阅", fmt.Sprintf("%s:%d:%d", job.Kind, job.TMDBID, job.Cycle), "订阅", "INFO", "STRM 已生成，追更状态更新为资源待确认")
		}
		return nil
	}
	if generationErrors == nil {
		generationErrors = fmt.Errorf("匹配资源未生成 STRM")
	}
	return &followBotFinalError{generationErrors}
}

// RunBotJobs 是唯一 Bot 搜索消费者；不同影视按 SQLite 入队顺序串行执行。
func (s *FollowService) RunBotJobs(ctx context.Context) error {
	if err := s.Store.RecoverFollowBotJobs(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		settings, err := s.Store.FollowSettings(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !settings.AutoEnabled {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				continue
			}
		}
		job, err := s.Store.NextFollowBotJob(ctx, time.Now())
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if job != nil {
			err := s.processFollowBotJob(ctx, *job)
			if err != nil && ctx.Err() == nil {
				message := err.Error()
				if len([]rune(message)) > 500 {
					message = string([]rune(message)[:500])
				}
				retry := true
				var final *followBotFinalError
				if errors.As(err, &final) {
					retry = false
				}
				if saveErr := s.Store.FailFollowBotJob(ctx, *job, message, retry); saveErr != nil {
					return errors.Join(err, saveErr)
				}
				if s.Logs != nil {
					s.Logs.Append("订阅", fmt.Sprintf("%s:%d:%d", job.Kind, job.TMDBID, job.Cycle), "订阅", "ERROR", message)
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
