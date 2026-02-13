package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"

	"orrn-spool/internal/archive"
)

type ArchiveHandler struct {
	archiver *archive.Archiver
	db       *sql.DB
}

func NewArchiveHandler(archiver *archive.Archiver, db *sql.DB) *ArchiveHandler {
	return &ArchiveHandler{
		archiver: archiver,
		db:       db,
	}
}

type ArchiveListResponse struct {
	Archives []*archive.ArchiveFile `json:"archives"`
	Count    int                    `json:"count"`
}

func (h *ArchiveHandler) ListArchives(c *gin.Context) {
	archives, err := h.archiver.ListArchives()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list archives"})
		return
	}

	c.JSON(http.StatusOK, ArchiveListResponse{
		Archives: archives,
		Count:    len(archives),
	})
}

type ArchiveInfoResponse struct {
	Filename    string    `json:"filename"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
	JobCount    int       `json:"job_count"`
	DateRange   string    `json:"date_range"`
	HasPassphrase bool    `json:"has_passphrase"`
}

func (h *ArchiveHandler) GetArchiveInfo(c *gin.Context) {
	filename := c.Param("filename")

	info, err := h.archiver.GetArchiveInfo(filename)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, ArchiveInfoResponse{
		Filename:      info.Filename,
		Size:          info.Size,
		CreatedAt:     info.CreatedAt,
		JobCount:      info.JobCount,
		DateRange:     info.DateRange,
		HasPassphrase: h.archiver.HasPassphrase(),
	})
}

func (h *ArchiveHandler) DownloadArchive(c *gin.Context) {
	filename := c.Param("filename")

	if !h.archiver.HasPassphrase() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "passphrase not configured"})
		return
	}

	tmpFile, err := os.CreateTemp("", "archive-download-*.db")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create temp file"})
		return
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	if err := h.archiver.DecryptArchive(filename, tmpPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to decrypt archive: %v", err)})
		return
	}

	info, err := os.Stat(tmpPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read decrypted archive"})
		return
	}

	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename+".db"))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", fmt.Sprintf("%d", info.Size()))

	c.File(tmpPath)
}

func (h *ArchiveHandler) DeleteArchive(c *gin.Context) {
	filename := c.Param("filename")

	if err := h.archiver.DeleteArchive(filename); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "archive deleted"})
}

type TriggerArchiveResponse struct {
	Message   string `json:"message"`
	Archived  int    `json:"archived,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (h *ArchiveHandler) TriggerArchive(c *gin.Context) {
	if !h.archiver.HasPassphrase() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "passphrase not configured"})
		return
	}

	if err := h.archiver.RunArchive(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"message": "archive completed with errors",
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "archive completed successfully"})
}

type PassphraseRequest struct {
	Passphrase string `json:"passphrase" binding:"required,min=8"`
}

func (h *ArchiveHandler) SetPassphrase(c *gin.Context) {
	var req PassphraseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.archiver.SetPassphrase(req.Passphrase); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to set passphrase"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "passphrase set successfully"})
}

type ArchiveSettingsResponse struct {
	ArchivePath   string `json:"archive_path"`
	ArchiveDays   int    `json:"archive_days"`
	HasPassphrase bool   `json:"has_passphrase"`
}

func (h *ArchiveHandler) GetArchiveSettings(c *gin.Context) {
	c.JSON(http.StatusOK, ArchiveSettingsResponse{
		ArchivePath:   h.archiver.GetArchivePath(),
		ArchiveDays:   h.archiver.GetArchiveDays(),
		HasPassphrase: h.archiver.HasPassphrase(),
	})
}

type UpdateArchiveSettingsRequest struct {
	ArchiveDays int `json:"archive_days" binding:"required,min=1,max=365"`
}

func (h *ArchiveHandler) UpdateArchiveSettings(c *gin.Context) {
	var req UpdateArchiveSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.archiver.SetArchiveDays(req.ArchiveDays)

	c.JSON(http.StatusOK, gin.H{
		"message":     "settings updated",
		"archive_days": req.ArchiveDays,
	})
}

type RestoreJobRequest struct {
	OriginalID int64 `json:"original_id" binding:"required"`
}

func (h *ArchiveHandler) RestoreJob(c *gin.Context) {
	var req RestoreJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.archiver.RestoreJobFromArchive(c.Request.Context(), req.OriginalID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "job restored",
		"original_id": req.OriginalID,
	})
}

type ArchiveStatsResponse struct {
	TotalArchives   int   `json:"total_archives"`
	TotalSize       int64 `json:"total_size_bytes"`
	TotalJobsStored int   `json:"total_jobs_stored"`
	OldestArchive   string `json:"oldest_archive,omitempty"`
	NewestArchive   string `json:"newest_archive,omitempty"`
}

func (h *ArchiveHandler) GetArchiveStats(c *gin.Context) {
	archives, err := h.archiver.ListArchives()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get archive stats"})
		return
	}

	var totalSize int64
	var totalJobsStored int
	var oldestArchive, newestArchive string

	for _, a := range archives {
		totalSize += a.Size
		totalJobsStored += a.JobCount
		if oldestArchive == "" || a.Filename < oldestArchive {
			oldestArchive = a.Filename
		}
		if newestArchive == "" || a.Filename > newestArchive {
			newestArchive = a.Filename
		}
	}

	err = h.db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM archive_jobs").Scan(&totalJobsStored)
	if err != nil {
		totalJobsStored = 0
	}

	c.JSON(http.StatusOK, ArchiveStatsResponse{
		TotalArchives:   len(archives),
		TotalSize:       totalSize,
		TotalJobsStored: totalJobsStored,
		OldestArchive:   oldestArchive,
		NewestArchive:   newestArchive,
	})
}

func (h *ArchiveHandler) DownloadArchivePath(c *gin.Context) {
	filename := c.Param("filename")
	
	filePath := filepath.Join(h.archiver.GetArchivePath(), filename)
	
	info, err := os.Stat(filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "archive not found"})
		return
	}

	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", fmt.Sprintf("%d", info.Size()))

	c.File(filePath)
}

func (h *ArchiveHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/archives", h.ListArchives)
	r.GET("/archives/stats", h.GetArchiveStats)
	r.GET("/archives/:filename", h.GetArchiveInfo)
	r.GET("/archives/:filename/download", h.DownloadArchive)
	r.GET("/archives/:filename/raw", h.DownloadArchivePath)
	r.DELETE("/archives/:filename", h.DeleteArchive)
	r.POST("/archives/run", h.TriggerArchive)
	r.POST("/archives/restore", h.RestoreJob)
	r.GET("/settings/archival", h.GetArchiveSettings)
	r.PUT("/settings/archival", h.UpdateArchiveSettings)
	r.PUT("/settings/archival/passphrase", h.SetPassphrase)
}
