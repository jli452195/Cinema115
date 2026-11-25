package tguser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/mediafacts"
	"strm115/backend/internal/netproxy"
)

const telegraphBodyLimit = 2 << 20
const telegraphFileLimit = 1000
const telegraphCacheLimit = 32

type ChannelSourceGroup struct {
	ID    string        `json:"id"`
	Title string        `json:"title"`
	Links []ChannelLink `json:"links"`
}

type ChannelSourceResult struct {
	SourceID    string               `json:"sourceId"`
	PageTitle   string               `json:"pageTitle"`
	Groups      []ChannelSourceGroup `json:"groups"`
	ShareGroups []ChannelSourceGroup `json:"shareGroups,omitempty"`
	Cached      bool                 `json:"cached"`
	ExpiresAt   time.Time            `json:"expiresAt"`
}

type telegraphCacheEntry struct {
	result  ChannelSourceResult
	expires time.Time
}

type telegraphFlight struct {
	done   chan struct{}
	result ChannelSourceResult
	err    error
}

type channelSourceContext struct {
	source      ChannelSource
	target      ChannelSearchTarget
	ownerID     int64
	fingerprint string
	expires     time.Time
}

var telegraphPathPattern = regexp.MustCompile(`^[\pL\pN_.-]{1,256}$`)
var telegraphTMDBID = regexp.MustCompile(`(?i)\{?tmdb(?:id)?\s*[-=:：]\s*([0-9]+)\}?`)
var telegraphTMDBURL = regexp.MustCompile(`(?i)^https?://(?:www\.)?themoviedb\.org/(movie|tv)/([0-9]+)(?:[/?#-]|$)`)
var telegraphED2K = regexp.MustCompile(`(?i)ed2k://\|file\|[^\r\n<>]+?\|/`)
var telegraphTitleYear = regexp.MustCompile(`\s*[(（]\s*(?:19|20)[0-9]{2}\s*[)）]\s*$`)
var telegraphTitleProgress = regexp.MustCompile(`\s+(?:全\s*[0-9]{1,5}\s*集(?:\s+·\s+S[0-9]{1,2}E[0-9]{1,5}-E[0-9]{1,5})?|更新至\s*(?:第\s*)?[0-9]{1,5}\s*集)\s*$`)

func channelTelegraphSource(raw string) (ChannelSource, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.Port() != "" || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Hostname(), "telegra.ph") {
		return ChannelSource{}, false
	}
	path := strings.TrimPrefix(u.Path, "/")
	if !telegraphPathPattern.MatchString(path) || path == "." || path == ".." {
		return ChannelSource{}, false
	}
	u = &url.URL{Scheme: "https", Host: "telegra.ph", Path: "/" + path}
	hash := sha256.Sum256([]byte(u.String()))
	return ChannelSource{ID: hex.EncodeToString(hash[:12]), Type: "telegraph", URL: u.String(), Label: "Telegraph 文件列表"}, true
}

func channelSourceExpired() error {
	return &OperationError{Code: "channel_search_expired", Message: "频道搜索已过期或配置已改变，请重新搜索"}
}

// 调用方持有 mu；候选只能来自当前账号、当前频道配置的有效搜索。
func (c *Client) channelSourceLocked(resourceID, sourceID string, target ChannelSearchTarget) (channelSourceContext, error) {
	if c.closed {
		return channelSourceContext{}, context.Canceled
	}
	if c.sessionStore.Value() == "" || !c.authorized || c.userID == 0 {
		return channelSourceContext{}, ErrLogin
	}
	now := time.Now()
	fingerprint := channelFingerprint(c.channels)
	var found channelSourceContext
	for _, search := range c.channelSearches {
		if !now.Before(search.expires) || search.ownerID != c.userID || search.fingerprint != fingerprint || search.target.Kind != target.Kind || search.target.TMDBID != target.TMDBID {
			continue
		}
		for _, item := range search.result.Items {
			if item.ID != resourceID {
				continue
			}
			for _, source := range item.Sources {
				if source.ID == sourceID && source.Type == "telegraph" && search.expires.After(found.expires) {
					found = channelSourceContext{source: source, target: search.target, ownerID: search.ownerID, fingerprint: fingerprint, expires: search.expires}
				}
			}
		}
	}
	if found.source.ID == "" {
		return channelSourceContext{}, channelSourceExpired()
	}
	return found, nil
}

// ResolveChannelSource 只读取服务端已有候选，不接收任意网址，不发送 Telegram 消息。
func (c *Client) ResolveChannelSource(ctx context.Context, resourceID, sourceID string, target ChannelSearchTarget) (ChannelSourceResult, error) {
	if resourceID == "" || len(resourceID) > 96 || sourceID == "" || len(sourceID) > 64 || (target.Kind != "movie" && target.Kind != "tv") {
		return ChannelSourceResult{}, ErrInput
	}
	id, err := strconv.ParseUint(target.TMDBID, 10, 63)
	if err != nil || id == 0 {
		return ChannelSourceResult{}, ErrInput
	}
	// 请求只声明作品键，片名必须从同作品的有效服务端搜索上下文读取。
	target.TMDBID, target.Title = strconv.FormatUint(id, 10), ""
	release, err := c.read(ctx)
	if err != nil {
		return ChannelSourceResult{}, err
	}
	defer release()
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	stop := context.AfterFunc(c.workerContext, cancel)
	defer stop()
	defer cancel()
	c.mu.Lock()
	scope, err := c.channelSourceLocked(resourceID, sourceID, target)
	if err != nil {
		c.mu.Unlock()
		return ChannelSourceResult{}, err
	}
	key := strconv.FormatInt(scope.ownerID, 10) + "/" + scope.fingerprint + "/" + scope.source.ID + "/" + scope.target.Kind + "/" + scope.target.TMDBID + "/" + scope.target.Title
	for id, entry := range c.telegraphCache {
		if !time.Now().Before(entry.expires) {
			delete(c.telegraphCache, id)
		}
	}
	if entry, ok := c.telegraphCache[key]; ok {
		result := entry.result
		result.Cached, result.ExpiresAt = true, minTime(entry.expires, scope.expires)
		c.mu.Unlock()
		return result, nil
	}
	if flight := c.telegraphFlights[key]; flight != nil {
		c.mu.Unlock()
		select {
		case <-requestCtx.Done():
			return ChannelSourceResult{}, requestCtx.Err()
		case <-flight.done:
			if flight.err != nil {
				return ChannelSourceResult{}, flight.err
			}
			c.mu.Lock()
			current, stateErr := c.channelSourceLocked(resourceID, sourceID, target)
			c.mu.Unlock()
			if stateErr != nil || current.fingerprint != scope.fingerprint || current.ownerID != scope.ownerID || current.target != scope.target {
				return ChannelSourceResult{}, channelSourceExpired()
			}
			result := flight.result
			result.Cached, result.ExpiresAt = true, minTime(result.ExpiresAt, current.expires)
			return result, nil
		}
	}
	flight := &telegraphFlight{done: make(chan struct{})}
	if c.telegraphFlights == nil {
		c.telegraphFlights = make(map[string]*telegraphFlight)
	}
	c.telegraphFlights[key] = flight
	client := c.telegraphHTTP
	c.mu.Unlock()
	result, fetchErr := c.resolveTelegraph(requestCtx, client, scope)
	if requestCtx.Err() != nil {
		fetchErr = requestCtx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	current, stateErr := c.channelSourceLocked(resourceID, sourceID, target)
	if stateErr != nil || current.fingerprint != scope.fingerprint || current.ownerID != scope.ownerID || current.target != scope.target {
		fetchErr = channelSourceExpired()
	}
	if fetchErr == nil {
		expires := time.Now().Add(channelSearchTTL)
		result.ExpiresAt = minTime(expires, current.expires)
		if c.telegraphCache == nil {
			c.telegraphCache = make(map[string]telegraphCacheEntry)
		}
		if len(c.telegraphCache) >= telegraphCacheLimit {
			oldest := ""
			var earliest time.Time
			for id, entry := range c.telegraphCache {
				if oldest == "" || entry.expires.Before(earliest) {
					oldest, earliest = id, entry.expires
				}
			}
			delete(c.telegraphCache, oldest)
		}
		c.telegraphCache[key] = telegraphCacheEntry{result: result, expires: expires}
	}
	flight.result, flight.err = result, fetchErr
	delete(c.telegraphFlights, key)
	close(flight.done)
	return result, fetchErr
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (c *Client) resolveTelegraph(ctx context.Context, client *http.Client, scope channelSourceContext) (ChannelSourceResult, error) {
	unlock, err := c.acquire(ctx, c.telegraphGate, 1)
	if err != nil {
		return ChannelSourceResult{}, err
	}
	defer unlock()
	if client == nil {
		client = &http.Client{Transport: netproxy.Transport("telegram_user"), Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	source, ok := channelTelegraphSource(scope.source.URL)
	if !ok {
		return ChannelSourceResult{}, ErrInput
	}
	u, _ := url.Parse(source.URL)
	endpoint := "https://api.telegra.ph/getPage/" + url.PathEscape(strings.TrimPrefix(u.Path, "/")) + "?return_content=true"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ChannelSourceResult{}, fmt.Errorf("创建 Telegraph 读取请求: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ChannelSourceResult{}, ctx.Err()
		}
		return ChannelSourceResult{}, &OperationError{Code: "channel_source_failed", Message: "Telegraph 页面读取失败，可重试"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ChannelSourceResult{}, &OperationError{Code: "channel_source_failed", Message: "Telegraph 页面暂不可用，可重试"}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, telegraphBodyLimit+1))
	if err != nil || len(data) > telegraphBodyLimit {
		return ChannelSourceResult{}, &OperationError{Code: "channel_source_failed", Message: "Telegraph 页面读取不完整或内容过大，可重试"}
	}
	result, err := parseTelegraph(data, scope.target)
	if err != nil {
		return ChannelSourceResult{}, &OperationError{Code: "channel_source_failed", Message: err.Error()}
	}
	result.SourceID = source.ID
	return result, nil
}

type telegraphNode struct {
	tag         string
	text        string
	href        string
	children    []telegraphNode
	nestedBlock bool
}

type telegraphPage struct {
	Title   string            `json:"title"`
	Content []json.RawMessage `json:"content"`
}

func parseTelegraph(data []byte, target ChannelSearchTarget) (ChannelSourceResult, error) {
	var response struct {
		OK     bool          `json:"ok"`
		Result telegraphPage `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil || !response.OK || strings.TrimSpace(response.Result.Title) == "" {
		return ChannelSourceResult{}, errors.New("Telegraph 页面格式不可识别，可重试或查看原帖")
	}
	page := response.Result
	if !telegraphTitleMatches(page.Title, target) {
		return ChannelSourceResult{}, errors.New("Telegraph 页面作品与当前条目不匹配，请查看原帖")
	}
	remaining := 16000
	nodes, err := decodeTelegraphNodes(page.Content, target, 0, &remaining)
	if err != nil {
		return ChannelSourceResult{}, err
	}
	groups := make([]ChannelSourceGroup, 0, 8)
	current := ChannelSourceGroup{Title: "文件"}
	groupFields, groupSubtitles := "", ""
	seen := make(map[string]bool)
	files := 0
	shares := newTelegraphShares()
	flush := func() {
		if len(current.Links) == 0 {
			return
		}
		for i := range current.Links {
			channelGroupMetadata(&current.Links[i], current.Title, groupFields, groupSubtitles)
		}
		current.ID = telegraphGroupID(len(groups), current.Title)
		groups = append(groups, current)
	}
	add := func(text string) error {
		for _, raw := range telegraphED2K.FindAllString(text, -1) {
			file, err := channelED2KIdentity(raw)
			if err != nil {
				continue
			}
			key := file.Hash + "/" + file.SizeBytes
			if seen[key] {
				continue
			}
			files++
			if files > telegraphFileLimit {
				return errors.New("Telegraph 页面文件过多，请到原帖查看")
			}
			seen[key] = true
			links := []ChannelLink{{Type: "ed2k", URL: raw, File: file}}
			metadata := channelMetadata("", "", links)
			links[0].Metadata = metadata
			current.Links = append(current.Links, links[0])
		}
		return nil
	}
	var walk func(telegraphNode) error
	walk = func(node telegraphNode) error {
		if node.tag == "h3" {
			flush()
			block := readTelegraphBlock(node)
			current, seen = ChannelSourceGroup{Title: telegraphGroupTitle(block.text, "文件")}, make(map[string]bool)
			groupFields, groupSubtitles = "", ""
			shares.startGroup(block.text, false)
			return shares.add(node, block)
		}
		if node.tag == "hr" {
			shares.startGroup("", true)
			return nil
		}
		if node.nestedBlock && node.tag != "a" {
			// 嵌套段落单独读取规格和密码，段落外的相邻行内节点仍拼合。
			start := 0
			for i, child := range node.children {
				if !child.isBlock() && !child.nestedBlock {
					continue
				}
				if start < i {
					if err := walk(telegraphNode{tag: node.tag, children: node.children[start:i]}); err != nil {
						return err
					}
				}
				if err := walk(child); err != nil {
					return err
				}
				start = i + 1
			}
			if start < len(node.children) {
				return walk(telegraphNode{tag: node.tag, children: node.children[start:]})
			}
			return nil
		}
		block := readTelegraphBlock(node)
		if node.tag == "p" {
			fields, subtitles := telegraphGroupFields(block.text)
			groupFields += fields
			if subtitles != "" {
				if groupSubtitles != "" {
					groupSubtitles += "/"
				}
				groupSubtitles += subtitles
			}
		}
		// ED2K 段落优先使用可见文本，单独的锚点优先使用实际地址。
		if node.tag != "a" {
			if err := add(block.text); err != nil {
				return err
			}
		}
		for _, href := range block.hrefs {
			if err := add(href); err != nil {
				return err
			}
		}
		if node.tag == "a" {
			if err := add(block.text); err != nil {
				return err
			}
		}
		return shares.add(node, block)
	}
	for _, node := range nodes {
		if err := walk(node); err != nil {
			return ChannelSourceResult{}, err
		}
	}
	flush()
	shareGroups := shares.finish()
	if len(groups) == 0 && len(shareGroups) == 0 {
		return ChannelSourceResult{}, errors.New("Telegraph 页面未找到有效 ED2K 文件或 115 分享，可重试或查看原帖")
	}
	return ChannelSourceResult{PageTitle: page.Title, Groups: groups, ShareGroups: shareGroups}, nil
}

// 同一棵节点树供两种资源解析使用，标题内的外链也要先验证作品身份。
func decodeTelegraphNodes(rawNodes []json.RawMessage, target ChannelSearchTarget, depth int, remaining *int) ([]telegraphNode, error) {
	if len(rawNodes) == 0 {
		return nil, nil
	}
	if depth > 32 || len(rawNodes) > *remaining {
		return nil, errors.New("Telegraph 页面结构过于复杂，请到原帖查看")
	}
	*remaining -= len(rawNodes)
	nodes := make([]telegraphNode, len(rawNodes))
	for i, raw := range rawNodes {
		if len(raw) > 0 && raw[0] == '"' {
			if err := json.Unmarshal(raw, &nodes[i].text); err != nil {
				return nil, errors.New("Telegraph 文本格式不可识别")
			}
			continue
		}
		var object struct {
			Tag      string            `json:"tag"`
			Attrs    map[string]string `json:"attrs"`
			Children []json.RawMessage `json:"children"`
		}
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, errors.New("Telegraph 节点格式不可识别")
		}
		node := telegraphNode{tag: object.Tag}
		if node.tag == "a" {
			node.href = object.Attrs["href"]
			if telegraphTMDBLinkConflicts(node.href, target) {
				return nil, errors.New("Telegraph 页面 TMDB 身份与当前条目不匹配")
			}
		}
		children, err := decodeTelegraphNodes(object.Children, target, depth+1, remaining)
		if err != nil {
			return nil, err
		}
		node.children = children
		for _, child := range children {
			if child.isBlock() || child.nestedBlock {
				node.nestedBlock = true
				break
			}
		}
		nodes[i] = node
	}
	return nodes, nil
}

func (node telegraphNode) isBlock() bool {
	switch node.tag {
	case "h3", "hr", "p", "pre", "li":
		return true
	default:
		return false
	}
}

func telegraphGroupTitle(text, fallback string) string {
	title := strings.TrimSpace(text)
	if title == "" {
		return fallback
	}
	if runes := []rune(title); len(runes) > 500 {
		return string(runes[:500])
	}
	return title
}

func telegraphGroupID(index int, title string) string {
	hash := sha256.Sum256([]byte(strconv.Itoa(index) + "/" + title))
	return hex.EncodeToString(hash[:12])
}

func telegraphGroupFields(text string) (string, string) {
	fields, subtitles := "", ""
	for _, line := range strings.Split(text, "\n") {
		label, value, ok := strings.Cut(strings.ReplaceAll(strings.TrimSpace(line), "：", ":"), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) > 1024 {
			continue
		}
		switch strings.Join(strings.Fields(label), "") {
		case "字幕", "主要字幕", "其他字幕":
			languages := make([]string, 0, 8)
			for _, language := range strings.Split(value, "/") {
				language = strings.TrimSpace(language)
				if language != "" && language != "未知语言" && language != "未知語言" {
					languages = append(languages, language)
				}
			}
			if len(languages) > 0 {
				if subtitles != "" {
					subtitles += "/"
				}
				subtitles += strings.Join(languages, "/")
			}
		case "质量", "質量", "画质", "畫質", "规格", "規格", "版本", "平台", "来源", "來源", "音轨", "音軌":
			fields += "\n[" + value + "]"
		}
	}
	return fields, subtitles
}

type telegraphBlock struct {
	text      string
	plainText string
	hrefs     []string
}

func readTelegraphBlock(node telegraphNode) telegraphBlock {
	var block telegraphBlock
	var text, plainText strings.Builder
	hasAnchor := false
	var read func(telegraphNode, bool)
	read = func(node telegraphNode, inAnchor bool) {
		if node.tag == "br" {
			text.WriteByte('\n')
			if hasAnchor && !inAnchor {
				plainText.WriteByte('\n')
			}
			return
		}
		if node.tag == "a" {
			if node.href != "" {
				block.hrefs = append(block.hrefs, node.href)
			}
			// 分享只使用锚点实际地址，展示文字中的其他地址不参与识别。
			if !hasAnchor {
				hasAnchor = true
				plainText.WriteString(text.String())
			}
			plainText.WriteByte(' ')
			inAnchor = true
		}
		text.WriteString(node.text)
		if hasAnchor && !inAnchor {
			plainText.WriteString(node.text)
		}
		for _, child := range node.children {
			read(child, inAnchor)
		}
	}
	read(node, false)
	block.text = text.String()
	block.plainText = block.text
	if hasAnchor {
		block.plainText = plainText.String()
	}
	return block
}

func telegraphTitleMatches(title string, target ChannelSearchTarget) bool {
	if target.TMDBID != "" {
		ids := telegraphTMDBID.FindAllStringSubmatch(title, -1)
		if len(ids) > 0 {
			for _, match := range ids {
				if match[1] != target.TMDBID {
					return false
				}
			}
		}
	}
	// TMDB 数字没有媒体类型信息；即使数字相同，也须匹配页面声明的片名。
	name := strings.TrimSpace(telegraphTMDBID.ReplaceAllString(title, ""))
	name = telegraphTitleYear.ReplaceAllString(name, "")
	key := mediafacts.NameKey(target.Title)
	if key == "" {
		return false
	}
	if mediafacts.NameKey(name) == key {
		return true
	}
	if target.Kind != "tv" {
		return false
	}
	// 仅剥离有空白边界的尾部集数说明，片名仍须精确一致，不能按包含关系归入续作。
	trimmed := telegraphTitleProgress.ReplaceAllString(name, "")
	if trimmed == name {
		return false
	}
	trimmed = telegraphTitleYear.ReplaceAllString(trimmed, "")
	return mediafacts.NameKey(trimmed) == key
}

func telegraphTMDBLinkConflicts(href string, target ChannelSearchTarget) bool {
	match := telegraphTMDBURL.FindStringSubmatch(href)
	return match != nil && target.TMDBID != "" && (!strings.EqualFold(match[1], target.Kind) || match[2] != target.TMDBID)
}
