package clouddrive2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInput    = errors.New("CloudDrive2 参数无效")
	ErrNotFound = errors.New("CloudDrive2 文件不存在")
	ErrExists   = errors.New("CloudDrive2 目标已存在，不覆盖")
)

type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

type File struct {
	ID           string
	Name         string
	Path         string
	Size         int64
	Directory    bool
	ReadOnly     bool
	Modified     time.Time
	CloudName    string
	CloudAccount string
	Hashes       map[uint32]string
}

type Filesystem interface {
	List(context.Context, string, bool) ([]File, error)
	Stat(context.Context, string, bool) (File, error)
	CreateDirectory(context.Context, string, string) (File, error)
	Rename(context.Context, string, string) (string, error)
	Move(context.Context, []string, string) ([]string, error)
	Delete(context.Context, []string) error
}

type Permissions struct {
	List         bool `json:"list"`
	Read         bool `json:"read"`
	CreateFolder bool `json:"createFolder"`
	Rename       bool `json:"rename"`
	Move         bool `json:"move"`
	Copy         bool `json:"copy"`
	Delete       bool `json:"delete"`
}

type Probe struct {
	Reachable   bool         `json:"reachable"`
	Authorized  bool         `json:"authorized"`
	Listable    bool         `json:"listable"`
	Version     string       `json:"version,omitempty"`
	ElapsedMS   int64        `json:"elapsedMs"`
	Message     string       `json:"message"`
	Permissions *Permissions `json:"permissions,omitempty"`
}

func Prepare(config Config) (Config, error) {
	config.URL = strings.TrimSpace(config.URL)
	config.Token = strings.TrimSpace(config.Token)
	u, err := url.Parse(config.URL)
	if err != nil || len(config.URL) > 2048 || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return Config{}, fmt.Errorf("%w：服务地址须为 http://主机:端口 或 https://主机:端口，不包含登录信息和路径", ErrInput)
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf("%w：服务端口无效", ErrInput)
	}
	if len(config.Token) > 16384 || strings.ContainsAny(config.Token, "\r\n\t ") {
		return Config{}, fmt.Errorf("%w：请填写有效的 API Token", ErrInput)
	}
	for _, ch := range config.Token {
		if ch < 33 || ch > 126 {
			return Config{}, fmt.Errorf("%w：API Token 包含无效字符", ErrInput)
		}
	}
	u.Host = net.JoinHostPort(u.Hostname(), port)
	u.Path, u.RawPath = "", ""
	config.URL = u.String()
	return config, nil
}

func CleanPath(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") || len(value) > 16384 || strings.ContainsAny(value, "\x00\r\n\\") {
		return "", fmt.Errorf("%w：请选择 CD2 原生绝对路径", ErrInput)
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." {
			return "", fmt.Errorf("%w：目录路径包含无效层级", ErrInput)
		}
	}
	return path.Clean(value), nil
}

func cleanName(value string) error {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00\r\n") {
		return fmt.Errorf("%w：文件名称无效", ErrInput)
	}
	return nil
}
