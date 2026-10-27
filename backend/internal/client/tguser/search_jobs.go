package tguser

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const searchQueueLimit = 8
const searchSubscriberLimit = 32

type searchTiming struct {
	operation, queue, firstPoll, poll, quiet, quarantine time.Duration
}

var defaultSearchTiming = searchTiming{45 * time.Second, 60 * time.Second, 250 * time.Millisecond, 500 * time.Millisecond, 2 * time.Second, time.Minute}

type OperationError struct {
	Code    string     `json:"code"`
	Message string     `json:"error"`
	RetryAt *time.Time `json:"retryAt,omitempty"`
}

func (e *OperationError) Error() string { return e.Message }

type SearchProgress struct {
	Stage    string `json:"stage"`
	Position int    `json:"position,omitempty"`
}

type SearchDiagnostic struct {
	Kind, Code   string
	Elapsed      time.Duration
	Items, Links int
}

type searchJob struct {
	key           string
	action        storedAction
	refresh       bool
	created       time.Time
	subscribers   int
	started, done bool
	changed       chan struct{}
	progress      SearchProgress
	result        SearchResult
	hasResult     bool
	version       uint64
	err           error
}

// Close cancels and joins every search worker, including queries detached from a browser.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	c.stopWorker()
	c.mu.Unlock()
	c.workers.Wait()
}

// Caller holds mu. Cache reads never acquire the Telegram connection gate.
func (c *Client) cachedExchangeLocked(action storedAction, key string, refresh bool) (SearchResult, bool, error) {
	if !validBotUsername(c.config.Bot) {
		return SearchResult{}, false, ErrBotConfig
	}
	if len(key) > 7 && key[:7] == "select:" {
		selected, ok := c.results[key[7:]]
		if !ok || !time.Now().Before(selected.expires) {
			return SearchResult{}, false, &OperationError{Code: "expired", Message: "搜索结果已过期，请刷新搜索"}
		}
		action = selected
		if action.direct != "" {
			return SearchResult{Items: []Action{}, Links: []string{action.direct}}, true, nil
		}
	}
	if refresh {
		return SearchResult{}, false, nil
	}
	result, ok := c.cachedResultLocked(key, time.Now())
	if ok && action.kind == "page" {
		if aggregate, exists := c.cachedResultLocked(action.searchKey, time.Now()); exists {
			return aggregate, true, nil
		}
	}
	return result, ok, nil
}

func notifySearch(job *searchJob) {
	close(job.changed)
	job.changed = make(chan struct{})
}

func (c *Client) exchange(ctx context.Context, action storedAction, key string, refresh bool, onUpdate func(SearchResult), onProgress func(SearchProgress)) (SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return SearchResult{}, context.Canceled
	}
	job := c.searches[key]
	if job == nil {
		cached, ok, err := c.cachedExchangeLocked(action, key, refresh)
		if err != nil || ok {
			c.mu.Unlock()
			return cached, err
		}
		if err := c.cooldownErrorLocked(); err != nil {
			c.mu.Unlock()
			return SearchResult{}, err
		}
		if len(c.searches) >= searchQueueLimit {
			c.mu.Unlock()
			return SearchResult{}, &OperationError{Code: "queue_full", Message: "Bot 搜索队列已满，请稍后重试"}
		}
		job = &searchJob{key: key, action: action, refresh: refresh, created: time.Now(), changed: make(chan struct{}), progress: SearchProgress{Stage: "queued", Position: len(c.queue) + 1}}
		c.searches[key] = job
		c.queue = append(c.queue, job)
	}
	if job.subscribers >= searchSubscriberLimit {
		c.mu.Unlock()
		return SearchResult{}, &OperationError{Code: "queue_full", Message: "此搜索已有过多等待请求，请稍后重试"}
	}
	job.subscribers++
	if !c.workerRunning {
		c.workerRunning = true
		c.workers.Add(1)
		go c.runSearchJobs()
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		job.subscribers--
		if job.subscribers == 0 && !job.started && !job.done {
			c.removeQueuedLocked(job, context.Canceled)
		}
	}()
	queueTimer := time.NewTimer(time.Until(job.created.Add(c.searchTiming.queue)))
	defer queueTimer.Stop()
	queueDeadline := queueTimer.C
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	var version uint64
	var previous SearchProgress
	for {
		c.mu.Lock()
		progress, result, hasResult, current := job.progress, cloneResult(job.result), job.hasResult, job.version
		done, err, changed, started := job.done, job.err, job.changed, job.started
		c.mu.Unlock()
		if ctx.Err() != nil {
			return SearchResult{}, ctx.Err()
		}
		if progress != previous && onProgress != nil {
			onProgress(progress)
		}
		previous = progress
		if hasResult && current != version && onUpdate != nil {
			onUpdate(result)
		}
		version = current
		if done {
			return result, err
		}
		if started {
			queueDeadline = nil
		}
		select {
		case <-ctx.Done():
			return SearchResult{}, ctx.Err()
		case <-changed:
		case <-heartbeat.C:
			if onProgress != nil {
				onProgress(previous)
			}
		case <-queueDeadline:
			c.mu.Lock()
			if !job.started && !job.done {
				c.removeQueuedLocked(job, &OperationError{Code: "queue_timeout", Message: "等待 Bot 搜索队列超时，本次查询尚未发送"})
			}
			c.mu.Unlock()
			queueDeadline = nil
		}
	}
}

func (c *Client) removeQueuedLocked(job *searchJob, err error) {
	for i, queued := range c.queue {
		if queued == job {
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			break
		}
	}
	job.done, job.err = true, err
	delete(c.searches, job.key)
	notifySearch(job)
	c.updateQueueLocked()
}

func (c *Client) updateQueueLocked() {
	for i, job := range c.queue {
		job.progress.Position = i + 1
		notifySearch(job)
	}
}

func (c *Client) runSearchJobs() {
	defer c.workers.Done()
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			c.workerRunning = false
			c.mu.Unlock()
			return
		}
		job := c.queue[0]
		if c.closed || job.subscribers == 0 {
			c.removeQueuedLocked(job, context.Canceled)
			c.mu.Unlock()
			continue
		}
		if time.Since(job.created) >= c.searchTiming.queue {
			c.removeQueuedLocked(job, &OperationError{Code: "queue_timeout", Message: "等待 Bot 搜索队列超时，本次查询尚未发送"})
			c.mu.Unlock()
			continue
		}
		c.mu.Unlock()
		// Login/check operations share the connection gate. Waiting here also has a deadline.
		gateCtx, gateCancel := context.WithDeadline(c.workerContext, job.created.Add(c.searchTiming.queue))
		select {
		case c.gate <- struct{}{}:
		case <-gateCtx.Done():
			gateCancel()
			c.mu.Lock()
			if !job.done {
				c.removeQueuedLocked(job, &OperationError{Code: "queue_timeout", Message: "等待 Telegram 连接超时，本次查询尚未发送"})
			}
			c.mu.Unlock()
			continue
		}
		gateCancel()
		c.mu.Lock()
		if job.done || job.subscribers == 0 || c.closed {
			if !job.done {
				c.removeQueuedLocked(job, context.Canceled)
			}
			c.mu.Unlock()
			c.unlock()
			continue
		}
		c.queue = c.queue[1:]
		job.started = true
		job.progress = SearchProgress{Stage: "waiting"}
		notifySearch(job)
		c.updateQueueLocked()
		c.mu.Unlock()
		started := time.Now()
		opCtx, cancel := context.WithTimeout(c.workerContext, c.searchTiming.operation)
		result, err := c.performExchange(opCtx, job.action, job.key, job.refresh, func(update SearchResult) {
			c.mu.Lock()
			defer c.mu.Unlock()
			job.result, job.hasResult = cloneResult(update), true
			job.version++
			job.progress = SearchProgress{Stage: "collecting"}
			notifySearch(job)
		})
		cancel()
		c.mu.Lock()
		job.result, job.err, job.done = result, err, true
		delete(c.searches, job.key)
		notifySearch(job)
		c.mu.Unlock()
		c.unlock()
		if c.ObserveSearch != nil {
			code := "complete"
			var detail *OperationError
			if errors.As(err, &detail) {
				code = detail.Code
			} else if err != nil {
				code = "failed"
			}
			c.ObserveSearch(SearchDiagnostic{Kind: job.action.kind, Code: code, Elapsed: time.Since(started), Items: len(result.Items), Links: len(result.Links)})
		}
	}
}

func (c *Client) cooldownErrorLocked() error {
	if !time.Now().Before(c.cooldown) {
		return nil
	}
	until := c.cooldown
	seconds := int(time.Until(until).Seconds()) + 1
	return &OperationError{Code: "cooldown", Message: fmt.Sprintf("上一项 Bot 操作未完整结束，正在隔离迟到回复，约 %d 秒后可重试", seconds), RetryAt: &until}
}
