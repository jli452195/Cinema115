package main

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"strm115/backend/internal/bot"
	"strm115/backend/internal/client/imagecache"
	"strm115/backend/internal/client/mikan"
	"strm115/backend/internal/client/p115"
	"strm115/backend/internal/client/tguser"
	"strm115/backend/internal/client/tmdb"
	"strm115/backend/internal/client/tmdbimage"
	"strm115/backend/internal/httpapi"
	"strm115/backend/internal/service"
	"strm115/backend/internal/storage"
	"sync"
	"syscall"
	"time"
)

type shareRequest struct {
	URL string `json:"url" binding:"required"`
}

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
	initCtx, initCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	database, err := storage.Open(initCtx, filepath.Join(dataDir, "cinema115.db"))
	if err != nil {
		initCancel()
		log.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("关闭数据库: %v", err)
		}
	}()
	accounts, err := service.OpenP115AccountsSQLite(initCtx, database, dataDir, secret)
	if err != nil {
		initCancel()
		log.Fatal(err)
	}
	migration, err := database.ImportLegacyJobs(initCtx, filepath.Join(dataDir, "reshare-jobs"))
	initCancel()
	if err != nil {
		_ = database.Close()
		log.Fatal(err)
	}
	if !migration.AlreadyDone {
		log.Printf("旧转存任务迁移完成：导入 %d，数据库已有 %d；原 JSON 保留", migration.Imported, migration.Skipped)
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
	auth := service.NewWebsiteAuth(database, user, pass, secret)
	httpapi.RegisterWebsiteAuth(r, auth)
	admin := r.Group("/api/admin")
	admin.Use(httpapi.WebsiteAuthenticated(auth), httpapi.WebsiteAdminOnly())
	admin.GET("/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"username": c.GetString("admin_username")}) })
	httpapi.RegisterWebsiteUsers(admin, auth)
	catalog := r.Group("/api/catalog")
	catalog.Use(httpapi.WebsiteAuthenticated(auth))
	tmdbService := service.NewTMDBService(tmdb.New(os.Getenv("TMDB_API_KEY")), database)
	httpapi.RegisterTMDB(admin, tmdbService)
	httpapi.RegisterTMDB(catalog, tmdbService)
	generationLogs := &service.GenerationLogs{}
	httpapi.RegisterMikan(admin, service.NewMikanService(mikan.New(), generationLogs))
	gyingStore, err := service.NewSessionStore(filepath.Join(dataDir, "gying-config.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	gyingService, err := service.NewGyingService(gyingStore)
	if err != nil {
		log.Fatal(err)
	}
	gyingService.Logs = generationLogs
	httpapi.RegisterGying(admin, gyingService, tmdbService, generationLogs)
	imageCacheBytes := tmdbimage.DefaultMaxBytes
	if configured := strings.TrimSpace(os.Getenv("TMDB_IMAGE_CACHE_MAX_GB")); configured != "" {
		gigabytes, parseErr := strconv.ParseInt(configured, 10, 64)
		if parseErr != nil || gigabytes < 1 || gigabytes > 1000 {
			log.Fatal("TMDB_IMAGE_CACHE_MAX_GB 必须是 1 到 1000 之间的整数")
		}
		imageCacheBytes = gigabytes << 30
	}
	tmdbImages, err := tmdbimage.New(filepath.Join(dataDir, "tmdb-images"), imageCacheBytes)
	if err != nil {
		log.Fatal(err)
	}
	httpapi.RegisterTMDBImages(r, tmdbImages)
	mikanImages, err := imagecache.NewMikan(filepath.Join(dataDir, "mikan-images"))
	if err != nil {
		log.Fatal(err)
	}
	httpapi.RegisterMikanImages(r, mikanImages)
	gyingImages, err := imagecache.NewGying(filepath.Join(dataDir, "gying-images"))
	if err != nil {
		log.Fatal(err)
	}
	httpapi.RegisterGyingImages(r, gyingImages)
	embySecrets, err := service.NewSessionStore(filepath.Join(dataDir, "emby-config.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	embyService, err := service.NewEmbyService(embySecrets, database)
	if err != nil {
		log.Fatal(err)
	}
	embyService.Logs = generationLogs
	httpapi.RegisterEmby(r, admin, embyService)
	httpapi.RegisterWebsiteCatalog(catalog, embyService)
	recentUpdates := &service.RecentUpdatesService{Source: embyService, Metadata: tmdbService}
	httpapi.RegisterRecentUpdates(admin, recentUpdates)
	httpapi.RegisterRecentUpdates(catalog, recentUpdates)
	resourceMatcher := service.NewResourceMatchService(tmdbService, embyService)
	httpapi.RegisterResourceMatch(admin, resourceMatcher)
	httpapi.RegisterResourceMatch(catalog, resourceMatcher)
	member := r.Group("/api/me", httpapi.WebsiteAuthenticated(auth))
	follows := &service.FollowService{Store: database, TMDB: tmdbService, Library: embyService, ResourceMatcher: resourceMatcher}
	httpapi.RegisterFollows(member, follows)
	httpapi.RegisterWebsiteNotifications(member, database)
	httpapi.RegisterFollowAdmin(admin, follows)
	httpapi.RegisterP115Accounts(admin, accounts, p115.NewAccountClient(), p115.NewClient())
	cloudDownloadStore, err := service.NewSessionStore(filepath.Join(dataDir, "cloud-download-config.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	cloudDownloadSettings, err := service.NewCloudDownloadSettings(cloudDownloadStore)
	if err != nil {
		log.Fatal(err)
	}
	cloudDownloadSettings.ValidateAccount = accounts.ValidateSelection
	cloudClient := p115.NewCloudClient()
	cloudDownloads := &service.CloudDownloadService{Accounts: accounts, Client: cloudClient}
	httpapi.RegisterCloudDownload(admin, accounts, cloudDownloadSettings, cloudDownloads, cloudClient, p115.NewShareClient())
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
	shareAPI := httpapi.NewShareAPI(secret, func() string { return "" })
	if root := os.Getenv("STRM_ROOT"); root != "" {
		shareAPI.OutputRoot = root
	}
	automation, err := service.NewAutomationStore(filepath.Join(dataDir, "automation.json"), shareAPI.OutputRoot, "")
	if err != nil {
		log.Fatal(err)
	}
	shareAPI.BaseURL = func() string { return automation.Value().Generation.BaseURL }
	accountMigrationCtx, accountMigrationCancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = service.MigrateP115ShareSelection(accountMigrationCtx, database, automation)
	accountMigrationCancel()
	if err != nil {
		log.Fatal(err)
	}
	automation.ValidateAccount = accounts.ValidateSelection
	shareCookie := func() string { return accounts.CookieFor(automation.Value().AccountID) }
	shareAPI.Cookie = shareCookie
	generator := &service.STRMGenerator{Client: shareAPI.Client, Cookie: shareCookie, Root: shareAPI.OutputRoot}
	workflow := service.NewShareWorkflow(shareAPI.Client, generator, shareCookie, automation, shareAPI.OutputRoot, database)
	httpapi.RegisterReshareJobs(admin, database)
	loggedGenerator := &service.LoggedGenerator{Generator: workflow, Logs: generationLogs, Root: shareAPI.OutputRoot}
	shareTasks := &service.ShareTasks{Generator: loggedGenerator, Store: database, Settings: automation, Cookie: shareCookie, AccountCookie: accounts.CookieFor}
	folderMonitors := service.NewFolderMonitorService(database, accounts, shareAPI.Client, generator, shareAPI.OutputRoot, generationLogs)
	folderMonitors.SetSharedSettings(automation.Value)
	httpapi.RegisterFolderMonitors(admin, folderMonitors)
	if err := database.InterruptShareTasks(context.Background()); err != nil {
		log.Fatal(err)
	}
	shareAPI.Workflow = shareTasks
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	shareAPI.Runs = service.NewShareRuns(ctx, shareTasks)
	shareTasks.RetryWake = make(chan struct{}, 1)
	httpapi.RegisterShareTasks(admin, database, shareTasks)
	httpapi.RegisterLogs(admin, generationLogs)
	telegram, err := bot.NewHub(dataDir, secret, shareAPI.Runs, automation)
	if err != nil {
		log.Fatal(err)
	}
	telegram.CloudDownloads = cloudDownloads
	automation.ValidateBot = telegram.ValidateSelection
	follows.ValidateBot = telegram.ValidateSelection
	telegramMigrationCtx, telegramMigrationCancel := context.WithTimeout(context.Background(), 10*time.Second)
	err = bot.MigrateLegacyBindings(telegramMigrationCtx, telegram.Profiles(), automation, database)
	telegramMigrationCancel()
	if err != nil {
		log.Fatal(err)
	}
	httpapi.RegisterAutomation(admin, automation, func() bool {
		value := automation.Value()
		return value.TelegramEnabled && telegram.ValidateSelection(value.BotID, true) == nil
	}, telegram.Refresh)
	telegram.Logs = generationLogs
	telegram.Notifications = database
	follows.Notifications = telegram
	shareTasks.Notifier, shareTasks.Logs = telegram, generationLogs
	httpapi.RegisterTelegramHub(admin, telegram, generationLogs)
	userConfig, err := service.NewSessionStore(filepath.Join(dataDir, "telegram-user-config.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	userSession, err := service.NewSessionStore(filepath.Join(dataDir, "telegram-user-session.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	telegramUser, err := tguser.New(userConfig, userSession)
	if err != nil {
		log.Fatal(err)
	}
	follows.BotSearch = telegramUser
	follows.ShareRuns = shareAPI.Runs
	follows.GenerationSettings = automation
	follows.Logs = generationLogs
	httpapi.RegisterTelegramUser(admin, telegramUser, tmdbService, generationLogs)
	shareAPI.Register(r, admin)
	if directory := os.Getenv("FRONTEND_DIST"); directory != "" {
		if err := httpapi.RegisterFrontend(r, directory); err != nil {
			log.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", ":3002")
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); tmdbImages.Run(ctx) }()
	workers.Add(1)
	go func() { defer workers.Done(); mikanImages.Run(ctx) }()
	workers.Add(1)
	go func() { defer workers.Done(); gyingImages.Run(ctx) }()
	workers.Add(1)
	go func() { defer workers.Done(); shareAPI.Runs.Run() }()
	if os.Getenv("TMDB_API_KEY") != "" {
		workers.Add(1)
		go func() { defer workers.Done(); _ = tmdbService.Run(ctx, embyService.CachedSeriesIDs) }()
	}
	workerErrors := make(chan error, 8)
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := folderMonitors.Run(ctx); err != nil {
			workerErrors <- err
		}
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := follows.RunChecks(ctx); err != nil {
			workerErrors <- err
		}
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := follows.RunBotJobs(ctx); err != nil {
			workerErrors <- err
		}
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := embyService.Run(ctx); err != nil {
			workerErrors <- err
		}
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		if err := shareTasks.Run(ctx); err != nil {
			workerErrors <- err
		}
	}()
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
