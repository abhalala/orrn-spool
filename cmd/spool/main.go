package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/gin-gonic/gin"

    "github.com/orrn/spool/internal/ai"
    "github.com/orrn/spool/internal/api/handlers"
    apimw "github.com/orrn/spool/internal/api/middleware"
    "github.com/orrn/spool/internal/archive"
    "github.com/orrn/spool/internal/config"
    "github.com/orrn/spool/internal/core"
    "github.com/orrn/spool/internal/db"
    "github.com/orrn/spool/internal/webhook"
)

func main() {
    configPath := flag.String("config", "config.yaml", "Path to spool config")
    flag.Parse()

    cfg, err := config.Load(*configPath)
    if err != nil {
        log.Fatalf("load config: %v", err)
    }
    if err := cfg.Validate(); err != nil {
        log.Fatalf("validate config: %v", err)
    }

    if err := db.Init(db.Config{Path: cfg.Database.Path}); err != nil {
        log.Fatalf("init db: %v", err)
    }
    database := db.GetDB()
    defer db.Close()

    authMiddleware, err := apimw.NewAuthMiddleware(database)
    if err != nil {
        log.Fatalf("init auth middleware: %v", err)
    }

    encryptionKey, err := apimw.GetEncryptionKey(database)
    if err != nil {
        log.Fatalf("load encryption key: %v", err)
    }

    webhookSender := webhook.NewWebhookSender(database, webhook.WebhookConfig{})
    webhookSender.Start()
    defer webhookSender.Stop()

    printerManager := core.NewPrinterManager(database, &cfg.Printers, webhookSender)
    printerManager.Start()
    defer printerManager.Stop()

    tsplGenerator := core.NewTSPL2Generator()
    queue := core.NewQueue(database, printerManager, tsplGenerator, webhookSender, &cfg.Queue)
    if err := queue.Start(); err != nil {
        log.Fatalf("start queue: %v", err)
    }
    defer queue.Stop()

    archiver, err := archive.NewArchiver(database, archive.ArchiveConfig{
        ArchivePath: cfg.Database.ArchivePath,
        ArchiveDays: cfg.Database.ArchiveDays,
    })
    if err != nil {
        log.Fatalf("init archiver: %v", err)
    }
    if passphrase := os.Getenv("SPOOL_ARCHIVE_PASSPHRASE"); passphrase != "" {
        if err := archiver.SetPassphrase(passphrase); err != nil {
            log.Fatalf("set archive passphrase: %v", err)
        }
    }
    archiver.Start()
    defer archiver.Stop()

    geminiClient := ai.NewGeminiClient()
    if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
        geminiClient.SetAPIKey(apiKey)
    }

    printerHandler := handlers.NewPrinterHandler(database, printerManager)
    jobHandler := handlers.NewJobHandler(database, queue, tsplGenerator)
    templateHandler := handlers.NewTemplateHandler(database, tsplGenerator, queue)
    webhookHandler := handlers.NewWebhookHandler(database, webhookSender)
    aiHandler := handlers.NewAIHandler(geminiClient, database, encryptionKey)
    archiveHandler := handlers.NewArchiveHandler(archiver, database)
    settingsHandler := handlers.NewSettingsHandler(database, cfg)
    webUIHandler := handlers.NewWebUIHandler(database, queue, printerManager)

    router := gin.Default()
    if err := router.SetTrustedProxies(nil); err != nil {
        log.Fatalf("set trusted proxies: %v", err)
    }
    router.LoadHTMLGlob("web/templates/**/*")
    router.Static("/static", "web/static")

    router.GET("/health", func(c *gin.Context) {
        c.JSON(http.StatusOK, gin.H{"status": "ok"})
    })
    router.GET("/login", func(c *gin.Context) {
        c.HTML(http.StatusOK, "login.html", gin.H{})
    })

    authRoutes := router.Group("/api/auth")
    {
        authRoutes.POST("/login", authMiddleware.LoginHandler)
        authRoutes.POST("/logout", authMiddleware.LogoutHandler)
        authRoutes.GET("/status", authMiddleware.StatusHandler)
        authRoutes.POST("/setup", authMiddleware.SetupHandler)
    }

    protectedPages := router.Group("/")
    protectedPages.Use(authMiddleware.RequireAuth())
    {
        protectedPages.GET("/", webUIHandler.Dashboard)
        protectedPages.GET("/dashboard", webUIHandler.Dashboard)
        protectedPages.GET("/api/dashboard/stats", webUIHandler.GetDashboardStats)
        protectedPages.GET("/api/printers/:id/status", webUIHandler.GetPrinterStatusCard)
    }

    api := router.Group("/api")
    api.Use(authMiddleware.RequireAuth())
    {
        handlers.RegisterTemplateRoutes(api, templateHandler)
        handlers.RegisterAIRoutes(api, aiHandler)
        handlers.RegisterWebhookRoutes(api, webhookHandler)
        handlers.RegisterSettingsRoutes(api, settingsHandler)
        archiveHandler.RegisterRoutes(api)
        jobHandler.RegisterRoutes(api)
        api.GET("/printers", printerHandler.ListPrinters)
        api.POST("/printers", printerHandler.CreatePrinter)
        api.GET("/printers/:id", printerHandler.GetPrinter)
        api.PUT("/printers/:id", printerHandler.UpdatePrinter)
        api.DELETE("/printers/:id", printerHandler.DeletePrinter)
        api.GET("/printers/:id/status", printerHandler.GetPrinterStatus)
        api.POST("/printers/:id/test", printerHandler.TestPrinter)
        api.POST("/printers/:id/pause", printerHandler.PausePrinter)
        api.POST("/printers/:id/resume", printerHandler.ResumePrinter)
        api.GET("/printers/:id/counters", printerHandler.GetPrinterCounters)
    }

    jobHandler.RegisterLegacyRoutes(router)

    server := &http.Server{
        Addr:              fmt.Sprintf(":%d", cfg.Server.Port),
        Handler:           router,
        ReadHeaderTimeout: 10 * time.Second,
        ReadTimeout:       cfg.Server.ReadTimeout,
        WriteTimeout:      cfg.Server.WriteTimeout,
        IdleTimeout:       60 * time.Second,
    }

    go func() {
        log.Printf("orrn-spool listening on :%d", cfg.Server.Port)
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("listen: %v", err)
        }
    }()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh

    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    if err := server.Shutdown(ctx); err != nil {
        log.Printf("shutdown error: %v", err)
    }
}
