package tguser

import (
	"errors"
	"regexp"
	"strings"
)

const telegraphShareLimit = 1000
const telegraphShareTitle = "115 分享"

var telegraphSharePassword = regexp.MustCompile(passwordPattern.String() + `\b`)
var telegraphShareSpecLabel = strings.NewReplacer("画质规格", "规格", "畫質規格", "規格")

type telegraphShares struct {
	groups       []ChannelSourceGroup
	current      ChannelSourceGroup
	fields       strings.Builder
	seen         map[string]struct{}
	coded        map[string]struct{}
	afterDivider bool
}

func newTelegraphShares() *telegraphShares {
	return &telegraphShares{
		current: ChannelSourceGroup{Title: telegraphShareTitle},
		seen:    make(map[string]struct{}),
		coded:   make(map[string]struct{}),
	}
}

func (s *telegraphShares) startGroup(title string, afterDivider bool) {
	s.flush()
	s.current = ChannelSourceGroup{Title: telegraphGroupTitle(title, telegraphShareTitle)}
	s.fields.Reset()
	s.afterDivider = afterDivider
}

func (s *telegraphShares) flush() {
	if len(s.current.Links) == 0 {
		return
	}
	// 单链接的公共元数据函数会返回卡片元数据，这里保留在本链接上。
	if metadata := channelMetadata(s.current.Title, s.fields.String(), s.current.Links); metadata != nil {
		s.current.Links[0].Metadata = metadata
	}
	s.groups = append(s.groups, s.current)
}

func (s *telegraphShares) add(node telegraphNode, block telegraphBlock) error {
	if s.afterDivider && (strings.TrimSpace(block.text) != "" || len(block.hrefs) > 0) {
		if node.tag == "p" && telegraphBoldParagraph(node) {
			s.current.Title = telegraphGroupTitle(block.text, telegraphShareTitle)
		}
		s.afterDivider = false
	}
	if node.tag == "p" {
		s.fields.WriteByte('\n')
		s.fields.WriteString(telegraphShareSpecLabel.Replace(block.text))
	}

	links, otherLinks := telegraphParagraphShares(block.plainText, block.hrefs)
	if len(links) == 1 && !otherLinks && !strings.Contains(links[0], "?password=") {
		// 同段仅有一条分享和一个提取码时才关联，避免把其他网盘的提取码接到 115 分享上。
		if password := telegraphParagraphPassword(block.plainText); password != "" {
			links[0] += "?password=" + password
		}
	}
	for _, link := range links {
		if _, exists := s.seen[link]; exists {
			continue
		}
		if len(s.seen) >= telegraphShareLimit {
			return errors.New("Telegraph 页面分享过多，请到原帖查看")
		}
		if base, _, hasCode := strings.Cut(link, "?password="); hasCode {
			s.coded[base] = struct{}{}
		}
		s.seen[link] = struct{}{}
		s.current.Links = append(s.current.Links, ChannelLink{Type: "115", URL: link})
	}
	return nil
}

func (s *telegraphShares) finish() []ChannelSourceGroup {
	s.flush()
	groups := s.groups[:0]
	for _, group := range s.groups {
		links := group.Links[:0]
		for _, link := range group.Links {
			// 段内或其他版本存在同一分享的明确提取码时，隐藏重复的无密码入口。
			if _, replaced := s.coded[link.URL]; !replaced {
				links = append(links, link)
			}
		}
		if len(links) > 0 {
			group.ID = telegraphGroupID(len(groups), "115/"+group.Title)
			group.Links = links
			groups = append(groups, group)
		}
	}
	return groups
}

func telegraphParagraphShares(text string, hrefs []string) ([]string, bool) {
	var links []string
	otherLinks := false
	seen := make(map[string]struct{})
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if len(raw) < 4 || !strings.EqualFold(raw[:4], "http") {
			return
		}
		link := shareLink(raw)
		if link == "" {
			otherLinks = true
			return
		}
		if _, exists := seen[link]; !exists {
			seen[link] = struct{}{}
			links = append(links, link)
		}
	}
	for _, href := range hrefs {
		add(href)
	}
	for _, raw := range channelURLPattern.FindAllString(text, -1) {
		if len(raw) < 4 || !strings.EqualFold(raw[:4], "http") {
			continue
		}
		// 相邻正文链接可能只用中文标点隔开，同段的多条链接都要计入密码关联判断。
		for _, piece := range strings.FieldsFunc(raw, func(r rune) bool { return strings.ContainsRune(",;，。；、！？（）「」【】", r) }) {
			add(strings.TrimRight(piece, ".!"))
		}
	}
	return links, otherLinks
}

func telegraphParagraphPassword(text string) string {
	password := ""
	for _, match := range telegraphSharePassword.FindAllStringSubmatch(text, -1) {
		if password != "" && password != match[1] {
			return ""
		}
		password = match[1]
	}
	return password
}

func telegraphBoldParagraph(node telegraphNode) bool {
	strong := false
	for _, child := range node.children {
		if child.tag == "" && strings.TrimSpace(child.text) == "" {
			continue
		}
		if child.tag != "strong" || strong {
			return false
		}
		strong = true
	}
	return strong
}
