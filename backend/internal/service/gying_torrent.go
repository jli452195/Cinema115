package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"strm115/backend/internal/client/gying"
)

// ReadTorrent keeps download tickets, session renewal and cache inside the site
// service. Neither the organizer nor its persisted jobs receive site cookies.
func (s *GyingService) ReadTorrent(ctx context.Context, kind, id, hash string) ([]byte, error) {
	key, err := gying.TorrentKey(kind, id, hash)
	if err != nil {
		return nil, ErrOrganizeInput
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	configured := s.client != nil && s.doc.Password != ""
	s.mu.RUnlock()
	if !configured {
		return nil, gying.ErrNotConfigured
	}
	if s.torrents == nil {
		s.torrents = newAnimeSiteCache(gyingTorrentTransport{s}, "gying")
	}
	read, _, err := s.torrents.read(ctx, key, 30*time.Minute, false, nil, func(body []byte) error {
		_, err := parseDownloadTorrent(body, hash)
		return err
	})
	return read.body, err
}

type gyingTorrentTransport struct{ site *GyingService }

// Fetch is called with opMu held, sharing the existing search/config session gate.
func (t gyingTorrentTransport) Fetch(ctx context.Context, key string, _ int64) ([]byte, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return nil, ErrOrganizeInput
	}
	s := t.site
	s.mu.RLock()
	client, doc := s.client, s.doc
	s.mu.RUnlock()
	cachedPath := ""
	for _, entry := range s.searchCache {
		if !time.Now().Before(entry.expires) {
			continue
		}
		for _, item := range entry.result.Items {
			if item.ID != parts[0]+":"+parts[1] {
				continue
			}
			for _, link := range item.Links {
				if link.HasTorrent && strings.HasPrefix(link.URL, "magnet:?xt=urn:btih:"+parts[2]) {
					cachedPath = link.TorrentPath
				}
			}
		}
	}
	body, err := client.ReadTorrent(ctx, parts[0], parts[1], parts[2], cachedPath)
	if errors.Is(err, gying.ErrForbidden) {
		if err = client.Login(ctx, doc.Username, doc.Password); err == nil {
			body, err = client.ReadTorrent(ctx, parts[0], parts[1], parts[2], "")
		}
	}
	if err != nil {
		return nil, err
	}
	doc.Cookies = client.SnapshotSession()
	if err := s.save(doc); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.doc = doc
	s.mu.Unlock()
	return body, nil
}
