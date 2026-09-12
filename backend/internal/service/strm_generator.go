package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"strm115/backend/internal/client/p115"
)

const DefaultVideoExtensions = "mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,m2ts,webm"

var ErrGenerationInput = errors.New("生成配置无效")
var ErrNoMedia = errors.New("没有符合条件的视频，请检查分享目录、视频后缀和最小文件大小")

type ShareLister interface {
	List(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error)
}

// GenerateInput 是网页和后续机器人共用的生成参数，不含管理员令牌和网盘凭证。
type GenerateInput struct {
	URL             string `json:"url"`
	Password        string `json:"password"`
	CID             string `json:"cid"`
	OutputDir       string `json:"outputDir"`
	BaseURL         string `json:"baseUrl"`
	VideoExtensions string `json:"videoExtensions"`
	MinSizeMB       int64  `json:"minSizeMB"`
	Recursive       bool   `json:"recursive"`
}

type GeneratedSTRM struct {
	Path    string
	Content string
}
type GenerationResult struct {
	ReshareURL      string
	TempDirectoryID string
	Cleaned         bool
	Directory       string
	Files           []GeneratedSTRM
	Skipped         int
	Written         int
}

// STRMGenerator 只读取分享目录和写入播放入口，不转存或下载媒体文件。
type STRMGenerator struct {
	Client ShareLister
	Cookie func() string
	Root   string
}

func generationInputError(message string) error {
	return fmt.Errorf("%w：%s", ErrGenerationInput, message)
}

// ValidateBaseURL 校验写入 STRM 的服务地址；该地址不会作为后端请求目标。
func ValidateBaseURL(raw string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", generationInputError("播放地址应为完整的 HTTP/HTTPS 服务地址，不含账号、查询参数或锚点")
	}
	return base, nil
}

// safeSTRMName 将不可信的分享名称转换为单个 Windows/Linux 兼容文件名。
func safeSTRMName(raw string) string {
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, raw)
	runes := []rune(name)
	if len(runes) > 90 {
		name = string(runes[:90])
	}
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "未命名"
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '0' && stem[3] <= '9') {
		name = "_" + name
	}
	return name
}

func (g *STRMGenerator) Generate(ctx context.Context, input GenerateInput) (*GenerationResult, error) {
	share, err := p115.ParseShareURL(input.URL)
	if err != nil {
		return nil, generationInputError(err.Error())
	}
	if input.Password != "" {
		share.Password = strings.TrimSpace(input.Password)
	}
	base, err := ValidateBaseURL(input.BaseURL)
	if err != nil {
		return nil, err
	}
	if input.MinSizeMB < 0 || input.MinSizeMB > 1<<30 {
		return nil, generationInputError("最小文件大小超出范围")
	}
	directory, err := ResolveOutputDirectory(g.Root, input.OutputDir)
	if err != nil {
		return nil, err
	}
	cid := strings.TrimSpace(input.CID)
	if cid == "" {
		cid = "0"
	}
	if _, err := strconv.ParseUint(cid, 10, 64); err != nil {
		return nil, generationInputError("起始目录 ID 应为数字，分享根目录使用 0")
	}
	extensions := make(map[string]bool)
	rawExtensions := input.VideoExtensions
	if strings.TrimSpace(rawExtensions) == "" {
		rawExtensions = DefaultVideoExtensions
	}
	if len(rawExtensions) > 1024 {
		return nil, generationInputError("视频后缀配置过长")
	}
	for _, ext := range strings.Split(strings.ReplaceAll(rawExtensions, "，", ","), ",") {
		ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
		if ext == "" {
			continue
		}
		for _, r := range ext {
			if r < 'a' || r > 'z' {
				if r < '0' || r > '9' {
					return nil, generationInputError("视频后缀只能包含英文字母和数字，以逗号分隔")
				}
			}
		}
		extensions["."+ext] = true
	}
	if len(extensions) == 0 {
		return nil, generationInputError("请至少填写一个视频后缀")
	}
	cookie := g.Cookie()
	if cookie == "" {
		return nil, generationInputError("请先在 115 网盘页面完成登录")
	}
	result := &GenerationResult{Files: make([]GeneratedSTRM, 0)}
	visited := make(map[string]bool)
	usedPaths := make(map[string]bool)
	entries, pages := 0, 0
	// 使用计数和深度上限防止异常分享目录导致无限分页或无限递归。
	var walk func(string, string, int) error
	walk = func(folderID, parent string, depth int) error {
		if depth > 20 {
			return fmt.Errorf("分享目录层级超过 20 层，请缩小生成范围")
		}
		if visited[folderID] {
			return fmt.Errorf("分享中出现重复目录，已停止生成，请重试")
		}
		visited[folderID] = true
		seen := make(map[string]bool)
		for offset := 0; ; {
			if err := ctx.Err(); err != nil {
				return err
			}
			pages++
			if pages > 500 {
				return fmt.Errorf("分享扫描超过 500 页，请缩小生成范围")
			}
			page, err := g.Client.List(ctx, share, folderID, offset, cookie)
			if err != nil {
				return fmt.Errorf("读取分享目录: %w", err)
			}
			if page == nil || page.Count < 0 {
				return fmt.Errorf("115 返回了无效的分享分页")
			}
			if len(page.Files) == 0 && offset < page.Count {
				return fmt.Errorf("分享分页不完整，请重试")
			}
			logGeneration(ctx, "DEBUG", fmt.Sprintf("读取分享目录第 %d 页，本页 %d 项", pages, len(page.Files)))
			for _, file := range page.Files {
				entries++
				if entries > 20000 {
					return fmt.Errorf("分享超过 20000 个条目，请缩小生成范围")
				}
				key := strconv.FormatBool(file.Directory) + ":" + file.ID
				if file.ID == "" || seen[key] {
					return fmt.Errorf("分享文件列表存在空 ID 或重复条目，请重试")
				}
				seen[key] = true
				if !file.Directory && (!extensions[strings.ToLower(path.Ext(file.Name))] || file.Size < input.MinSizeMB*(1<<20)) {
					result.Skipped++
					logGeneration(ctx, "DEBUG", "按后缀或大小过滤："+safeSTRMName(file.Name))
					continue
				}
				if file.Directory && !input.Recursive {
					continue
				}
				name := safeSTRMName(file.Name)
				if !file.Directory {
					name = safeSTRMName(strings.TrimSuffix(file.Name, path.Ext(file.Name))) + ".strm"
				}
				relative := path.Join(parent, name)
				for suffix := 2; usedPaths[strings.ToLower(relative)]; suffix++ {
					ext := path.Ext(name)
					if file.Directory {
						ext = ""
					}
					relative = path.Join(parent, strings.TrimSuffix(name, ext)+" ("+strconv.Itoa(suffix)+")"+ext)
				}
				usedPaths[strings.ToLower(relative)] = true
				if file.Directory {
					if err := walk(file.ID, relative, depth+1); err != nil {
						return err
					}
					continue
				}
				if len(result.Files) >= 10000 {
					return fmt.Errorf("单次生成最多支持 10000 个视频，请缩小范围")
				}
				content := base + "/api/?share_code=" + url.QueryEscape(share.Code) + "&id=" + url.QueryEscape(file.ID) + "&receive_code=" + url.QueryEscape(share.Password) + "\n"
				result.Files = append(result.Files, GeneratedSTRM{Path: relative, Content: content})
			}
			offset += len(page.Files)
			if offset >= page.Count {
				break
			}
		}
		return nil
	}
	if err := walk(cid, "", 0); err != nil {
		return nil, err
	}
	if len(result.Files) == 0 {
		return nil, ErrNoMedia
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result.Directory = directory
	if err := ensureSTRMDirectory(directory); err != nil {
		return result, fmt.Errorf("创建生成目录: %w", err)
	}
	for _, file := range result.Files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		destination := filepath.Join(directory, filepath.FromSlash(file.Path))
		if err := ensureSTRMDirectory(filepath.Dir(destination)); err != nil {
			return result, fmt.Errorf("创建视频子目录: %w", err)
		}
		if err := writeSTRM(ctx, destination, file.Content); err != nil {
			return result, fmt.Errorf("写入 STRM: %w", err)
		}
		result.Written++
		logGeneration(ctx, "INFO", "STRM 生成成功："+filepath.ToSlash(destination))
	}
	logGeneration(ctx, "INFO", fmt.Sprintf("STRM 生成完成：新增 %d", result.Written))
	return result, nil
}

// 先完整写入同目录临时文件，再替换目标；无需检查已存在文件，也不会截断旧内容。
func writeSTRM(ctx context.Context, destination, content string) error {
	stream, err := os.CreateTemp(filepath.Dir(destination), ".strm-*")
	if err != nil {
		return err
	}
	defer os.Remove(stream.Name())
	defer stream.Close()
	if err := stream.Chmod(0644); err != nil {
		return err
	}
	if _, err := stream.WriteString(content); err != nil {
		return err
	}
	if err := stream.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(stream.Name(), destination)
}

// ResolveOutputDirectory 将相对目录限制在媒体库根目录内，禁止绝对路径和目录穿越。
func ResolveOutputDirectory(root, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, `\:`) || strings.HasPrefix(raw, "/") {
		return "", generationInputError("请填写媒体库内的相对目录，例如 strm、strm2 或 strm/电影")
	}
	for _, segment := range strings.Split(raw, "/") {
		if segment == "" || safeSTRMName(segment) != segment {
			return "", generationInputError("目录名称无效，不能包含 ..、空目录段或特殊字符")
		}
	}
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("未配置媒体库根目录")
	}
	base, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析媒体库根目录: %w", err)
	}
	directory := filepath.Join(base, filepath.FromSlash(raw))
	relative, err := filepath.Rel(base, directory)
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return "", generationInputError("生成目录必须位于媒体库根目录内")
	}
	return directory, nil
}

// 逐层检查已有目录，避免分享子目录通过符号链接写到其他位置。
func ensureSTRMDirectory(directory string) error {
	parent := filepath.Dir(directory)
	if parent != directory {
		if err := ensureSTRMDirectory(parent); err != nil {
			return err
		}
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if createErr := os.Mkdir(directory, 0755); createErr != nil && !errors.Is(createErr, os.ErrExist) {
			return createErr
		}
		info, err = os.Lstat(directory)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("目标不是普通目录：%s", directory)
	}
	return nil
}
