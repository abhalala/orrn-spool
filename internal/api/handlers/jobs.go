package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"orrn-spool/internal/core"
	"orrn-spool/internal/db"
)

type CreateJobRequest struct {
	PrinterID  int64             `json:"printer_id" binding:"required"`
	TemplateID int64             `json:"template_id" binding:"required"`
	Variables  map[string]string `json:"variables" binding:"required"`
	Copies     int               `json:"copies"`
	Priority   int               `json:"priority"`
}

type JobResponse struct {
	ID           int64             `json:"id"`
	PrinterID    int64             `json:"printer_id"`
	PrinterName  string            `json:"printer_name,omitempty"`
	TemplateID   int64             `json:"template_id"`
	TemplateName string            `json:"template_name,omitempty"`
	Variables    map[string]string `json:"variables"`
	TSPLContent  string            `json:"tspl_content,omitempty"`
	Status       string            `json:"status"`
	Priority     int               `json:"priority"`
	RetryCount   int               `json:"retry_count"`
	ErrorMessage string            `json:"error_message,omitempty"`
	Copies       int               `json:"copies"`
	SubmittedBy  string            `json:"submitted_by"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	CompletedAt  *time.Time        `json:"completed_at,omitempty"`
	Duration     *int64            `json:"duration_ms,omitempty"`
}

type ListJobsQuery struct {
	PrinterID int64  `form:"printer_id"`
	Status    string `form:"status"`
	FromDate  string `form:"from_date"`
	ToDate    string `form:"to_date"`
	Limit     int    `form:"limit" binding:"max=100"`
	Offset    int    `form:"offset"`
	SortBy    string `form:"sort_by"`
	SortDir   string `form:"sort_dir"`
}

type QueueResponse struct {
	Pending    int `json:"pending"`
	Processing int `json:"processing"`
	Paused     int `json:"paused"`
	Failed     int `json:"failed"`
	Completed  int `json:"completed"`
	Total      int `json:"total"`
}

type JobStatsResponse struct {
	TodayTotal     int64          `json:"today_total"`
	TodaySuccess   int64          `json:"today_success"`
	TodayFailed    int64          `json:"today_failed"`
	WeekTotal      int64          `json:"week_total"`
	MonthTotal     int64          `json:"month_total"`
	ByPrinter      []PrinterStats `json:"by_printer"`
	ByStatus       []StatusStats  `json:"by_status"`
	AvgProcessTime int64          `json:"avg_process_time_ms"`
}

type PrinterStats struct {
	PrinterID   int64  `json:"printer_id"`
	PrinterName string `json:"printer_name"`
	Count       int64  `json:"count"`
}

type StatusStats struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type JobHandler struct {
	db            *sql.DB
	queue         *core.Queue
	tsplGenerator *core.TSPL2Generator
}

func NewJobHandler(database *sql.DB, queue *core.Queue, tsplGenerator *core.TSPL2Generator) *JobHandler {
	return &JobHandler{
		db:            database,
		queue:         queue,
		tsplGenerator: tsplGenerator,
	}
}

func (h *JobHandler) CreateJob(c *gin.Context) {
	var req CreateJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Copies <= 0 {
		req.Copies = 1
	}

	printer, err := db.Printers.GetPrinterByID(c.Request.Context(), req.PrinterID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "printer not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get printer"})
		return
	}

	if printer.Status == "paused" || printer.Status == "offline" {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("printer is %s", printer.Status)})
		return
	}

	template, err := db.Templates.GetTemplateByID(c.Request.Context(), req.TemplateID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	schema, err := h.tsplGenerator.ParseSchema(template.SchemaJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid template schema"})
		return
	}

	if err := h.tsplGenerator.ValidateVariables(schema, req.Variables); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	variablesJSON, err := json.Marshal(req.Variables)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to serialize variables"})
		return
	}

	clientIP := c.ClientIP()

	job := &core.Job{
		PrinterID:     req.PrinterID,
		TemplateID:    req.TemplateID,
		VariablesJSON: string(variablesJSON),
		Priority:      req.Priority,
		Copies:        req.Copies,
		SubmittedBy:   clientIP,
		Status:        core.JobStatusPending,
	}

	jobID, err := h.queue.Enqueue(job)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue job"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":      jobID,
		"message": "job submitted successfully",
	})
}

func (h *JobHandler) ListJobs(c *gin.Context) {
	var query ListJobsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 100 {
		query.Limit = 100
	}

	filter := db.JobFilter{
		PrinterID: query.PrinterID,
		Status:    query.Status,
		Limit:     query.Limit,
		Offset:    query.Offset,
		OrderBy:   query.SortBy,
		OrderDir:  query.SortDir,
	}

	if query.FromDate != "" {
		t, err := time.Parse("2006-01-02", query.FromDate)
		if err == nil {
			filter.FromDate = &t
		}
	}
	if query.ToDate != "" {
		t, err := time.Parse("2006-01-02", query.ToDate)
		if err == nil {
			endOfDay := t.Add(24*time.Hour - time.Second)
			filter.ToDate = &endOfDay
		}
	}

	jobs, err := db.Jobs.ListJobs(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list jobs"})
		return
	}

	printerNames := make(map[int64]string)
	templateNames := make(map[int64]string)

	for _, job := range jobs {
		if _, ok := printerNames[job.PrinterID]; !ok {
			if p, err := db.Printers.GetPrinterByID(c.Request.Context(), job.PrinterID); err == nil {
				printerNames[job.PrinterID] = p.Name
			}
		}
		if _, ok := templateNames[job.TemplateID]; !ok {
			if t, err := db.Templates.GetTemplateByID(c.Request.Context(), job.TemplateID); err == nil {
				templateNames[job.TemplateID] = t.Name
			}
		}
	}

	responses := make([]JobResponse, 0, len(jobs))
	for _, job := range jobs {
		resp := h.jobToResponse(job)
		resp.PrinterName = printerNames[job.PrinterID]
		resp.TemplateName = templateNames[job.TemplateID]
		responses = append(responses, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"jobs":   responses,
		"limit":  query.Limit,
		"offset": query.Offset,
		"count":  len(responses),
	})
}

func (h *JobHandler) GetJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	job, err := db.Jobs.GetJobByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job"})
		return
	}

	resp := h.jobToResponse(job)

	if printer, err := db.Printers.GetPrinterByID(c.Request.Context(), job.PrinterID); err == nil {
		resp.PrinterName = printer.Name
	}
	if template, err := db.Templates.GetTemplateByID(c.Request.Context(), job.TemplateID); err == nil {
		resp.TemplateName = template.Name
	}

	if job.StartedAt != nil && job.CompletedAt != nil {
		duration := job.CompletedAt.Sub(*job.StartedAt).Milliseconds()
		resp.Duration = &duration
	}

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) DeleteJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	job, err := db.Jobs.GetJobByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get job"})
		return
	}

	if job.Status == "processing" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete processing job"})
		return
	}

	if err := db.Jobs.DeleteJob(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete job"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "job deleted"})
}

func (h *JobHandler) CancelJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	if err := h.queue.CancelJob(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "job cancelled"})
}

func (h *JobHandler) RetryJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	if err := h.queue.RetryJob(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "job queued for retry"})
}

func (h *JobHandler) ReprintJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	newJobID, err := h.queue.ReprintJob(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":   "job reprinted",
		"new_job_id": newJobID,
	})
}

func (h *JobHandler) PauseJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	if err := h.queue.PauseJob(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "job paused"})
}

func (h *JobHandler) ResumeJob(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid job id"})
		return
	}

	if err := h.queue.ResumeJob(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "job resumed"})
}

func (h *JobHandler) GetQueue(c *gin.Context) {
	stats := h.queue.GetStats()

	resp := QueueResponse{
		Pending:    stats.Pending,
		Processing: stats.Processing,
		Paused:     stats.Paused,
		Failed:     stats.Failed,
		Completed:  stats.Completed,
		Total:      stats.Total,
	}

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) GetJobStats(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := todayStart.AddDate(0, 0, -7)
	monthStart := todayStart.AddDate(0, -1, 0)

	resp := &JobStatsResponse{
		ByPrinter: make([]PrinterStats, 0),
		ByStatus:  make([]StatusStats, 0),
	}

	h.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM print_jobs WHERE created_at >= ?",
		todayStart,
	).Scan(&resp.TodayTotal)

	h.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM print_jobs WHERE created_at >= ? AND status = 'completed'",
		todayStart,
	).Scan(&resp.TodaySuccess)

	h.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM print_jobs WHERE created_at >= ? AND status = 'failed'",
		todayStart,
	).Scan(&resp.TodayFailed)

	h.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM print_jobs WHERE created_at >= ?",
		weekStart,
	).Scan(&resp.WeekTotal)

	h.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM print_jobs WHERE created_at >= ?",
		monthStart,
	).Scan(&resp.MonthTotal)

	rows, err := h.db.QueryContext(ctx, `
		SELECT printer_id, COUNT(*) as count
		FROM print_jobs
		WHERE created_at >= ?
		GROUP BY printer_id
		ORDER BY count DESC
		LIMIT 10
	`, weekStart)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ps PrinterStats
			if err := rows.Scan(&ps.PrinterID, &ps.Count); err != nil {
				continue
			}
			if printer, err := db.Printers.GetPrinterByID(ctx, ps.PrinterID); err == nil {
				ps.PrinterName = printer.Name
			}
			resp.ByPrinter = append(resp.ByPrinter, ps)
		}
	}

	rows, err = h.db.QueryContext(ctx, `
		SELECT status, COUNT(*) as count
		FROM print_jobs
		GROUP BY status
		ORDER BY count DESC
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ss StatusStats
			if err := rows.Scan(&ss.Status, &ss.Count); err != nil {
				continue
			}
			resp.ByStatus = append(resp.ByStatus, ss)
		}
	}

	h.db.QueryRowContext(ctx, `
		SELECT AVG(
			CAST((julianday(completed_at) - julianday(started_at)) * 86400000 AS INTEGER)
		)
		FROM print_jobs
		WHERE status = 'completed' AND started_at IS NOT NULL AND completed_at IS NOT NULL
		AND completed_at >= ?
	`, weekStart).Scan(&resp.AvgProcessTime)

	c.JSON(http.StatusOK, resp)
}

func (h *JobHandler) LegacyPrintHandler(c *gin.Context) {
	layout := c.Param("layout")
	uid := c.Param("uid")

	if layout == "" || uid == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "layout and uid are required"})
		return
	}

	template, err := db.Templates.GetTemplateByName(c.Request.Context(), layout)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("template '%s' not found", layout)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	variables := map[string]string{
		"uid": uid,
	}

	schema, err := h.tsplGenerator.ParseSchema(template.SchemaJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid template schema"})
		return
	}

	variables = h.tsplGenerator.MergeVariablesWithDefaults(schema, variables)

	if err := h.tsplGenerator.ValidateVariables(schema, variables); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	printers, err := db.Printers.ListPrinters(c.Request.Context())
	if err != nil || len(printers) == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "no printers available"})
		return
	}

	var printer *db.Printer
	for _, p := range printers {
		if p.Status == "online" {
			printer = p
			break
		}
	}
	if printer == nil {
		for _, p := range printers {
			if p.Status != "offline" {
				printer = p
				break
			}
		}
	}
	if printer == nil {
		printer = printers[0]
	}

	variablesJSON, _ := json.Marshal(variables)
	clientIP := c.ClientIP()

	job := &core.Job{
		PrinterID:     printer.ID,
		TemplateID:    template.ID,
		VariablesJSON: string(variablesJSON),
		Copies:        1,
		SubmittedBy:   clientIP,
		Status:        core.JobStatusPending,
	}

	jobID, err := h.queue.Enqueue(job)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to submit job"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id":      jobID,
		"printer":     printer.Name,
		"template":    template.Name,
		"uid":         uid,
		"status":      "queued",
		"message":     "print job submitted",
	})
}

func (h *JobHandler) jobToResponse(job *db.PrintJob) JobResponse {
	var variables map[string]string
	if job.VariablesJSON != "" {
		json.Unmarshal([]byte(job.VariablesJSON), &variables)
	}
	if variables == nil {
		variables = make(map[string]string)
	}

	return JobResponse{
		ID:           job.ID,
		PrinterID:    job.PrinterID,
		TemplateID:   job.TemplateID,
		Variables:    variables,
		TSPLContent:  job.TSPLContent,
		Status:       job.Status,
		Priority:     job.Priority,
		RetryCount:   job.RetryCount,
		ErrorMessage: job.ErrorMessage,
		Copies:       job.Copies,
		SubmittedBy:  job.SubmittedBy,
		CreatedAt:    job.CreatedAt,
		StartedAt:    job.StartedAt,
		CompletedAt:  job.CompletedAt,
	}
}

func (h *JobHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/jobs", h.ListJobs)
	r.POST("/jobs", h.CreateJob)
	r.GET("/jobs/queue", h.GetQueue)
	r.GET("/jobs/stats", h.GetJobStats)
	r.GET("/jobs/:id", h.GetJob)
	r.DELETE("/jobs/:id", h.DeleteJob)
	r.POST("/jobs/:id/cancel", h.CancelJob)
	r.POST("/jobs/:id/retry", h.RetryJob)
	r.POST("/jobs/:id/reprint", h.ReprintJob)
	r.POST("/jobs/:id/pause", h.PauseJob)
	r.POST("/jobs/:id/resume", h.ResumeJob)
}

func (h *JobHandler) RegisterLegacyRoutes(r *gin.Engine) {
	r.GET("/print/:layout/:uid", h.LegacyPrintHandler)
}
