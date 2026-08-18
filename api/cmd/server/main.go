package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"fly-print-cloud/api/internal/auth"
	"fly-print-cloud/api/internal/business"
	"fly-print-cloud/api/internal/config"
	"fly-print-cloud/api/internal/database"
	"fly-print-cloud/api/internal/handlers"
	"fly-print-cloud/api/internal/logger"
	"fly-print-cloud/api/internal/middleware"
	"fly-print-cloud/api/internal/models"
	"fly-print-cloud/api/internal/operations"
	"fly-print-cloud/api/internal/security"
	"fly-print-cloud/api/internal/storage"
	"fly-print-cloud/api/internal/websocket"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// @title           Fly Print Cloud API
// @version         1.0
// @description     云打印系统后端API服务，提供打印任务管理、边缘节点管理、OAuth2认证等功能
// @termsOfService  http://swagger.io/terms/

// @contact.name   API Support
// @contact.email  support@flyprint.com

// @license.name  Apache 2.0
// @license.url   http://www.apache.org/licenses/LICENSE-2.0.html

// @host      localhost:8080
// @BasePath  /api

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

func main() {
	// 加载配置
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}

	// 验证配置
	if err := cfg.Validate(); err != nil {
		logger.Fatal("Invalid configuration", zap.Error(err))
	}
	middleware.ConfigureOAuth2(cfg.OAuth2)

	// 初始化日志系统
	if err := logger.Init(cfg.App.Debug); err != nil {
		logger.Fatal("Failed to initialize logger", zap.Error(err))
	}
	defer logger.Sync()

	logger.Info("Starting application",
		zap.String("name", cfg.App.Name),
		zap.String("version", cfg.App.Version),
		zap.String("environment", cfg.App.Environment),
		zap.Bool("debug", cfg.App.Debug),
	)

	// 设置Gin模式
	if !cfg.App.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	// 连接数据库
	db, err := database.New(&cfg.Database)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	logger.Info("Database connected successfully")

	// 初始化数据库表
	if err := db.InitTables(); err != nil {
		logger.Fatal("Failed to initialize database tables", zap.Error(err))
	}

	// 创建默认管理员账户（如果配置了）
	if err := db.CreateDefaultAdmin(); err != nil {
		logger.Warn("Failed to create default admin", zap.Error(err))
	}

	// 初始化服务
	userRepo := database.NewUserRepository(db)
	sitePortalRepo := database.NewSitePortalRepository(db)
	externalIdentityRepo := database.NewExternalIdentityRepository(db)
	portalReadyOutboxRepo := database.NewPortalSessionReadyOutboxRepository(db)
	if cfg.SitePortalBootstrap.Code != "" {
		if err := sitePortalRepo.UpsertBootstrap(&models.SitePortal{
			Code:         cfg.SitePortalBootstrap.Code,
			DisplayName:  cfg.SitePortalBootstrap.DisplayName,
			EntryURL:     cfg.SitePortalBootstrap.EntryURL,
			ClaimBaseURL: cfg.SitePortalBootstrap.ClaimBaseURL,
		}); err != nil {
			logger.Fatal("Failed to initialize Site Portal", zap.Error(err))
		}
	}
	edgeNodeRepo := database.NewEdgeNodeRepository(db)
	printerRepo := database.NewPrinterRepository(db)
	printJobRepo := database.NewPrintJobRepository(db)
	printAuthorizationRepo := database.NewPrintAuthorizationRepository(db)
	printQuotaRepo := database.NewPrintQuotaRepository(db)
	fileRepo := database.NewFileRepository(db)
	tokenUsageRepo := database.NewTokenUsageRepository(db)
	alertRepo := database.NewOperationalAlertRepository(db)
	if _, err := alertRepo.CleanupOrphaned(); err != nil {
		logger.Warn("Failed to clean up orphaned operational alerts", zap.Error(err))
	}
	statusService := operations.NewStatusService(db, alertRepo)
	systemSettingsRepo := database.NewSystemSettingsRepository(db)
	businessSettingsService := business.NewSettingsService(systemSettingsRepo, cfg)

	// 初始化凭证管理器（支持一次性凭证验证）
	tokenManager := security.NewTokenManager(
		cfg.Security.FileAccessSecret,
		cfg.Security.UploadTokenTTL,
		cfg.Security.DownloadTokenTTL,
		tokenUsageRepo,
	)
	tokenManager.SetTTLProvider(businessSettingsService)

	storageService, err := storage.NewFromConfig(cfg.Storage)
	if err != nil {
		logger.Fatal("Failed to initialize storage backend", zap.Error(err))
	}
	opsContactRepo := database.NewOpsContactRepository(db)

	// 启动Token使用记录清理任务（每小时清理过期记录）
	go startTokenCleanupTask(tokenUsageRepo)
	// 启动文件清理任务（定期删除1天前的文件）
	go startFileCleanupTask(fileRepo, cfg.Storage)
	// 启动打印任务状态清理任务（每30分钟检查一次超时任务）
	go startStaleJobCleanupTask(statusService)

	// 初始化 WebSocket 管理器
	wsManager := websocket.NewConnectionManager(tokenManager, statusService)
	go startPortalReadyOutboxTask(context.Background(), portalReadyOutboxRepo, wsManager)
	jobUpdateReceiptRepo := database.NewEdgeJobUpdateReceiptRepository(db)
	terminalTicketRepo := database.NewTerminalTicketRepository(db)
	entrySessionRepo := database.NewEntrySessionRepository(db)
	terminalUploadSessions := database.NewTerminalUploadSessionRepository(db)
	terminalSessionRepo := database.NewTerminalSessionRepository(db)
	wsHandler := websocket.NewWebSocketHandler(wsManager, printerRepo, edgeNodeRepo, printJobRepo, fileRepo, tokenManager, cfg.Server.AllowedOrigins, statusService, jobUpdateReceiptRepo, terminalSessionRepo, terminalTicketRepo, entrySessionRepo, terminalUploadSessions)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for now := range ticker.C {
			if err := statusService.Sweep(now); err != nil {
				logger.Error("Operational status sweep failed", zap.Error(err))
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := alertRepo.CleanupResolved(90 * 24 * time.Hour); err != nil {
				logger.Error("Alert history cleanup failed", zap.Error(err))
			}
			if _, err := alertRepo.CleanupOrphaned(); err != nil {
				logger.Error("Orphaned alert cleanup failed", zap.Error(err))
			}
		}
	}()

	// 初始化内置认证服务（builtin 模式）
	oauth2SecretCipher, err := security.NewClientSecretCipher(cfg.Security.OAuthClientSecretEncryptionKey)
	if err != nil {
		logger.Fatal("Invalid service credential encryption key", zap.Error(err))
	}
	var builtinAuth *auth.BuiltinAuthService
	var oauth2ClientRepo *database.OAuth2ClientRepository
	if cfg.OAuth2.IsBuiltinMode() {
		oauth2ClientRepo = database.NewOAuth2ClientRepository(db)
		builtinAuth = auth.NewBuiltinAuthService(oauth2ClientRepo, userRepo, &cfg.OAuth2)
		logger.Info("OAuth2 mode: builtin (embedded auth service)")

		// Edge credentials are provisioned explicitly through the admin API and
		// are bound to one existing node. Shared edge-default credentials are no
		// longer created by any environment.
		logger.Info("OAuth2 requires one bound credential per Edge node")
		if cfg.SitePortalBootstrap.Code != "" {
			if err := ensureBootstrapSitePortalClient(oauth2ClientRepo, oauth2SecretCipher, cfg.SitePortalBootstrap); err != nil {
				logger.Fatal("Failed to initialize Site Portal OAuth client", zap.Error(err))
			}
		}
	} else {
		logger.Info("OAuth2 mode: keycloak (external identity provider)")
	}
	var sitePortalAdminHandler *handlers.SitePortalAdminHandler
	if oauth2ClientRepo != nil {
		sitePortalAdminHandler = handlers.NewSitePortalAdminHandler(sitePortalRepo, oauth2ClientRepo, oauth2SecretCipher)
	}
	var edgeActivationHandler *handlers.EdgeActivationHandler
	if oauth2ClientRepo != nil {
		edgeActivationHandler = handlers.NewEdgeActivationHandler(db, oauth2ClientRepo, oauth2SecretCipher)
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				if _, err := edgeActivationHandler.CleanupExpired(); err != nil {
					logger.Error("Failed to clean expired activations", zap.Error(err))
				}
			}
		}()
	}

	// 初始化处理器
	userHandler := handlers.NewUserHandler(userRepo, printQuotaRepo)
	edgeNodeHandler := handlers.NewEdgeNodeHandlerWithServices(db, edgeNodeRepo, printerRepo, printJobRepo, wsManager, tokenUsageRepo, alertRepo, terminalTicketRepo, terminalUploadSessions, opsContactRepo, statusService)
	printerHandler := handlers.NewPrinterHandler(printerRepo, edgeNodeRepo, printJobRepo, wsManager, tokenUsageRepo, statusService, alertRepo)
	printJobHandler := handlers.NewPrintJobHandler(printJobRepo, printerRepo, edgeNodeRepo, wsManager, statusService, alertRepo)
	portalPrintHandler := handlers.NewPortalPrintHandler(printAuthorizationRepo)
	oauth2Handler := handlers.NewOAuth2Handler(&cfg.OAuth2, &cfg.Admin, userRepo, builtinAuth, cfg.Security.EntryCookieSecure)
	fileHandler := handlers.NewFileHandler(fileRepo, &cfg.Storage, storageService, wsManager, tokenManager, businessSettingsService, edgeNodeRepo, printerRepo)
	fileHandler.SetTerminalUploadSessionBinder(terminalUploadSessions)
	fileHandler.SetTerminalSessionMatcher(terminalSessionRepo)
	terminalTicketHandler := handlers.NewTerminalTicketHandler(entrySessionRepo, printerRepo, edgeNodeRepo, wsManager, sitePortalRepo, cfg.Security.EntryCookieSecure)
	sitePortalHandler := handlers.NewSitePortalHandler(sitePortalRepo, entrySessionRepo, externalIdentityRepo, wsManager, portalReadyOutboxRepo)
	businessSettingsHandler := handlers.NewBusinessSettingsHandler(businessSettingsService)
	opsContactHandler := handlers.NewOpsContactHandler(opsContactRepo, businessSettingsService)
	healthHandler := handlers.NewHealthHandler(db, wsManager)

	// 启动 WebSocket 管理器
	go wsManager.Run()

	// 创建Gin路由
	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxyCIDRs); err != nil {
		logger.Fatal("Invalid trusted proxy configuration", zap.Error(err))
	}

	// Rate Limiting (10 req/s)
	// 添加中间件
	r.Use(middleware.LoggerMiddleware())
	r.Use(gin.Recovery())
	r.Use(middleware.CORSMiddleware(cfg.Server.AllowedOrigins))
	r.Use(middleware.SecurityHeadersMiddleware())

	// 设置路由
	setupRoutes(r, userHandler, edgeNodeHandler, edgeActivationHandler, printerHandler, printJobHandler, portalPrintHandler, wsHandler, oauth2Handler, fileHandler, terminalTicketHandler, sitePortalHandler, sitePortalAdminHandler, businessSettingsHandler, opsContactHandler, healthHandler, printJobRepo, edgeNodeRepo, printerRepo, alertRepo)

	// 创建HTTP服务器
	serverAddr := cfg.Server.GetServerAddr()
	srv := &http.Server{
		Addr:    serverAddr,
		Handler: r,
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 启动服务器（在goroutine中）
	go func() {
		logger.Info("Server starting", zap.String("address", serverAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server failed to start", zap.Error(err))
		}
	}()
	if deadline, err := cfg.Security.InsecureHTTPDeadline(); err != nil {
		logger.Fatal("Invalid temporary HTTP deadline", zap.Error(err))
	} else if !deadline.IsZero() {
		logger.Error("TEMPORARY INSECURE HTTP MODE ENABLED: credentials and tickets traverse plaintext HTTP", zap.Time("expires_at", deadline))
		go func() {
			timer := time.NewTimer(time.Until(deadline))
			defer timer.Stop()
			<-timer.C
			logger.Error("Temporary insecure HTTP deadline reached; shutting down Cloud API")
			quit <- syscall.SIGTERM
		}()
	}

	// 等待中断信号以优雅关机
	<-quit

	logger.Info("Shutting down server...")

	// 设置5秒超时的context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 优雅关闭服务器
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
	}

	logger.Info("Server exited")
}

func ensureBootstrapSitePortalClient(repo *database.OAuth2ClientRepository, cipher *security.ClientSecretCipher, bootstrap config.SitePortalBootstrapConfig) error {
	existing, err := repo.GetByClientID(bootstrap.OAuthClientID)
	if err == nil {
		if existing.ClientType != "site_portal" || existing.SitePortalCode == nil || *existing.SitePortalCode != bootstrap.Code {
			return fmt.Errorf("bootstrap OAuth client_id %q is already bound to a different client", bootstrap.OAuthClientID)
		}
		return nil
	}
	if !strings.Contains(err.Error(), "not found") {
		return err
	}
	secretHash, err := auth.HashClientSecret(bootstrap.OAuthClientSecret)
	if err != nil {
		return fmt.Errorf("hash bootstrap Site Portal secret: %w", err)
	}
	encryptedSecret, err := cipher.Encrypt(bootstrap.OAuthClientSecret)
	if err != nil {
		return fmt.Errorf("encrypt bootstrap Site Portal secret: %w", err)
	}
	portalCode := bootstrap.Code
	client := &models.OAuth2Client{
		ClientID:              bootstrap.OAuthClientID,
		ClientSecretHash:      secretHash,
		ClientSecretEncrypted: encryptedSecret,
		ClientType:            "site_portal",
		SitePortalCode:        &portalCode,
		AllowedScopes:         "site-portal:access",
		Description:           bootstrap.DisplayName,
		Enabled:               true,
	}
	if err := repo.Create(client); err != nil {
		return fmt.Errorf("create bootstrap Site Portal OAuth client: %w", err)
	}
	return nil
}

func setupRoutes(r *gin.Engine, userHandler *handlers.UserHandler, edgeNodeHandler *handlers.EdgeNodeHandler, edgeActivationHandler *handlers.EdgeActivationHandler, printerHandler *handlers.PrinterHandler, printJobHandler *handlers.PrintJobHandler, portalPrintHandler *handlers.PortalPrintHandler, wsHandler *websocket.WebSocketHandler, oauth2Handler *handlers.OAuth2Handler, fileHandler *handlers.FileHandler, terminalTicketHandler *handlers.TerminalTicketHandler, sitePortalHandler *handlers.SitePortalHandler, sitePortalAdminHandler *handlers.SitePortalAdminHandler, businessSettingsHandler *handlers.BusinessSettingsHandler, opsContactHandler *handlers.OpsContactHandler, healthHandler *handlers.HealthHandler, printJobRepo *database.PrintJobRepository, edgeNodeRepo *database.EdgeNodeRepository, printerRepo *database.PrinterRepository, alertRepo *database.OperationalAlertRepository) {
	r.GET("/entry", terminalTicketHandler.EntryPage)
	r.GET("/entry/options", terminalTicketHandler.SelectPage)
	// 公开健康检查路由（快速响应）
	r.GET("/health", healthHandler.BasicHealth)
	r.HEAD("/health", healthHandler.BasicHealth)

	// OAuth2 认证路由
	authGroup := r.Group("/auth")
	{
		authGroup.GET("/mode", oauth2Handler.Mode)         // 返回当前认证模式（公开）
		authGroup.POST("/token", oauth2Handler.Token)      // Token 端点（builtin 模式）
		authGroup.GET("/userinfo", oauth2Handler.UserInfo) // UserInfo 端点
		authGroup.GET("/login", oauth2Handler.Login)
		authGroup.GET("/callback", oauth2Handler.Callback)
		authGroup.GET("/me", oauth2Handler.Me)
		authGroup.GET("/verify", oauth2Handler.Verify)  // Nginx auth_request 使用
		authGroup.GET("/logout", oauth2Handler.Logout)  // 支持 GET 请求登出
		authGroup.POST("/logout", oauth2Handler.Logout) // 保留 POST 支持
	}

	// 统一 API 路由组（/api/v1）- OAuth2 Resource Server
	apiV1Group := r.Group("/api/v1")
	{
		sitePortalGroup := apiV1Group.Group("/site-portal", middleware.OAuth2ResourceServer("site-portal:access"))
		{
			sitePortalGroup.POST("/context", sitePortalHandler.Context)
			sitePortalGroup.POST("/login-completions", sitePortalHandler.CompleteLogin)
			sitePortalGroup.POST("/claims/validate", sitePortalHandler.ValidateClaim)
		}

		apiV1Group.POST("/public/terminal-entry/acquire", terminalTicketHandler.Acquire)
		apiV1Group.POST("/public/terminal-entry/status", terminalTicketHandler.EntryStatus)
		apiV1Group.POST("/public/terminal-entry/select", terminalTicketHandler.SelectEntry)
		// Admin Console API - 需要 admin:* scope
		adminGroup := apiV1Group.Group("/admin")
		{
			// Dashboard 路由 - 需要 admin 或 operator 权限
			dashboardGroup := adminGroup.Group("/dashboard", middleware.OAuth2ResourceServerAny("fly-print-admin", "fly-print-operator"))
			{
				dashboardHandler := handlers.NewDashboardHandler(printJobRepo, alertRepo)
				dashboardGroup.GET("/trends", dashboardHandler.GetTrends)
				dashboardGroup.GET("/maintenance", dashboardHandler.GetMaintenance)
				adminGroup.GET("/alerts/history", middleware.OAuth2ResourceServerAny("fly-print-admin", "fly-print-operator"), dashboardHandler.GetAlertHistory)
			}

			// 当前用户业务信息 - 任何认证用户都可以访问自己的档案
			adminGroup.GET("/profile", middleware.OAuth2ResourceServer(), userHandler.GetCurrentUserProfile)

			// 用户管理只允许 admin scope；viewer/operator 不能读取或修改账号资料。
			userManagementGroup := adminGroup.Group("/users", middleware.OAuth2ResourceServer("fly-print-admin"))
			{
				userManagementGroup.GET("", userHandler.ListUsers)
				userManagementGroup.POST("", userHandler.CreateUser)
				userManagementGroup.GET("/:id", userHandler.GetUser)
				userManagementGroup.PUT("/:id", userHandler.UpdateUser)
				userManagementGroup.PATCH("/:id/enabled", userHandler.UpdateEnabled)
				userManagementGroup.DELETE("/:id", userHandler.DeleteUser)
				userManagementGroup.PUT("/:id/password", userHandler.ChangePassword)
				userManagementGroup.POST("/:id/print-quota-grants", userHandler.GrantPrintQuota)
			}

			businessSettingsGroup := adminGroup.Group("/business-settings", middleware.OAuth2ResourceServer("fly-print-admin"))
			{
				businessSettingsGroup.GET("", businessSettingsHandler.Get)
				businessSettingsGroup.PUT("", businessSettingsHandler.Update)
			}
			if sitePortalAdminHandler != nil {
				sitePortalGroup := adminGroup.Group("/site-portals", middleware.OAuth2ResourceServer("fly-print-admin"))
				sitePortalGroup.GET("", sitePortalAdminHandler.List)
				sitePortalGroup.POST("", sitePortalAdminHandler.Create)
				sitePortalGroup.PUT("/:code", sitePortalAdminHandler.Update)
				sitePortalGroup.PATCH("/:code/enabled", sitePortalAdminHandler.SetEnabled)
				sitePortalGroup.DELETE("/:code", sitePortalAdminHandler.Delete)
				sitePortalGroup.POST("/:code/rotate-secret", sitePortalAdminHandler.RotateSecret)
				sitePortalGroup.GET("/:code/providers", sitePortalAdminHandler.ListProviders)
				sitePortalGroup.POST("/:code/providers", sitePortalAdminHandler.CreateProvider)
				sitePortalGroup.PUT("/:code/providers/:provider_id", sitePortalAdminHandler.UpdateProvider)
				sitePortalGroup.PATCH("/:code/providers/:provider_id/enabled", sitePortalAdminHandler.SetProviderEnabled)
				sitePortalGroup.DELETE("/:code/providers/:provider_id", sitePortalAdminHandler.DeleteProvider)
			}

			opsContactGroup := adminGroup.Group("/ops-contacts", middleware.OAuth2ResourceServerAny("fly-print-admin", "fly-print-operator"))
			{
				opsContactGroup.GET("", opsContactHandler.List)
				opsContactGroup.POST("", opsContactHandler.Create)
				opsContactGroup.GET("/:id", opsContactHandler.Get)
				opsContactGroup.PUT("/:id", opsContactHandler.Update)
				opsContactGroup.DELETE("/:id", opsContactHandler.Delete)
				opsContactGroup.PATCH("/:id/enabled", opsContactHandler.UpdateEnabled)
				opsContactGroup.PUT("/:id/nodes", opsContactHandler.ReplaceNodes)
			}

			// Edge Node 管理路由 - 需要 admin 或 operator 权限
			edgeNodeGroup := adminGroup.Group("/edge-nodes", middleware.OAuth2ResourceServerAny("fly-print-admin", "fly-print-operator"))
			{
				edgeNodeGroup.GET("", edgeNodeHandler.ListEdgeNodes)
				if edgeActivationHandler != nil {
					edgeNodeGroup.POST("/activations", edgeActivationHandler.CreatePending)
				}
				edgeNodeGroup.GET("/:id", edgeNodeHandler.GetEdgeNode)
				edgeNodeGroup.PATCH("/:id/alias", edgeNodeHandler.UpdateAlias)
				edgeNodeGroup.PATCH("/:id/enabled", edgeNodeHandler.UpdateEnabled)
				if sitePortalAdminHandler != nil {
					edgeNodeGroup.GET("/:id/site-portals", middleware.OAuth2ResourceServer("fly-print-admin"), sitePortalAdminHandler.GetEdgeSitePortals)
					edgeNodeGroup.PUT("/:id/site-portals", middleware.OAuth2ResourceServer("fly-print-admin"), sitePortalAdminHandler.UpdateEdgeSitePortals)
				}
				edgeNodeGroup.DELETE("/:id", edgeNodeHandler.DeleteEdgeNode)
			}

			// 打印机管理路由 - 需要 admin 或 operator 权限
			printerGroup := adminGroup.Group("/printers", middleware.OAuth2ResourceServerAny("fly-print-admin", "fly-print-operator"))
			{
				printerGroup.GET("", printerHandler.ListPrinters)
				printerGroup.GET("/:id", printerHandler.GetPrinter)
				printerGroup.PUT("/:id", printerHandler.UpdatePrinter)
				printerGroup.DELETE("/:id", printerHandler.DeletePrinter)
			}

			// 打印任务只读；任务状态由打印链路驱动，不能从管理端重试或改写。
			printJobGroup := adminGroup.Group("/print-jobs", middleware.OAuth2ResourceServerAny("fly-print-admin", "fly-print-operator"))
			{
				printJobGroup.GET("", printJobHandler.ListPrintJobs)
				printJobGroup.GET("/:id", printJobHandler.GetPrintJob)
			}
		}

		// 直接 print:submit API 暂不对外注册；当前打印主链仅由已授权的
		// Edge/Portal 会话驱动，管理端继续使用 /api/v1/admin/* 只读查看。

		// Edge Node API - 需要 edge:* scope
		edgeGroup := apiV1Group.Group("/edge")
		{
			if edgeActivationHandler != nil {
				edgeGroup.POST("/activate", edgeActivationHandler.Activate)
				edgeGroup.PUT("/self/profile", middleware.OAuth2ResourceServer("edge:register"), edgeActivationHandler.UpdateSelfProfile)
			}
			edgeGroup.GET("/self/contacts", middleware.OAuth2ResourceServer("edge:register"), opsContactHandler.ListSelfContacts)
			// HTTP 心跳 API 已删除，改为通过 WebSocket 进行心跳

			// Edge Node 的打印机管理 - 添加节点禁用检查
			edgeGroup.POST("/:node_id/printers", middleware.OAuth2ResourceServer("edge:printer"), middleware.EdgeNodeIdentityMatch(), middleware.EdgeNodeEnabledCheck(edgeNodeRepo), printerHandler.EdgeRegisterPrinter)
			// 删除打印机：启用的节点可以管理自己的所有打印机（包括禁用的）
			edgeGroup.DELETE("/:node_id/printers/:printer_id", middleware.OAuth2ResourceServer("edge:printer"), middleware.EdgeNodeIdentityMatch(), middleware.EdgeNodeEnabledCheck(edgeNodeRepo), printerHandler.EdgeDeletePrinter)

			// 功能 3.2.2: 批量状态上报 API - 从 WebSocket 迁移到 REST API
			// 功能 3.2.3: 放行禁用打印机的状态上报请求，仅检查节点启用状态
			// 放行禁用节点的批量状态上报，允许监控禁用节点的打印机状态
			edgeGroup.POST("/:node_id/printers/status", middleware.OAuth2ResourceServer("edge:printer"), middleware.EdgeNodeIdentityMatch(), printerHandler.EdgeBatchUpdatePrinterStatus)
			edgeGroup.POST("/:node_id/print-authorizations", middleware.OAuth2ResourceServer("edge:printer"), middleware.EdgeNodeIdentityMatch(), middleware.EdgeNodeEnabledCheck(edgeNodeRepo), portalPrintHandler.Authorize)

			// WebSocket 连接
			edgeGroup.GET("/ws", wsHandler.HandleConnection)
		}

		// 文件上传/下载 - 支持凭证认证或 OAuth2 认证
		fileGroup := apiV1Group.Group("/files")
		{
			// 轻量验证上传Token（不消耗一次性Token）
			fileGroup.GET("/upload-policy", fileHandler.GetUploadPolicy)
			fileGroup.GET("/verify-upload-token", fileHandler.VerifyUploadToken)
			fileGroup.POST("/preflight", middleware.OptionalOAuth2ResourceServer(), fileHandler.PreflightUpload)
			// 上传：支持上传凭证或 OAuth2 认证
			fileGroup.POST("", middleware.OptionalOAuth2ResourceServer(), fileHandler.Upload)
			// 下载：支持下载凭证或 OAuth2 认证
			fileGroup.GET("/:id", middleware.OptionalOAuth2ResourceServer(), fileHandler.Download)
		}
	}
}

// startFileCleanupTask 启动文件清理任务
// 每小时扫描一次，删除创建时间超过1天的文件记录和物理文件
func startFileCleanupTask(fileRepo *database.FileRepository, storageCfg config.StorageConfig) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	backendCache := make(map[string]storage.Service)

	for range ticker.C {
		cutoff := time.Now().Add(-24 * time.Hour)
		files, err := fileRepo.ListOldFiles(cutoff)
		if err != nil {
			logger.Error("File cleanup: failed to list old files", zap.Error(err))
			continue
		}
		if len(files) == 0 {
			continue
		}

		deletedCount := 0
		for _, f := range files {
			provider := f.StorageProvider
			if provider == "" {
				provider = "local"
			}

			storageSvc, ok := backendCache[provider]
			if !ok {
				var err error
				storageSvc, err = storage.NewForFile(storageCfg, f)
				if err != nil {
					logger.Warn("File cleanup: failed to initialize storage backend", zap.String("provider", provider), zap.String("id", f.ID), zap.Error(err))
					continue
				}
				backendCache[provider] = storageSvc
			}

			storageKey := f.ObjectKey
			if storageKey == "" {
				storageKey = f.FilePath
			}

			if err := storageSvc.Delete(context.Background(), storageKey); err != nil {
				logger.Warn("File cleanup: failed to remove file", zap.String("key", storageKey), zap.String("provider", provider), zap.Error(err))
				continue
			}

			if err := fileRepo.DeleteByID(f.ID); err != nil {
				logger.Warn("File cleanup: failed to delete db record", zap.String("id", f.ID), zap.Error(err))
				continue
			}
			deletedCount++
		}

		if deletedCount > 0 {
			logger.Info("File cleanup completed", zap.Int("deleted_count", deletedCount))
		}
	}
}

// startStaleJobCleanupTask 启动打印任务状态清理任务
// 每30分钟扫描一次，将超过30分钟未更新的“打印中”任务标记为失败
func startStaleJobCleanupTask(statusService *operations.StatusService) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		// 30分钟超时
		timeout := 30 * time.Minute
		affected, err := statusService.CleanupStaleJobs(time.Now(), timeout)
		if err != nil {
			logger.Error("Stale job cleanup error", zap.Error(err))
			continue
		}

		if affected > 0 {
			logger.Info("Stale job cleanup completed", zap.Int64("affected", affected))
		}
	}
}

func startPortalReadyOutboxTask(ctx context.Context, repo *database.PortalSessionReadyOutboxRepository, dispatcher *websocket.ConnectionManager) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for {
				event, err := repo.ClaimDue(now)
				if err != nil {
					logger.Error("Portal session-ready outbox claim failed", zap.Error(err))
					break
				}
				if event == nil {
					break
				}
				var payload websocket.PortalSessionReadyPayload
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					logger.Error("Portal session-ready outbox payload invalid", zap.String("event_id", event.ID), zap.Error(err))
					_ = repo.Retry(event.ID, event.AttemptCount, err.Error(), now)
					continue
				}
				if !dispatcher.IsNodeConnected(event.NodeID) {
					_ = repo.Retry(event.ID, event.AttemptCount, "edge_not_connected", now)
					continue
				}
				if err := dispatcher.DispatchPortalSessionReady(event.NodeID, payload); err != nil {
					_ = repo.Retry(event.ID, event.AttemptCount, err.Error(), now)
					continue
				}
				if err := repo.MarkDelivered(event.ID); err != nil {
					logger.Error("Portal session-ready outbox completion failed", zap.String("event_id", event.ID), zap.Error(err))
				}
			}
		}
	}
}

// startTokenCleanupTask 启动Token使用记录清理任务
// 每小时清理一次过期的token记录，防止数据库表膨胀
func startTokenCleanupTask(tokenUsageRepo *database.TokenUsageRepository) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		deleted, err := tokenUsageRepo.CleanupExpiredTokens(time.Now())
		if err != nil {
			logger.Error("Token cleanup error", zap.Error(err))
		} else if deleted > 0 {
			logger.Info("Token cleanup completed", zap.Int64("deleted", deleted))
		}
	}
}
