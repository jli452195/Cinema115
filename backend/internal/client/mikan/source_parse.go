package mikan

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/url"
	"strings"
)

func sourceURL(raw string, torrent bool) (string, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.EscapedPath() != u.Path ||
		(u.IsAbs() && (u.Scheme != "https" || u.Host != "mikanani.me")) || (!u.IsAbs() && u.Host != "") {
		return "", ""
	}
	if torrent {
		if !torrentPath.MatchString(u.Path) {
			return "", ""
		}
		return BaseURL + u.Path, ""
	}
	matches := episodePath.FindStringSubmatch(u.Path)
	if len(matches) != 2 {
		return "", ""
	}
	key := strings.ToLower(matches[1])
	return BaseURL + "/Home/Episode/" + key, key
}

func parseSourceHTML(body []byte, in SourceRequest) (SourcePage, error) {
	result, err := parseWithOptions(bytes.NewReader(body), in.Source.ItemID, in.Source.GroupID, in.Take, true)
	if err != nil {
		return SourcePage{}, err
	}
	if result.RawCount > in.MaxRows {
		return SourcePage{}, &LimitError{Kind: "rows"}
	}
	page := SourcePage{Releases: make([]SourceRelease, 0, len(result.Items)), RawCount: result.RawCount, RequestedTake: in.Take}
	if in.Kind == "entry" {
		if result.Bangumi == nil || result.Bangumi.Title == "" {
			return SourcePage{}, ErrParse
		}
		page.Entry = &SourceEntry{ID: in.Source.ItemID, Title: result.Bangumi.Title, CoverURL: result.Bangumi.CoverURL, Groups: result.Groups, BangumiIDs: result.BangumiIDs}
		// Entry metadata confirms available groups. Its mixed-group resource
		// rows are never used as a single-group preview or baseline.
		return page, nil
	}
	if result.RawCount > in.Take {
		return SourcePage{}, ErrParse
	}
	for _, item := range result.Items {
		link, key := sourceURL(item.URL, false)
		torrent := ""
		if item.TorrentURL != "" {
			torrent, _ = sourceURL(item.TorrentURL, true)
			if torrent == "" {
				return SourcePage{}, ErrParse
			}
		}
		if link == "" || item.GroupID != in.Source.GroupID {
			return SourcePage{}, ErrParse
		}
		page.Releases = append(page.Releases, SourceRelease{PostKey: key, Title: item.Title, URL: link, TorrentURL: torrent, MagnetHint: item.Magnet, SizeRaw: item.Size, PublishedRaw: item.UpdatedAt})
	}
	page.Completeness, page.StopReason = "partial", "more_available"
	if page.RawCount < in.Take {
		page.Completeness, page.StopReason = "visible_end", ""
	} else if in.Take == AnimeMaxRows {
		page.StopReason = "limit"
	} else {
		page.NextTake = min(in.Take+50, AnimeMaxRows)
	}
	return page, nil
}

type rssItem struct {
	Title   string `xml:"title"`
	GUID    string `xml:"guid"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	Torrent struct {
		Link          string `xml:"link"`
		ContentLength int64  `xml:"contentLength"`
		PubDate       string `xml:"pubDate"`
	} `xml:"https://mikanani.me/0.1/ torrent"`
	Enclosure struct {
		Type   string `xml:"type,attr"`
		URL    string `xml:"url,attr"`
		Length int64  `xml:"length,attr"`
	} `xml:"enclosure"`
}

// Validate the envelope, nesting and raw row count before allocating the item
// slice. XML directives/entities are never loaded and descriptions stay inert.
func validateSourceXML(body []byte, maxRows int) (int, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	path := make([]xml.Name, 0, 8)
	rows, channels, roots := 0, 0, 0
	fields := make(map[string]bool, 12)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, ErrParse
		}
		switch node := token.(type) {
		case xml.StartElement:
			path = append(path, node.Name)
			if len(path) > 32 {
				return 0, ErrParse
			}
			if len(path) == 1 {
				roots++
				version := ""
				for _, attr := range node.Attr {
					if attr.Name.Space == "" && attr.Name.Local == "version" {
						version = attr.Value
					}
				}
				if roots != 1 || node.Name != (xml.Name{Local: "rss"}) || version != "2.0" {
					return 0, ErrParse
				}
			}
			if len(path) == 2 && node.Name == (xml.Name{Local: "channel"}) {
				channels++
			}
			if node.Name.Local == "item" {
				if len(path) != 3 || path[1] != (xml.Name{Local: "channel"}) || node.Name.Space != "" {
					return 0, ErrParse
				}
				rows++
				clear(fields)
				if rows > maxRows {
					return 0, &LimitError{Kind: "rows"}
				}
			}
			if len(path) >= 4 && path[2] == (xml.Name{Local: "item"}) {
				critical := ""
				if len(path) == 4 {
					switch node.Name.Local {
					case "title", "guid", "link", "pubDate", "enclosure":
						if node.Name.Space != "" {
							return 0, ErrParse
						}
						critical = node.Name.Local
					}
				}
				if len(path) == 4 && node.Name == (xml.Name{Space: "https://mikanani.me/0.1/", Local: "torrent"}) {
					critical = "torrent"
				}
				if len(path) == 5 && path[3] == (xml.Name{Space: "https://mikanani.me/0.1/", Local: "torrent"}) {
					switch node.Name.Local {
					case "link", "contentLength", "pubDate":
						if node.Name.Space != "https://mikanani.me/0.1/" {
							return 0, ErrParse
						}
						critical = "torrent/" + node.Name.Local
					}
				}
				if critical != "" {
					if fields[critical] {
						return 0, ErrParse
					}
					fields[critical] = true
				}
			}
		case xml.EndElement:
			path = path[:len(path)-1]
		case xml.Directive:
			return 0, ErrParse
		case xml.ProcInst:
			if node.Target != "xml" || roots != 0 {
				return 0, ErrParse
			}
		case xml.CharData:
			if len(path) == 0 && strings.TrimSpace(string(node)) != "" {
				return 0, ErrParse
			}
		}
	}
	if roots != 1 || channels != 1 || len(path) != 0 {
		return 0, ErrParse
	}
	return rows, nil
}

func parseSourceRSS(body []byte, in SourceRequest) (SourcePage, error) {
	count, err := validateSourceXML(body, in.MaxRows)
	if err != nil {
		return SourcePage{}, err
	}
	var feed struct {
		Channel struct {
			Items []rssItem `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &feed); err != nil || len(feed.Channel.Items) != count {
		return SourcePage{}, ErrParse
	}
	page := SourcePage{Releases: make([]SourceRelease, 0, count), RawCount: count, Completeness: "partial", StopReason: "more_available"}
	seen := make(map[string]SourceRelease, count)
	for _, item := range feed.Channel.Items {
		link, key := sourceURL(item.Link, false)
		torrent, _ := sourceURL(item.Enclosure.URL, true)
		if strings.TrimSpace(item.Title) == "" || len(item.Title) > 8192 || len(item.GUID) > 8192 || len(item.PubDate) > 128 || len(item.Torrent.PubDate) > 128 ||
			link == "" || torrent == "" || item.Enclosure.Type != "application/x-bittorrent" || item.Enclosure.Length < 0 || item.Torrent.ContentLength < 0 {
			return SourcePage{}, ErrParse
		}
		if item.Torrent.Link != "" {
			if other, _ := sourceURL(item.Torrent.Link, false); other != link {
				return SourcePage{}, ErrParse
			}
		}
		if item.Enclosure.Length != 0 && item.Torrent.ContentLength != 0 && item.Enclosure.Length != item.Torrent.ContentLength {
			return SourcePage{}, ErrParse
		}
		release := SourceRelease{PostKey: key, Title: item.Title, OriginalGUID: item.GUID, URL: link, TorrentURL: torrent, SizeBytes: max(item.Enclosure.Length, item.Torrent.ContentLength), PublishedRaw: item.Torrent.PubDate, AlternatePublishedRaw: item.PubDate}
		if release.PublishedRaw == "" {
			release.PublishedRaw, release.AlternatePublishedRaw = item.PubDate, ""
		}
		if old, ok := seen[key]; ok {
			if old != release {
				return SourcePage{}, ErrParse
			}
			continue
		}
		seen[key] = release
		page.Releases = append(page.Releases, release)
	}
	return page, nil
}
