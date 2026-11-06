package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/api/handlers"
	apimw "github.com/orrn/spool/internal/api/middleware"
)

// routeDeps holds what registerRoutes mounts. main builds it from the running
// services; tests build it over a temporary database so they exercise the
// same route table and middleware order the spool serves.
type routeDeps struct {
	auth      *apimw.AuthMiddleware
	webUI     *handlers.WebUIHandler
	printers  *handlers.PrinterHandler
	jobs      *handlers.JobHandler
	templates *handlers.TemplateHandler
	webhooks  *handlers.WebhookHandler
	ai        *handlers.AIHandler
	archive   *handlers.ArchiveHandler
	settings  *handlers.SettingsHandler
	scale     *handlers.ScaleHandler
	version   string
}

// registerRoutes mounts every spool route on router. Template loading and
// /static stay in main because they depend on the working directory.
func registerRoutes(router *gin.Engine, d routeDeps) {
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": d.version})
	})
	router.GET("/login", d.auth.LoginPage)
	router.POST("/login", d.auth.LoginForm)
	router.POST("/logout", d.auth.LogoutRedirect)

	authRoutes := router.Group("/api/auth")
	{
		authRoutes.POST("/login", d.auth.LoginHandler)
		authRoutes.POST("/logout", d.auth.LogoutHandler)
		authRoutes.GET("/status", d.auth.StatusHandler)
		authRoutes.POST("/setup", d.auth.SetupHandler)
	}

	protectedPages := router.Group("/")
	protectedPages.Use(d.auth.RequirePageAuth())
	d.webUI.RegisterPages(protectedPages)

	api := router.Group("/api")
	api.Use(d.auth.RequireAuth())
	api.Use(d.webUI.HTMXFragments())
	{
		handlers.RegisterTemplateRoutes(api, d.templates)
		handlers.RegisterAIRoutes(api, d.ai)
		handlers.RegisterWebhookRoutes(api, d.webhooks)
		handlers.RegisterSettingsRoutes(api, d.settings)
		d.archive.RegisterRoutes(api)
		d.jobs.RegisterRoutes(api)
		api.GET("/printers", d.printers.ListPrinters)
		api.POST("/printers", d.printers.CreatePrinter)
		api.GET("/printers/:id", d.printers.GetPrinter)
		api.PUT("/printers/:id", d.printers.UpdatePrinter)
		api.DELETE("/printers/:id", d.printers.DeletePrinter)
		api.GET("/printers/:id/status", d.printers.GetPrinterStatus)
		api.POST("/printers/:id/test", d.printers.TestPrinter)
		api.POST("/printers/:id/pause", d.printers.PausePrinter)
		api.POST("/printers/:id/resume", d.printers.ResumePrinter)
		api.GET("/printers/:id/counters", d.printers.GetPrinterCounters)
	}

	scaleRoutes := router.Group("/")
	scaleRoutes.Use(d.auth.RequireAuth())
	d.scale.RegisterRoutes(scaleRoutes)

	legacyPrint := router.Group("/")
	legacyPrint.Use(d.auth.RequireAuth())
	d.jobs.RegisterLegacyRoutes(router, legacyPrint)
}
