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
	"sync"
	"time"
	"unicode"

	"strm115/backend/internal/client/p115"
)

const DefaultVideoExtensions = "mp4,mkv,ts,iso,rmvb,avi,mov,mpeg,mpg,wmv,3gp,asf,m4v,m2ts,webm"
const DefaultMetadataExtensions = "srt,ass,ssa"

var ErrGenerationInput = errors.New("生成配置无效")
var ErrNoMedia = errors.New("没有符合条件的视频，请检查分享目录、视频后缀和最小文件大小")

// generationCookieKey 仅在转存工作流内部传递本次会话，不进入请求参数、日志或持久化记录。
type generationCookieKey struct{}

// 新任务在入口固定凭据，账号切换只影响后续任务。
func generationCookie(ctx context.Context, current func() string) string {
	if cookie, ok := ctx.Value(generationCookieKey{}).(string); ok {
		return cookie
	}
	return current()
}

type ShareLister interface {
	List(context.Context, *p115.ShareInfo, string, int, string) (*p115.SharePage, error)
}

// GenerateInput 是网页和后续机器人共用的生成参数，不含管理员令牌和网盘凭证。
type GenerateInput struct {
	Mode               string `json:"mode,omitempty"`
	URL                string `json:"url"`
	Password           string `json:"password"`
	CID                string `json:"cid"`
	OutputDir          string `json:"outputDir"`
	BaseURL            string `json:"baseUrl"`
	VideoExtensions    string `json:"videoExtensions"`
	MetadataExtensions string `json:"metadataExtensions"`
	MinSizeMB          int64  `json:"minSizeMB"`
	Recursive          bool   `json:"recursive"`
}

type GeneratedSTRM struct {
	Path    string
	Content string
}
type GeneratedMetadata struct {
	Path string
	ID   string
	Size int64
}
type GenerationResult struct {
	Cached bool `json:"-"`
	// 记录实际执行方式，供任务日志和 Telegram 回执使用。
	Mode            string `json:"-"`
	ReshareURL      string
	TempDirectoryID string
	Cleaned         bool
	Directory       string
	Files           []GeneratedSTRM
	Skipped         int
	Written         int
	Failed          int
	MetadataFiles   []GeneratedMetadata
	MetadataWritten int
	MetadataFailed  int
	ConflictRenamed int
}

var generatedFileWriteMu sync.Mutex

// STRMGenerator 读取分享目录、写入播放入口及选定的配套元数据，不转存或下载视频。
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

func (g *STRMGenerator) Generate(ctx context.Context, input GenerateInput) (output *GenerationResult, err error) {
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
	metadataExtensions, err := parseMetadataExtensions(input.MetadataExtensions, extensions)
	if err != nil {
		return nil, err
	}
	cookie, pinned := ctx.Value(generationCookieKey{}).(string)
	if !pinned {
		cookie = g.Cookie()
	}
	if cookie == "" {
		return nil, generationInputError("请在分享基础设置中选择已登录的 115 账号配置")
	}
	result := &GenerationResult{Files: make([]GeneratedSTRM, 0)}
	started := time.Now()
	defer func() {
		label := "STRM 生成完成！"
		remainder := ""
		if err != nil {
			label = "STRM 生成中断！"
			remainder = fmt.Sprintf("，未处理：%d", len(result.Files)-result.Written-result.Failed)
		}
		logGeneration(ctx, "INFO", fmt.Sprintf("%s成功：%d，失败：%d%s，耗时 %.2f 秒", label, result.Written, result.Failed, remainder, time.Since(started).Seconds()))
	}()
	visited := make(map[string]bool)
	usedPaths := make(map[string]bool)
	rootIDs, scoped := ctx.Value(generationRootIDsKey{}).([]string)
	firstPage, hasFirstPage := ctx.Value(generationFirstPageKey{}).(generationFirstPage)
	firstPageUsed := false
	snapshot, hasSnapshot := ctx.Value(generationSnapshotKey{}).(generationSnapshot)
	hasSnapshot = hasSnapshot && snapshot.ShareCode == share.Code && snapshot.RootCID == cid
	selectedRoots := make(map[string]bool, len(rootIDs))
	for _, id := range rootIDs {
		selectedRoots[id] = false
	}
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
			var page *p115.SharePage
			var err error
			if hasSnapshot {
				var ok bool
				page, ok = snapshot.Pages[generationSnapshotPageKey{CID: folderID, Offset: offset}]
				if !ok {
					return fmt.Errorf("已确认快照缺少目录页 %s/%d", folderID, offset)
				}
			} else if hasFirstPage && !firstPageUsed && firstPage.Page != nil && firstPage.ShareCode == share.Code && firstPage.CID == folderID && offset == 0 {
				page, firstPageUsed = firstPage.Page, true
			} else {
				page, err = g.Client.List(ctx, share, folderID, offset, cookie)
			}
			if err != nil {
				return fmt.Errorf("读取分享目录: %w", err)
			}
			if page == nil || page.Count < 0 {
				return fmt.Errorf("115 返回了无效的分享分页")
			}
			if len(page.Files) == 0 && offset < page.Count {
				return fmt.Errorf("分享分页不完整，请重试")
			}
			logGeneration(ctx, "INFO", fmt.Sprintf("读取分享目录第 %d 页，本页 %d 项", pages, len(page.Files)))
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
				selected := true
				if scoped && depth == 0 {
					_, selected = selectedRoots[file.ID]
					if selected {
						selectedRoots[file.ID] = true
					}
				}
				isMetadata := !file.Directory && metadataExtensions[strings.ToLower(path.Ext(file.Name))]
				if !file.Directory && !isMetadata && (!extensions[strings.ToLower(path.Ext(file.Name))] || file.Size < input.MinSizeMB*(1<<20)) {
					if selected {
						result.Skipped++
					}
					logGeneration(ctx, "DEBUG", "按后缀或大小过滤："+safeSTRMName(file.Name))
					continue
				}
				if file.Directory && !input.Recursive {
					continue
				}
				name := safeSTRMName(file.Name)
				if !file.Directory && !isMetadata {
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
				// 先为其他来源的同名根条目保留名称，使分来源生成和重试的路径一致。
				if !selected {
					continue
				}
				if file.Directory {
					if err := walk(file.ID, relative, depth+1); err != nil {
						return err
					}
					continue
				}
				if isMetadata {
					if file.Size > p115.MaxMetadataSize {
						return fmt.Errorf("元数据文件超过 32 MB：%s", safeSTRMName(file.Name))
					}
					result.MetadataFiles = append(result.MetadataFiles, GeneratedMetadata{Path: relative, ID: file.ID, Size: file.Size})
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
	for _, found := range selectedRoots {
		if !found {
			return nil, fmt.Errorf("新分享缺少本来源的根条目，已停止生成")
		}
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
	reservedPaths := make(map[string]bool, len(result.Files)+len(result.MetadataFiles))
	for _, file := range result.Files {
		reservedPaths[strings.ToLower(file.Path)] = true
	}
	for _, file := range result.MetadataFiles {
		reservedPaths[strings.ToLower(file.Path)] = true
	}
	for i := range result.Files {
		file := &result.Files[i]
		if err := ctx.Err(); err != nil {
			return result, err
		}
		delete(reservedPaths, strings.ToLower(file.Path))
		originalPath := file.Path
		actualPath, renamed, err := writeGeneratedFile(ctx, directory, file.Path, file.Content, reservedPaths)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				result.Failed++
			}
			return result, fmt.Errorf("写入 STRM: %w", err)
		}
		file.Path = actualPath
		reservedPaths[strings.ToLower(actualPath)] = true
		if renamed {
			result.ConflictRenamed++
			logGeneration(ctx, "WARNING", fmt.Sprintf("同名目标内容不同，已保留旧文件并改名：%s -> %s", originalPath, actualPath))
		}
		result.Written++
		logGeneration(ctx, "DEBUG", "STRM 生成成功："+filepath.ToSlash(filepath.Join(directory, filepath.FromSlash(actualPath))))
		// INFO 每成功写入十个文件更新一次，最后不足十个也报告完整进度。
		if result.Written%10 == 0 || result.Written == len(result.Files) {
			logGeneration(ctx, "INFO", fmt.Sprintf("STRM 生成成功：%d/%d", result.Written, len(result.Files)))
		}
	}
	logGeneration(ctx, "DEBUG", fmt.Sprintf("STRM 生成完成：新增 %d", result.Written))
	if metadataErr := g.copyMetadata(ctx, share, cookie, directory, result, reservedPaths); metadataErr != nil {
		if errors.Is(metadataErr, context.Canceled) || errors.Is(metadataErr, context.DeadlineExceeded) {
			return result, metadataErr
		}
		logGeneration(ctx, "WARNING", fmt.Sprintf("STRM 已生成，元数据部分失败（%d/%d）；再次提交同一分享会继续重试：%v", result.MetadataFailed, len(result.MetadataFiles), metadataErr))
	}
	return result, nil
}

// writeGeneratedFile 保留内容不同的已有文件；相同内容直接复用，否则选择稳定的数字后缀。
func writeGeneratedFile(ctx context.Context, directory, relative, content string, reserved map[string]bool) (string, bool, error) {
	generatedFileWriteMu.Lock()
	defer generatedFileWriteMu.Unlock()
	for suffix := 1; suffix <= 10000; suffix++ {
		candidate := relative
		if suffix > 1 {
			ext := path.Ext(relative)
			candidate = strings.TrimSuffix(relative, ext) + " (" + strconv.Itoa(suffix) + ")" + ext
		}
		if reserved[strings.ToLower(candidate)] {
			continue
		}
		destination := filepath.Join(directory, filepath.FromSlash(candidate))
		info, err := os.Lstat(destination)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := ensureSTRMDirectory(filepath.Dir(destination)); err != nil {
				return "", false, err
			}
			if err := writeSTRM(ctx, destination, content); err != nil {
				return "", false, err
			}
			return candidate, candidate != relative, nil
		case err != nil:
			return "", false, err
		case info.Mode().IsRegular():
			existing, readErr := os.ReadFile(destination)
			if readErr != nil {
				return "", false, readErr
			}
			if string(existing) == content {
				return candidate, candidate != relative, nil
			}
		}
	}
	return "", false, fmt.Errorf("同名输出超过 10000 个：%s", relative)
}

// 先完整写入同目录临时文件，再替换目标；无需检查已存在文件，也不会截断旧内容。
func writeSTRM(ctx context.Context, destination, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if info, err := os.Lstat(destination); err == nil && info.Mode().IsRegular() {
		existing, readErr := os.ReadFile(destination)
		if readErr == nil && string(existing) == content {
			return nil
		}
	}
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

// ResolveOutputDirectory accepts "." as an explicit selection of the configured
// media root. Empty/missing selections, absolute paths and traversal stay invalid.
func ResolveOutputDirectory(root, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, `\:`) || strings.HasPrefix(raw, "/") {
		return "", generationInputError("请填写媒体库内的相对目录，例如 strm、strm2 或 strm/电影")
	}
	for _, segment := range strings.Split(raw, "/") {
		if raw == "." {
			break
		}
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
	if err != nil || !filepath.IsLocal(relative) || (relative == "." && raw != ".") {
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
