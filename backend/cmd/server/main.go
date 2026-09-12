package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"strm115/backend/internal/bot"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/httpapi"
	"strm115/backend/internal/service"
	"sync"
	"syscall"
	"time"
)

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type qrcodeRequest struct {
	ClientType string `json:"clientType"`
}
type shareRequest struct {
	URL string `json:"url" binding:"required"`
}

var cookieStore *service.SessionStore
var p115QR = p115.NewClient()
var p115QRID string
var p115Mu sync.RWMutex

func cookieValue() string          { return cookieStore.Value() }
func setCookie(value string) error { return cookieStore.Save(value) }
func qrValue() string              { p115Mu.RLock(); defer p115Mu.RUnlock(); return p115QRID }
func setQR(value string)           { p115Mu.Lock(); defer p115Mu.Unlock(); p115QRID = value }

func main() {
	// 加载当前目录中的环境配置。
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")
	user, pass, secret := os.Getenv("ADMIN_USERNAME"), os.Getenv("ADMIN_PASSWORD"), os.Getenv("JWT_SECRET")
	if user == "" || pass == "" || secret == "" {
		panic("ADMIN_USERNAME, ADMIN_PASSWORD and JWT_SECRET must be configured")
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "../data"
	}
	var err error
	cookieStore, err = service.NewSessionStore(filepath.Join(dataDir, "p115-session.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	// 记录路由模板，避免将播放凭证写入访问日志。
	r.Use(func(c *gin.Context) {
		c.Next()
		log.Printf("%s %s %d", c.Request.Method, c.FullPath(), c.Writer.Status())
	})
	r.Use(corsMiddleware())
	r.GET("/api/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "strm115"}) })
	r.POST("/api/auth/login", func(c *gin.Context) {
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "username and password are required"})
			return
		}
		if !secureEqual(req.Username, user) || !secureEqual(req.Password, pass) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
			return
		}
		now := time.Now()
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": req.Username, "iat": now.Unix(), "exp": now.Add(24 * time.Hour).Unix()})
		signed, err := token.SignedString([]byte(secret))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": signed, "expiresIn": 86400})
	})
	admin := r.Group("/api/admin")
	admin.Use(jwtMiddleware(secret))
	admin.GET("/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"username": c.GetString("admin_username")}) })
	admin.POST("/p115/qrcode", func(c *gin.Context) {
		var req qrcodeRequest
		_ = c.ShouldBindJSON(&req)
		app := "alipaymini"
		switch req.ClientType {
		case "wechat-miniapp":
			app = "wechatmini"
		case "115-android":
			app = "115android"
		case "115-ios":
			app = "115ios"
		case "115-life-android":
			app = "android"
		case "115-life-ios":
			app = "ios"
		}
		qr, err := p115QR.CreateQRCode(c.Request.Context(), app)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		setQR(qr.ID)
		c.JSON(http.StatusOK, gin.H{"qrId": qr.ID, "qrcode": qr.ImageURL, "expiresAt": qr.ExpiresAt})
	})
	admin.GET("/p115/qrcode/status", func(c *gin.Context) {
		id := qrValue()
		if requested := c.Query("qrId"); requested != "" && requested != id {
			c.JSON(409, gin.H{"error": "二维码已更新，请重新获取"})
			return
		}
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请先获取二维码"})
			return
		}
		status, err := p115QR.CheckQRCode(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if status.Status == "success" && status.Cookie == "" {
			status.Cookie, err = p115QR.CompleteQRCodeLogin(c.Request.Context(), id)
			if err != nil {
				c.JSON(502, gin.H{"error": err.Error()})
				return
			}
		}
		if status.Cookie != "" {
			// 切换登录方式后，不允许旧二维码覆盖新保存的 Cookie。
			p115Mu.Lock()
			defer p115Mu.Unlock()
			if p115QRID != id {
				c.JSON(409, gin.H{"error": "二维码已更新，请重新获取"})
				return
			}
			if err = setCookie(status.Cookie); err != nil {
				c.JSON(500, gin.H{"error": "登录成功，但保存会话失败"})
				return
			}
			p115QRID = ""
		}
		c.JSON(http.StatusOK, gin.H{"status": status.Status, "connected": status.Cookie != ""})
	})
	admin.POST("/p115/share/parse", func(c *gin.Context) {
		var req shareRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "分享链接不能为空"})
			return
		}
		info, err := p115.ParseShareURL(req.URL)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, info)
	})
	httpapi.RegisterAccount(admin, cookieValue, func(value string) error {
		p115Mu.Lock()
		defer p115Mu.Unlock()
		if err := setCookie(value); err != nil {
			return err
		}
		p115QRID = ""
		return nil
	}, p115.NewAccountClient())
	shareAPI := httpapi.NewShareAPI(secret, cookieValue)
	if root := os.Getenv("STRM_ROOT"); root != "" {
		shareAPI.OutputRoot = root
	}
	automation, err := service.NewAutomationStore(filepath.Join(dataDir, "automation.json"), shareAPI.OutputRoot, "")
	if err != nil {
		log.Fatal(err)
	}
	shareAPI.BaseURL = func() string { return automation.Value().Generation.BaseURL }
	generator := &service.STRMGenerator{Client: shareAPI.Client, Cookie: cookieValue, Root: shareAPI.OutputRoot}
	workflow := service.NewShareWorkflow(shareAPI.Client, generator, cookieValue, automation, shareAPI.OutputRoot, dataDir)
	generationLogs := &service.GenerationLogs{}
	loggedGenerator := &service.LoggedGenerator{Generator: workflow, Logs: generationLogs}
	shareAPI.Workflow = loggedGenerator
	httpapi.RegisterLogs(admin, generationLogs)
	telegram, err := bot.NewManager(dataDir, secret, loggedGenerator, automation)
	if err != nil {
		log.Fatal(err)
	}
	httpapi.RegisterAutomation(admin, automation, func() bool { view := telegram.View(); return view.Enabled && view.Configured })
	httpapi.RegisterTelegram(admin, telegram)
	shareAPI.Register(r, admin)
	if directory := os.Getenv("FRONTEND_DIST"); directory != "" {
		if err := httpapi.RegisterFrontend(r, directory); err != nil {
			log.Fatal(err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", ":3002")
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	var workers sync.WaitGroup
	workerErrors := make(chan error, 2)
	workers.Add(1)
	go func() { defer workers.Done(); workerErrors <- server.Serve(listener) }()
	{
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := telegram.Run(ctx); err != nil {
				workerErrors <- err
			}
		}()
	}
	log.Print("Cinema115 已启动，监听 :3002")
	select {
	case <-ctx.Done():
	case err := <-workerErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("服务停止: %v", err)
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP 服务退出: %v", err)
		_ = server.Close()
	}
	workers.Wait()
}
func secureEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func jwtMiddleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		value := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
		token, err := jwt.Parse(value, func(t *jwt.Token) (any, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, jwt.ErrSignatureInvalid
			}
			return []byte(secret), nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			if name, ok := claims["sub"].(string); ok {
				c.Set("admin_username", name)
			}
		}
		c.Next()
	}
}
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, Accept, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
