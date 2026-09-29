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
	httpPort := os.Getenv("HTTP_PORT")
	if httpPort == "" {
		httpPort = "3002"
	}
	port, portErr := strconv.Atoi(httpPort)
	if portErr != nil || port < 1024 || port > 65535 || strconv.Itoa(port) != httpPort {
		log.Fatal("HTTP_PORT 必须是 1024～65535 之间的整数")
	}
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
	auth := service.NewPortalAuth(database, user, pass, secret)
	httpapi.RegisterPortalAuth(r, auth)
	admin := r.Group("/api/admin")
	admin.Use(httpapi.PortalAuthenticated(auth), httpapi.PortalAdminOnly())
	admin.GET("/me", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"username": c.GetString("admin_username")}) })
	httpapi.RegisterPortalInvites(admin, auth)
	catalog := r.Group("/api/catalog")
	catalog.Use(httpapi.PortalAuthenticated(auth))
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
	nanShareStore, err := service.NewSessionStore(filepath.Join(dataDir, "nanshare-config.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	nanShareService, err := service.NewNanShareService(nanShareStore)
	if err != nil {
		log.Fatal(err)
	}
	httpapi.RegisterNanShare(admin, nanShareService)
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
	member := r.Group("/api/me", httpapi.PortalAuthenticated(auth))
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
	cloudDownloadSettings.ValidateAccount = accounts.ValidatePrimary
	cloudDownloadSettings.PrimaryID = accounts.PrimaryID
	cloudClient := p115.NewCloudClient()
	cloudDownloads := &service.CloudDownloadService{Accounts: accounts, Client: cloudClient}
	httpapi.RegisterCloudDownload(admin, accounts, cloudDownloadSettings, cloudDownloads, cloudClient, p115.NewShareClient())
	organizeStore, err := service.NewSessionStore(filepath.Join(dataDir, "manual-organize.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	manualOrganize, err := service.NewManualOrganize(organizeStore, accounts, p115.NewShareClient(), tmdbService)
	if err != nil {
		log.Fatal(err)
	}
	httpapi.RegisterManualOrganize(admin, manualOrganize)
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
	shareAPI := httpapi.NewShareAPI(func() string { return "" })
	if root := os.Getenv("STRM_ROOT"); root != "" {
		shareAPI.OutputRoot = root
	}
	automation, err := service.NewAutomationStore(filepath.Join(dataDir, "automation.json"), shareAPI.OutputRoot, "")
	if err != nil {
		log.Fatal(err)
	}
	shareAPI.BaseURL = func() string { return automation.Value().Generation.BaseURL }
	automation.PrimaryID = accounts.PrimaryID
	automation.ValidateAccount = accounts.ValidatePrimary
	shareCookie := func() string { return accounts.CookieFor(automation.Value().AccountID) }
	shareAPI.Cookie = shareCookie
	generator := &service.STRMGenerator{Client: shareAPI.Client, Cookie: shareCookie, Root: shareAPI.OutputRoot}
	workflow := service.NewShareWorkflow(shareAPI.Client, generator, shareCookie, automation, shareAPI.OutputRoot, database)
	httpapi.RegisterReshareJobs(admin, database)
	loggedGenerator := &service.LoggedGenerator{Generator: workflow, Logs: generationLogs, Root: shareAPI.OutputRoot}
	shareTasks := &service.ShareTasks{Generator: loggedGenerator, Store: database, Settings: automation, Cookie: shareCookie, AccountCookie: accounts.CookieFor}
	folderMonitors := service.NewFolderMonitorService(database, accounts, shareAPI.Client, generator, shareAPI.OutputRoot, generationLogs)
	folderMonitors.SetSharedSettings(automation.Value)
	folderMonitors.Tasks = shareTasks
	shareTasks.DirectoryPrepare = folderMonitors.VerifyGeneration
	if err := folderMonitors.DisableLegacySchedules(context.Background()); err != nil {
		log.Fatal(err)
	}
	httpapi.RegisterFolderMonitors(admin, folderMonitors)
	if err := database.InterruptShareTasks(context.Background()); err != nil {
		log.Fatal(err)
	}
	shareAPI.Workflow = shareTasks
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Reserve the management port before restoring the independently configured Emby listener.
	listener, err := net.Listen("tcp", ":"+httpPort)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	proxyStore, err := service.NewSessionStore(filepath.Join(dataDir, "emby-proxy.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	embyProxyLogs := service.NewEmbyProxyLogs(generationLogs)
	playbackStore, err := service.NewSessionStore(filepath.Join(dataDir, "playback-cache.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	playbackCache, err := service.NewSQLitePlaybackCache(ctx, database, playbackStore, accounts, shareAPI.Client, embyProxyLogs)
	if err != nil {
		log.Fatal(err)
	}
	defer playbackCache.Close()
	memberPlayback, err := service.NewMemberPlayback(ctx, database, accounts, embyService, shareAPI.Client, p115.NewAccountClient(), p115.NewClient())
	if err != nil {
		log.Fatal(err)
	}
	playbackCache.Members = memberPlayback
	memberPlayback.Cache = playbackCache
	auth.Members = memberPlayback
	identityStore, err := service.NewSessionStore(filepath.Join(dataDir, "emby-playback-identities.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	playbackIdentities, err := service.NewEmbyPlaybackIdentities(identityStore)
	if err != nil {
		log.Fatal(err)
	}
	embyProxy, err := service.NewEmbyProxy(ctx, proxyStore, accounts, func(config service.EmbyProxyConfig) http.Handler {
		return httpapi.NewEmbyCachedProxyHandler(config, accounts, shareAPI, playbackCache, embyProxyLogs, playbackIdentities)
	}, embyProxyLogs)
	if err != nil {
		log.Fatal(err)
	}
	defer embyProxy.Close()
	memberPlayback.ProxyStatus = embyProxy.Status
	memberPlayback.ClearDirectLinks = func() { embyProxy.ClearDirectLinks("") }
	httpapi.RegisterMemberPlayback(member, admin, memberPlayback)
	httpapi.RegisterEmbyProxy(admin, embyProxy)
	httpapi.RegisterPlaybackCache(admin, playbackCache, embyProxy, embyService)
	instantStore, err := service.NewSessionStore(filepath.Join(dataDir, "instant-upload.enc"), secret)
	if err != nil {
		log.Fatal(err)
	}
	instantUploads, err := service.NewInstantUploads(instantStore, accounts, shareAPI.Client, embyProxyLogs)
	if err != nil {
		log.Fatal(err)
	}
	instantUploads.Playback = playbackCache
	httpapi.RegisterInstantUploads(admin, instantUploads, shareAPI, httpapi.InstantPlaybackUserValidator(embyService, embyProxy, playbackCache))
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
	server := &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); memberPlayback.Run(ctx) }()
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
		if err := manualOrganize.Run(ctx); err != nil {
			workerErrors <- err
		}
	}()
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
	log.Printf("Cinema115 已启动，监听 :%s", httpPort)
	select {
	case <-ctx.Done():
	case err := <-workerErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("服务停止: %v", err)
		}
	}
	cancel()
	embyProxy.Close()
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
