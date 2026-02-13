package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
)

type DashboardStats struct {
	TodayPrints     int64
	PrintsTrend     int
	QueueDepth      int
	ProcessingJobs  int
	OnlinePrinters  int
	TotalPrinters   int
	PausedPrinters  int
	OfflinePrinters int
	FailedToday     int
	FailedJobs      int
}

type PrinterWithStatus struct {
	ID                   int64
	Name                 string
	IPAddress            string
	Port                 int
	Status               string
	StatusClass          string
	StatusIndicatorClass string
	LastSeenAt           *time.Time
	LastSeenAtFormatted  string
	CanPrint             bool
	Warning              string
	Error                string
}

type JobSummary struct {
	ID                 int64
	PrinterName        string
	TemplateName       string
	Status             string
	StatusClass        string
	Copies             int
	CreatedAtFormatted string
}

type DashboardData struct {
	Title    string
	Stats    DashboardStats
	Printers []PrinterWithStatus
	Jobs     []JobSummary
}

type WebUIHandler struct {
	db             *sql.DB
	queue          *core.Queue
	printerManager *core.PrinterManager
}

func NewWebUIHandler(database *sql.DB, queue *core.Queue, printerManager *core.PrinterManager) *WebUIHandler {
	return &WebUIHandler{
		db:             database,
		queue:          queue,
		printerManager: printerManager,
	}
}

func (h *WebUIHandler) Dashboard(c *gin.Context) {
	data := DashboardData{
		Title: "Dashboard",
	}

	data.Stats = h.getDashboardStats(c)
	data.Printers = h.getPrinterStatuses(c)
	data.Jobs = h.getRecentJobs(c)

	c.HTML(http.StatusOK, "dashboard", data)
}

func (h *WebUIHandler) getDashboardStats(c *gin.Context) DashboardStats {
	stats := DashboardStats{}
	ctx := c.Request.Context()
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterdayStart := todayStart.AddDate(0, 0, -1)

	h.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(count), 0) FROM print_counters WHERE date >= ?",
		todayStart.Format("2006-01-02"),
	).Scan(&stats.TodayPrints)

	var yesterdayPrints int64
	h.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(count), 0) FROM print_counters WHERE date >= ? AND date < ?",
		yesterdayStart.Format("2006-01-02"),
		todayStart.Format("2006-01-02"),
	).Scan(&yesterdayPrints)

	if yesterdayPrints > 0 {
		stats.PrintsTrend = int((float64(stats.TodayPrints-yesterdayPrints) / float64(yesterdayPrints)) * 100)
	} else if stats.TodayPrints > 0 {
		stats.PrintsTrend = 100
	}

	queueStats := h.queue.GetStats()
	stats.QueueDepth = queueStats.Pending
	stats.ProcessingJobs = queueStats.Processing
	stats.FailedJobs = queueStats.Failed

	printers, err := db.Printers.ListPrinters(ctx)
	if err == nil {
		stats.TotalPrinters = len(printers)
		for _, p := range printers {
			switch p.Status {
			case "online", "idle", "standby":
				stats.OnlinePrinters++
			case "paused":
				stats.PausedPrinters++
			case "offline":
				stats.OfflinePrinters++
			}
		}
	}

	h.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM print_jobs WHERE status = 'failed' AND created_at >= ?",
		todayStart,
	).Scan(&stats.FailedToday)

	return stats
}

func (h *WebUIHandler) getPrinterStatuses(c *gin.Context) []PrinterWithStatus {
	ctx := c.Request.Context()
	printers, err := db.Printers.ListPrinters(ctx)
	if err != nil {
		return nil
	}

	statuses := make([]PrinterWithStatus, 0, len(printers))
	for _, p := range printers {
		ps := PrinterWithStatus{
			ID:        p.ID,
			Name:      p.Name,
			IPAddress: p.IPAddress,
			Port:      p.Port,
			Status:    p.Status,
			LastSeenAt: p.LastSeenAt,
		}

		ps.StatusClass = getStatusClass(p.Status)
		ps.StatusIndicatorClass = getIndicatorClass(p.Status)
		ps.CanPrint = p.Status == "online" || p.Status == "idle" || p.Status == "standby"

		if p.LastSeenAt != nil {
			ps.LastSeenAtFormatted = formatLastSeen(*p.LastSeenAt)
		}

		if h.printerManager != nil {
			status, err := h.printerManager.CheckStatus(p.ID)
			if err == nil {
				if status.Warning != "" && status.Warning != "none" {
					ps.Warning = status.Warning
				}
				if status.Error != "" && status.Error != "none" {
					ps.Error = status.Error
				}
			}
		}

		statuses = append(statuses, ps)
	}

	return statuses
}

func (h *WebUIHandler) getRecentJobs(c *gin.Context) []JobSummary {
	ctx := c.Request.Context()
	filter := db.JobFilter{
		Limit:   10,
		Offset:  0,
		OrderBy: "created_at",
		OrderDir: "DESC",
	}

	jobs, err := db.Jobs.ListJobs(ctx, filter)
	if err != nil {
		return nil
	}

	printerNames := make(map[int64]string)
	templateNames := make(map[int64]string)

	for _, job := range jobs {
		if _, ok := printerNames[job.PrinterID]; !ok {
			if p, err := db.Printers.GetPrinterByID(ctx, job.PrinterID); err == nil {
				printerNames[job.PrinterID] = p.Name
			}
		}
		if _, ok := templateNames[job.TemplateID]; !ok {
			if t, err := db.Templates.GetTemplateByID(ctx, job.TemplateID); err == nil {
				templateNames[job.TemplateID] = t.Name
			}
		}
	}

	summaries := make([]JobSummary, 0, len(jobs))
	for _, job := range jobs {
		summary := JobSummary{
			ID:                 job.ID,
			PrinterName:        printerNames[job.PrinterID],
			TemplateName:       templateNames[job.TemplateID],
			Status:             job.Status,
			StatusClass:        getJobStatusClass(job.Status),
			Copies:             job.Copies,
			CreatedAtFormatted: job.CreatedAt.Format("Jan 2, 15:04"),
		}
		summaries = append(summaries, summary)
	}

	return summaries
}

func (h *WebUIHandler) GetDashboardStats(c *gin.Context) {
	stats := h.getDashboardStats(c)
	c.JSON(http.StatusOK, stats)
}

func (h *WebUIHandler) GetPrinterStatusCard(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid printer id"})
		return
	}

	printer, err := db.Printers.GetPrinterByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "printer not found"})
		return
	}

	ps := PrinterWithStatus{
		ID:          printer.ID,
		Name:        printer.Name,
		IPAddress:   printer.IPAddress,
		Port:        printer.Port,
		Status:      printer.Status,
		LastSeenAt:  printer.LastSeenAt,
		StatusClass: getStatusClass(printer.Status),
		StatusIndicatorClass: getIndicatorClass(printer.Status),
		CanPrint:    printer.Status == "online" || printer.Status == "idle" || printer.Status == "standby",
	}

	if printer.LastSeenAt != nil {
		ps.LastSeenAtFormatted = formatLastSeen(*printer.LastSeenAt)
	}

	if h.printerManager != nil {
		status, err := h.printerManager.CheckStatus(printer.ID)
		if err == nil {
			if status.Warning != "" && status.Warning != "none" {
				ps.Warning = status.Warning
			}
			if status.Error != "" && status.Error != "none" {
				ps.Error = status.Error
			}
		}
	}

	c.HTML(http.StatusOK, "printer-card", ps)
}

func getStatusClass(status string) string {
	switch status {
	case "online", "idle", "standby":
		return "bg-green-100 text-green-800"
	case "paused":
		return "bg-yellow-100 text-yellow-800"
	case "offline":
		return "bg-red-100 text-red-800"
	case "error":
		return "bg-red-100 text-red-800"
	default:
		return "bg-gray-100 text-gray-800"
	}
}

func getIndicatorClass(status string) string {
	switch status {
	case "online", "idle", "standby":
		return "bg-green-400"
	case "paused":
		return "bg-yellow-400"
	case "offline":
		return "bg-red-400"
	case "error":
		return "bg-red-400"
	default:
		return "bg-gray-400"
	}
}

func getJobStatusClass(status string) string {
	switch status {
	case "pending":
		return "bg-yellow-100 text-yellow-800"
	case "processing":
		return "bg-blue-100 text-blue-800"
	case "completed":
		return "bg-green-100 text-green-800"
	case "failed":
		return "bg-red-100 text-red-800"
	case "paused":
		return "bg-gray-100 text-gray-800"
	case "cancelled":
		return "bg-gray-100 text-gray-800"
	default:
		return "bg-gray-100 text-gray-800"
	}
}

func formatLastSeen(t time.Time) string {
	now := time.Now()
	diff := now.Sub(t)

	if diff < time.Minute {
		return "just now"
	} else if diff < time.Hour {
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return strconv.Itoa(mins) + " minutes ago"
	} else if diff < 24*time.Hour {
		hours := int(diff.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return strconv.Itoa(hours) + " hours ago"
	}
	return t.Format("Jan 2, 15:04")
}

func RegisterWebUIRoutes(router *gin.Engine, handler *WebUIHandler) {
	router.GET("/", handler.Dashboard)
	router.GET("/dashboard", handler.Dashboard)
	router.GET("/api/dashboard/stats", handler.GetDashboardStats)
	router.GET("/api/printers/:id/status", handler.GetPrinterStatusCard)
}
