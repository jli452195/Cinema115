package httpapi

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// RegisterFrontend 为容器部署托管前端；本地开发不配置此目录，继续使用 Vite。
func RegisterFrontend(r *gin.Engine, directory string) error {
	assets := os.DirFS(directory)
	index, err := fs.Stat(assets, "index.html")
	if err != nil {
		return fmt.Errorf("读取前端入口: %w", err)
	}
	if !index.Mode().IsRegular() {
		return fmt.Errorf("前端入口不是普通文件")
	}
	r.NoRoute(func(c *gin.Context) {
		// 未知 API 必须返回 JSON 404，避免前端把 HTML 当作 JSON 解析。
		if c.Request.URL.Path == "/api" || strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Header("Allow", "GET, HEAD")
			c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "请求方法不支持"})
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+c.Request.URL.Path), "/")
		info, err := fs.Stat(assets, name)
		if err != nil || !info.Mode().IsRegular() {
			// 缺失的静态资源不回退到首页，也不展示目录列表。
			if name == "assets" || strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
				c.Status(http.StatusNotFound)
				return
			}
			name = "index.html"
		}
		if strings.HasPrefix(name, "assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Header("Cache-Control", "no-cache")
		}
		// Gin 的 NoRoute 初始状态为 404，发送页面前恢复成功状态。
		c.Status(http.StatusOK)
		http.ServeFileFS(c.Writer, c.Request, assets, name)
	})
	return nil
}
