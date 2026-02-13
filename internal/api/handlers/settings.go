package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"orrn-spool/internal/config"
	"orrn-spool/internal/db"
)

const (
	settingsKeyPassword     = "admin_password"
	settingsKeyArchiveDays  = "archive_days"
	settingsKeyArchiveEnabled = "archive_enabled"
	settingsKeyAIEnabled    = "ai_enabled"
	settingsKeyAIModel      = "ai_model"
)

type SettingsHandler struct {
	db     *sql.DB
	config *config.Config
}

type SettingsResponse struct {
	ArchiveDays    int    `json:"archive_days"`
	ArchiveEnabled bool   `json:"archive_enabled"`
	AIEnabled      bool   `json:"ai_enabled"`
	AIModel        string `json:"ai_model"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=6"`
}

type ServerConfigResponse struct {
	Port                int    `json:"port"`
	DatabasePath        string `json:"database_path"`
	ArchivePath         string `json:"archive_path"`
	HealthCheckInterval string `json:"health_check_interval"`
	ConnectionTimeout   string `json:"connection_timeout"`
	StatusPollInterval  string `json:"status_poll_interval"`
	MaxRetries          int    `json:"max_retries"`
	RetryDelay          string `json:"retry_delay"`
	WorkerCount         int    `json:"worker_count"`
	LogLevel            string `json:"log_level"`
	LogFormat           string `json:"log_format"`
}

type UpdateArchiveSettingsRequest struct {
	ArchiveDays    int  `json:"archive_days" binding:"min=0"`
	ArchiveEnabled bool `json:"archive_enabled"`
}

func NewSettingsHandler(database *sql.DB, cfg *config.Config) *SettingsHandler {
	return &SettingsHandler{
		db:     database,
		config: cfg,
	}
}

func (h *SettingsHandler) GetSettings(c *gin.Context) {
	ctx := c.Request.Context()
	resp := SettingsResponse{
		ArchiveDays:    h.config.Database.ArchiveDays,
		ArchiveEnabled: true,
		AIEnabled:      false,
		AIModel:        "",
	}

	if setting, err := db.Settings.GetSetting(ctx, settingsKeyArchiveDays); err == nil {
		if days, err := strconv.Atoi(setting.Value); err == nil && days > 0 {
			resp.ArchiveDays = days
		}
	}

	if setting, err := db.Settings.GetSetting(ctx, settingsKeyArchiveEnabled); err == nil {
		resp.ArchiveEnabled = setting.Value == "true"
	}

	if setting, err := db.Settings.GetSetting(ctx, settingsKeyAIEnabled); err == nil {
		resp.AIEnabled = setting.Value == "true"
	}

	if setting, err := db.Settings.GetSetting(ctx, settingsKeyAIModel); err == nil {
		resp.AIModel = setting.Value
	}

	c.JSON(http.StatusOK, resp)
}

func (h *SettingsHandler) ChangePassword(c *gin.Context) {
	var req ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: err.Error(),
		})
		return
	}

	ctx := c.Request.Context()
	setting, err := db.Settings.GetSetting(ctx, settingsKeyPassword)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "setup_required",
				Message: "No password has been set",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve current password",
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(setting.Value), []byte(req.CurrentPassword)); err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			Error:   "invalid_password",
			Message: "Current password is incorrect",
		})
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "hash_error",
			Message: "Failed to hash new password",
		})
		return
	}

	if err := db.Settings.SetSetting(ctx, settingsKeyPassword, string(hashedPassword), false); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to update password",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Password changed successfully",
	})
}

func (h *SettingsHandler) GetServerConfig(c *gin.Context) {
	resp := ServerConfigResponse{
		Port:                h.config.Server.Port,
		DatabasePath:        h.config.Database.Path,
		ArchivePath:         h.config.Database.ArchivePath,
		HealthCheckInterval: h.config.Printers.HealthCheckInterval.String(),
		ConnectionTimeout:   h.config.Printers.ConnectionTimeout.String(),
		StatusPollInterval:  h.config.Printers.StatusPollInterval.String(),
		MaxRetries:          h.config.Queue.MaxRetries,
		RetryDelay:          h.config.Queue.RetryDelay.String(),
		WorkerCount:         h.config.Queue.WorkerCount,
		LogLevel:            h.config.Logging.Level,
		LogFormat:           h.config.Logging.Format,
	}

	c.JSON(http.StatusOK, resp)
}

func (h *SettingsHandler) UpdateArchiveSettings(c *gin.Context) {
	var req UpdateArchiveSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: err.Error(),
		})
		return
	}

	ctx := c.Request.Context()

	archiveDays := req.ArchiveDays
	if archiveDays <= 0 {
		archiveDays = h.config.Database.ArchiveDays
	}

	if err := db.Settings.SetSetting(ctx, settingsKeyArchiveDays, strconv.Itoa(archiveDays), false); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to update archive days",
		})
		return
	}

	archiveEnabledStr := "false"
	if req.ArchiveEnabled {
		archiveEnabledStr = "true"
	}
	if err := db.Settings.SetSetting(ctx, settingsKeyArchiveEnabled, archiveEnabledStr, false); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to update archive enabled setting",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"message":         "Archive settings updated",
		"archive_days":    archiveDays,
		"archive_enabled": req.ArchiveEnabled,
	})
}

func RegisterSettingsRoutes(r *gin.RouterGroup, h *SettingsHandler) {
	r.GET("/settings", h.GetSettings)
	r.PUT("/settings/password", h.ChangePassword)
	r.GET("/settings/server", h.GetServerConfig)
	r.PUT("/settings/archive", h.UpdateArchiveSettings)
}
