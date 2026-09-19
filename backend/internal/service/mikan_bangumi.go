package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"strm115/backend/internal/client/mikan"
)

type MikanBangumiInput struct {
	ID      string `json:"id"`
	Group   string `json:"group"`
	Take    int    `json:"take"`
	Refresh bool   `json:"refresh"`
}

type mikanBangumiReader interface {
	Bangumi(context.Context, string, string, int) (mikan.Result, error)
}

func (s *MikanService) Bangumi(ctx context.Context, input MikanBangumiInput) (output MikanResult, err error) {
	if !mikan.ValidBangumiInput(input.ID, input.Group, input.Take) {
		return output, mikan.ErrInput
	}
	reader, ok := s.Client.(mikanBangumiReader)
	if !ok {
		return output, errors.New("蜜柑番剧读取功能不可用")
	}
	started := time.Now()
	task := ""
	if s.Logs != nil {
		s.Logs.mu.Lock()
		s.Logs.taskSequence++
		task = fmt.Sprintf("MIKAN-%06d", s.Logs.taskSequence)
		s.Logs.mu.Unlock()
	}
	log := func(level, message string) { s.Logs.Append("蜜柑", task, "番剧资源", level, message) }
	log("INFO", fmt.Sprintf("读取蜜柑番剧 %s，字幕组 %s，加载数量 %d", input.ID, input.Group, input.Take))
	defer func() {
		if err != nil {
			level := "ERROR"
			if errors.Is(err, context.Canceled) {
				level = "WARNING"
			}
			log(level, fmt.Sprintf("番剧资源读取未完成：%s，耗时 %.2f 秒", err.Error(), time.Since(started).Seconds()))
		} else {
			log("INFO", fmt.Sprintf("番剧资源读取完成，%d 条资源，%d 个字幕组待展开，耗时 %.2f 秒", len(output.Items), len(output.More), time.Since(started).Seconds()))
		}
	}()
	// 与关键词搜索共享缓存容量、并发合并和访问间隔，使用独立键避免互相污染。
	key := "\x00bangumi\x00" + input.ID + "\x00" + input.Group + "\x00" + strconv.Itoa(input.Take)
	entry, cached, err := s.cachedRead(ctx, key, input.Refresh, func(ctx context.Context) (mikan.Result, error) {
		return reader.Bangumi(ctx, input.ID, input.Group, input.Take)
	})
	if err != nil {
		return output, err
	}
	if cached {
		log("DEBUG", "从缓存或同一正在执行的请求返回，未重复访问蜜柑")
	}
	output.Result = cloneMikan(entry.result)
	output.Group, output.Cached = input.Group, cached
	output.Attempts = []MikanAttempt{}
	output.FetchedAt, output.ExpiresAt = entry.fetched, entry.expires
	return output, nil
}
