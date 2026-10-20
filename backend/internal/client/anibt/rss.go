package anibt

import (
	"encoding/xml"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"strm115/backend/internal/client/animefeed"
)

const HistoryLimit = 100

var releaseID = regexp.MustCompile(`^rel_[A-Za-z0-9_-]{1,60}$`)

func RSSPath(source animefeed.Source, take int) (string, error) {
	if source.Site() != "anibt" || !source.Valid() || take < 1 || take > HistoryLimit {
		return "", animefeed.ErrInput
	}
	q := url.Values{"bgmId": {source.ItemID}, "groupSlug": {source.GroupID}}
	if take != HistoryLimit {
		q.Set("limit", strconv.Itoa(take))
	}
	return "/rss/anime.xml?" + q.Encode(), nil
}
func ParseRSS(body []byte, source animefeed.Source) ([]animefeed.SourceRelease, error) {
	var v struct {
		XMLName xml.Name `xml:"rss"`
		Channel *struct {
			Items []struct {
				Title     string `xml:"title"`
				Link      string `xml:"link"`
				GUID      string `xml:"guid"`
				Date      string `xml:"pubDate"`
				ID        string `xml:"https://anibt.net/xmlns/rss/1.0/ releaseId"`
				BgmID     string `xml:"https://anibt.net/xmlns/rss/1.0/ bgmId"`
				Group     string `xml:"https://anibt.net/xmlns/rss/1.0/ groupSlug"`
				Torrent   string `xml:"https://anibt.net/xmlns/rss/1.0/ torrentUrl"`
				Size      int64  `xml:"https://anibt.net/xmlns/rss/1.0/ fileSize"`
				Enclosure struct {
					URL string `xml:"url,attr"`
				} `xml:"enclosure"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if source.Site() != "anibt" || !source.Valid() || xml.Unmarshal(body, &v) != nil || v.XMLName.Local != "rss" || v.Channel == nil || len(v.Channel.Items) > HistoryLimit {
		return nil, ErrInvalid
	}
	out := make([]animefeed.SourceRelease, 0, len(v.Channel.Items))
	seen := make(map[string]bool, len(v.Channel.Items))
	for _, a := range v.Channel.Items {
		if !releaseID.MatchString(a.ID) || a.ID != a.GUID || a.BgmID != source.ItemID || a.Group != source.GroupID || a.Title == "" || len([]rune(a.Title)) > 1024 || a.Size < 0 || seen[a.ID] {
			return nil, ErrInvalid
		}
		seen[a.ID] = true
		if a.Link != BaseURL+"/release/"+a.ID {
			return nil, ErrInvalid
		}
		torrent := a.Torrent
		if torrent == "" {
			torrent = a.Enclosure.URL
		}
		if torrent != BaseURL+"/api/torrent/"+a.ID+".torrent" {
			return nil, ErrInvalid
		}
		// No description HTML is retained or rendered. Publication precision is
		// preserved as RSS text and interpreted by the shared time parser.
		out = append(out, animefeed.SourceRelease{PostKey: a.ID, OriginalGUID: a.GUID, Title: strings.TrimSpace(a.Title), URL: a.Link, TorrentURL: torrent, SizeBytes: a.Size, SizeRaw: strconv.FormatInt(a.Size, 10) + " B", PublishedRaw: a.Date})
	}
	return out, nil
}
func HistoryPage(source animefeed.Source, rows []animefeed.SourceRelease, take, total int, observed time.Time) animefeed.SourcePage {
	page := animefeed.SourcePage{Kind: "history", Source: source, Releases: rows, RawCount: len(rows), RequestedTake: take, ObservedAt: observed, Completeness: "partial", StopReason: "more_available"}
	if total >= 0 {
		n := total
		page.Total = &n
	}
	if total == len(rows) && len(rows) <= take {
		page.Completeness, page.StopReason = "visible_end", ""
	} else if take < HistoryLimit && len(rows) >= take {
		page.NextTake = min(HistoryLimit, take+50)
	} else {
		page.StopReason = "source_limit"
	}
	return page
}
