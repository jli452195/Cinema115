package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"strm115/backend/internal/client/clouddrive2"
)

var ErrCD2Unavailable = errors.New("CloudDrive2 连接未启用")
var ErrCD2Changed = errors.New("CloudDrive2 连接已变化，请重新选择目录")

type CD2Input struct {
	Enabled bool    `json:"enabled"`
	URL     string  `json:"url"`
	Token   *string `json:"token"`
}

type CD2View struct {
	ID              string `json:"id"`
	Revision        uint64 `json:"revision"`
	Enabled         bool   `json:"enabled"`
	URL             string `json:"url"`
	TokenConfigured bool   `json:"tokenConfigured"`
}

type cd2Document struct {
	Version  int                `json:"version"`
	ID       string             `json:"id"`
	Revision uint64             `json:"revision"`
	Enabled  bool               `json:"enabled"`
	Config   clouddrive2.Config `json:"config"`
}

type cd2Client interface {
	clouddrive2.Filesystem
	Probe(context.Context) clouddrive2.Probe
	Close() error
}

type cd2Pool struct {
	client     cd2Client
	references int
}

type CD2Connections struct {
	mu        sync.Mutex
	store     accountSecretStore
	doc       cd2Document
	pool      map[uint64]*cd2Pool
	closed    bool
	newClient func(clouddrive2.Config) (cd2Client, error)
	eventHub  *cd2EventHub
}

type CD2Lease struct {
	ID       string
	Revision uint64
	Client   clouddrive2.Filesystem
	once     sync.Once
	release  func() error
	err      error
}

// Close 仅释放本次任务的引用，旧配置连接会在最后一个任务结束时关闭。
func (l *CD2Lease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.release != nil {
			l.err = l.release()
		}
	})
	return l.err
}

func NewCD2Connections(store accountSecretStore) (*CD2Connections, error) {
	s := &CD2Connections{store: store, doc: cd2Document{Version: 1, ID: "cd2", Revision: 1}, pool: make(map[uint64]*cd2Pool), newClient: func(config clouddrive2.Config) (cd2Client, error) { return clouddrive2.New(config) }}
	if raw := store.Value(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &s.doc); err != nil || s.doc.Version != 1 || s.doc.ID == "" || s.doc.Revision == 0 {
			return nil, errors.New("CloudDrive2 配置无法读取")
		}
		prepared, err := s.prepareLocked(CD2Input{Enabled: s.doc.Enabled, URL: s.doc.Config.URL})
		if err != nil {
			return nil, fmt.Errorf("读取 CloudDrive2 配置: %w", err)
		}
		s.doc = prepared
	}
	return s, nil
}

func cd2View(doc cd2Document) CD2View {
	return CD2View{ID: doc.ID, Revision: doc.Revision, Enabled: doc.Enabled, URL: doc.Config.URL, TokenConfigured: doc.Config.Token != ""}
}

func (s *CD2Connections) View() CD2View { s.mu.Lock(); defer s.mu.Unlock(); return cd2View(s.doc) }

func (s *CD2Connections) prepareLocked(input CD2Input) (cd2Document, error) {
	doc := s.doc
	doc.Enabled, doc.Config.URL = input.Enabled, strings.TrimSpace(input.URL)
	if input.Token != nil {
		doc.Config.Token = strings.TrimSpace(*input.Token)
	}
	if doc.Config.URL == "" && doc.Config.Token == "" && !doc.Enabled {
		return doc, nil
	}
	config, err := clouddrive2.Prepare(doc.Config)
	if err != nil {
		return cd2Document{}, err
	}
	if doc.Enabled && config.Token == "" {
		return cd2Document{}, fmt.Errorf("%w：启用连接前请填写 API Token", clouddrive2.ErrInput)
	}
	doc.Config = config
	return doc, nil
}

func (s *CD2Connections) Save(ctx context.Context, input CD2Input) (CD2View, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return CD2View{}, ErrCD2Unavailable
	}
	if err := ctx.Err(); err != nil {
		return CD2View{}, err
	}
	doc, err := s.prepareLocked(input)
	if err != nil {
		return CD2View{}, err
	}
	if doc.Config != s.doc.Config {
		doc.Revision++
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return CD2View{}, fmt.Errorf("编码 CloudDrive2 配置: %w", err)
	}
	if err := s.store.Save(string(raw)); err != nil {
		return CD2View{}, fmt.Errorf("保存 CloudDrive2 配置: %w", err)
	}
	s.doc = doc
	if s.eventHub != nil {
		select {
		case s.eventHub.wake <- struct{}{}:
		default:
		}
	}
	for revision, entry := range s.pool {
		if entry.references == 0 && (!doc.Enabled || revision != doc.Revision) {
			_ = entry.client.Close()
			delete(s.pool, revision)
		}
	}
	return cd2View(doc), nil
}

func (s *CD2Connections) Test(ctx context.Context, input CD2Input) (clouddrive2.Probe, error) {
	s.mu.Lock()
	input.Enabled = true
	doc, err := s.prepareLocked(input)
	factory := s.newClient
	s.mu.Unlock()
	if err != nil {
		return clouddrive2.Probe{}, err
	}
	client, err := factory(doc.Config)
	if err != nil {
		return clouddrive2.Probe{}, err
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return client.Probe(ctx), nil
}

func (s *CD2Connections) Acquire(ctx context.Context, connectionID string, expectedRevision uint64) (*CD2Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed || !s.doc.Enabled || s.doc.Config.Token == "" {
		return nil, ErrCD2Unavailable
	}
	if connectionID != "" && connectionID != s.doc.ID || expectedRevision != 0 && expectedRevision != s.doc.Revision {
		return nil, ErrCD2Changed
	}
	entry := s.pool[s.doc.Revision]
	if entry == nil {
		client, err := s.newClient(s.doc.Config)
		if err != nil {
			return nil, err
		}
		entry = &cd2Pool{client: client}
		s.pool[s.doc.Revision] = entry
	}
	entry.references++
	revision := s.doc.Revision
	return &CD2Lease{ID: s.doc.ID, Revision: revision, Client: entry.client, release: func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		entry.references--
		if entry.references == 0 && (s.closed || !s.doc.Enabled || revision != s.doc.Revision) {
			delete(s.pool, revision)
			return entry.client.Close()
		}
		return nil
	}}, nil
}

func (s *CD2Connections) Close() error {
	s.mu.Lock()
	s.closed = true
	hub := s.eventHub
	if hub != nil {
		hub.cancel()
	}
	var errs []error
	for revision, entry := range s.pool {
		if entry.references == 0 {
			errs = append(errs, entry.client.Close())
			delete(s.pool, revision)
		}
	}
	s.mu.Unlock()
	if hub != nil {
		<-hub.done
	}
	return errors.Join(errs...)
}

type CD2Directory struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
}

type CD2DirectoryPage struct {
	ConnectionID string         `json:"connectionId"`
	Revision     uint64         `json:"revision"`
	Path         string         `json:"path"`
	ParentPath   *string        `json:"parentPath"`
	ReadOnly     bool           `json:"readOnly"`
	Directories  []CD2Directory `json:"directories"`
	Truncated    bool           `json:"truncated"`
}

func (s *CD2Connections) Directories(ctx context.Context, directory string, revision uint64) (CD2DirectoryPage, error) {
	directory, err := clouddrive2.CleanPath(directory)
	if err != nil {
		return CD2DirectoryPage{}, err
	}
	lease, err := s.Acquire(ctx, "", revision)
	if err != nil {
		return CD2DirectoryPage{}, err
	}
	defer lease.Close()
	info, err := lease.Client.Stat(ctx, directory, false)
	if err != nil {
		return CD2DirectoryPage{}, err
	}
	if !info.Directory {
		return CD2DirectoryPage{}, fmt.Errorf("%w：所选路径不是目录", clouddrive2.ErrInput)
	}
	files, err := lease.Client.List(ctx, directory, false)
	if err != nil {
		return CD2DirectoryPage{}, err
	}
	page := CD2DirectoryPage{ConnectionID: lease.ID, Revision: lease.Revision, Path: directory, ReadOnly: info.ReadOnly, Directories: make([]CD2Directory, 0, len(files))}
	if directory != "/" {
		parent := path.Dir(directory)
		page.ParentPath = &parent
	}
	for _, file := range files {
		if file.Directory {
			page.Directories = append(page.Directories, CD2Directory{ID: file.ID, Name: file.Name, Path: file.Path, ReadOnly: file.ReadOnly})
		}
	}
	sort.Slice(page.Directories, func(i, j int) bool {
		return strings.ToLower(page.Directories[i].Name) < strings.ToLower(page.Directories[j].Name)
	})
	if len(page.Directories) > 2000 {
		page.Directories, page.Truncated = page.Directories[:2000], true
	}
	return page, nil
}
