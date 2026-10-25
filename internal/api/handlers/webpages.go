package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/ai"
	"github.com/orrn/spool/internal/archive"
	"github.com/orrn/spool/internal/config"
	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
)

// Toast is one notification shown by the layout's toast area.
type Toast struct {
	Type    string
	Message string
}

// PageBase carries what the shared layout (sidebar, title, toasts) needs. Every
// full-page view model embeds it, so layout fields resolve on any page.
type PageBase struct {
	Title         string
	Active        string
	Authenticated bool
	Toasts        []Toast
}

// newPage builds the layout data for a page behind RequireAuth.
func newPage(title, active string) PageBase {
	return PageBase{Title: title, Active: active, Authenticated: true}
}

// WebUIDeps are the optional services the settings page reads.
type WebUIDeps struct {
	Config   *config.Config
	Archiver *archive.Archiver
	AI       *ai.GeminiClient
}

// WithDeps attaches the services the settings page needs and returns the handler.
func (h *WebUIHandler) WithDeps(deps WebUIDeps) *WebUIHandler {
	h.deps = deps
	return h
}

// PrinterView is a printer as the Printers page and its modals show it.
type PrinterView struct {
	ID          int64
	Name        string
	IPAddress   string
	Port        int
	DPI         int
	Status      string
	LabelWidth  float64
	LabelHeight float64
	Gap         float64
	LastSeen    *time.Time
	TotalPrints int64
}

func printerView(p *db.Printer) PrinterView {
	return PrinterView{
		ID: p.ID, Name: p.Name, IPAddress: p.IPAddress, Port: p.Port, DPI: p.DPI,
		Status: p.Status, LabelWidth: p.LabelWidthMM, LabelHeight: p.LabelHeightMM,
		Gap: p.GapMM, LastSeen: p.LastSeenAt, TotalPrints: p.TotalPrints,
	}
}

func (h *WebUIHandler) printerViews(c *gin.Context) []PrinterView {
	printers, err := db.Printers.ListPrinters(c.Request.Context())
	if err != nil {
		return nil
	}
	views := make([]PrinterView, 0, len(printers))
	for _, p := range printers {
		views = append(views, printerView(p))
	}
	return views
}

type PrintersPage struct {
	PageBase
	Printers []PrinterView
}

func (h *WebUIHandler) PrintersPage(c *gin.Context) {
	c.HTML(http.StatusOK, "printers", PrintersPage{
		PageBase: newPage("Printers", "printers"),
		Printers: h.printerViews(c),
	})
}

// PrintersGrid renders only the grid, for the page's HTMX refresh.
func (h *WebUIHandler) PrintersGrid(c *gin.Context) {
	c.HTML(http.StatusOK, "printers-grid", PrintersPage{Printers: h.printerViews(c)})
}

type PrinterModalData struct {
	Printer   PrinterView
	Templates []TemplateView
	Error     string
}

func (h *WebUIHandler) printerFromParam(c *gin.Context) (*db.Printer, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid printer id")
		return nil, false
	}
	p, err := db.Printers.GetPrinterByID(c.Request.Context(), id)
	if err != nil {
		c.String(http.StatusNotFound, "printer not found")
		return nil, false
	}
	return p, true
}

func (h *WebUIHandler) PrinterAddModal(c *gin.Context) {
	c.HTML(http.StatusOK, "printer-form", PrinterModalData{})
}

func (h *WebUIHandler) PrinterEditModal(c *gin.Context) {
	p, ok := h.printerFromParam(c)
	if !ok {
		return
	}
	c.HTML(http.StatusOK, "printer-form", PrinterModalData{Printer: printerView(p)})
}

func (h *WebUIHandler) PrinterTestModal(c *gin.Context) {
	p, ok := h.printerFromParam(c)
	if !ok {
		return
	}
	c.HTML(http.StatusOK, "test-print", PrinterModalData{
		Printer:   printerView(p),
		Templates: h.templateViews(c),
	})
}

// ---- templates ----------------------------------------------------------

// TemplateView is a label template as the Templates page lists it.
type TemplateView struct {
	ID            int64
	Name          string
	Description   string
	LabelWidth    float64
	LabelHeight   float64
	Gap           float64
	DPI           int
	ElementCount  int
	VariableCount int
	UpdatedAt     time.Time
}

func templateView(t *db.LabelTemplate) TemplateView {
	v := TemplateView{
		ID: t.ID, Name: t.Name, Description: t.Description,
		LabelWidth: t.WidthMM, LabelHeight: t.HeightMM, UpdatedAt: t.UpdatedAt,
	}
	var schema core.LabelSchema
	if json.Unmarshal([]byte(t.SchemaJSON), &schema) == nil {
		v.Gap = schema.GapMM
		v.DPI = schema.DPI
		v.ElementCount = len(schema.Elements)
		v.VariableCount = len(schema.Variables)
	}
	if v.DPI == 0 {
		v.DPI = 203
	}
	return v
}

func (h *WebUIHandler) templateViews(c *gin.Context) []TemplateView {
	templates, err := db.Templates.ListTemplates(c.Request.Context())
	if err != nil {
		return nil
	}
	views := make([]TemplateView, 0, len(templates))
	for _, t := range templates {
		views = append(views, templateView(t))
	}
	return views
}

type TemplatesPage struct {
	PageBase
	Templates []TemplateView
}

func (h *WebUIHandler) TemplatesPage(c *gin.Context) {
	c.HTML(http.StatusOK, "templates", TemplatesPage{
		PageBase:  newPage("Label Templates", "templates"),
		Templates: h.templateViews(c),
	})
}

// editorVariable and editorTemplate are the shapes the template editor's
// Alpine component works with (see modals/template_form.html).
type editorVariable struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Required     bool   `json:"required"`
	DefaultValue string `json:"default_value"`
}

type editorTemplate struct {
	ID          int64               `json:"id,omitempty"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	LabelWidth  float64             `json:"label_width"`
	LabelHeight float64             `json:"label_height"`
	Gap         float64             `json:"gap"`
	DPI         int                 `json:"dpi"`
	Elements    []core.LabelElement `json:"elements"`
	Variables   []editorVariable    `json:"variables"`
}

func toEditorTemplate(t *db.LabelTemplate) editorTemplate {
	e := editorTemplate{
		ID: t.ID, Name: t.Name, Description: t.Description,
		LabelWidth: t.WidthMM, LabelHeight: t.HeightMM, DPI: 203,
		Elements: []core.LabelElement{}, Variables: []editorVariable{},
	}
	var schema core.LabelSchema
	if json.Unmarshal([]byte(t.SchemaJSON), &schema) == nil {
		e.Gap = schema.GapMM
		if schema.DPI > 0 {
			e.DPI = schema.DPI
		}
		if schema.Elements != nil {
			e.Elements = schema.Elements
		}
		names := make([]string, 0, len(schema.Variables))
		for name := range schema.Variables {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			def := schema.Variables[name]
			e.Variables = append(e.Variables, editorVariable{
				Name: name, Type: def.Type, Required: def.Required, DefaultValue: def.Default,
			})
		}
	}
	return e
}

type TemplateModalData struct {
	Template      any
	TSPLPreview   string
	TSPLCode      string
	MergedPreview string
}

func (h *WebUIHandler) templateFromParam(c *gin.Context) (*db.LabelTemplate, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid template id")
		return nil, false
	}
	t, err := db.Templates.GetTemplateByID(c.Request.Context(), id)
	if err != nil {
		c.String(http.StatusNotFound, "template not found")
		return nil, false
	}
	return t, true
}

func tsplPreview(t *db.LabelTemplate) string {
	var schema core.LabelSchema
	if json.Unmarshal([]byte(t.SchemaJSON), &schema) != nil {
		return ""
	}
	out, err := core.NewTSPL2Generator().GeneratePreview(&schema)
	if err != nil {
		return ""
	}
	return out
}

// TemplateCreateModal opens the editor on a blank label (the editor's own
// defaults apply when the template is null).
func (h *WebUIHandler) TemplateCreateModal(c *gin.Context) {
	c.HTML(http.StatusOK, "template-form", TemplateModalData{})
}

func (h *WebUIHandler) TemplateEditModal(c *gin.Context) {
	t, ok := h.templateFromParam(c)
	if !ok {
		return
	}
	c.HTML(http.StatusOK, "template-form", TemplateModalData{
		Template:    toEditorTemplate(t),
		TSPLPreview: tsplPreview(t),
	})
}

func (h *WebUIHandler) TemplatePreviewModal(c *gin.Context) {
	t, ok := h.templateFromParam(c)
	if !ok {
		return
	}
	code := tsplPreview(t)
	c.HTML(http.StatusOK, "template-preview", TemplateModalData{
		Template:      toEditorTemplate(t),
		TSPLCode:      code,
		MergedPreview: code,
	})
}

// ---- queue --------------------------------------------------------------

type QueueJobView struct {
	ID               int64
	PrinterName      string
	TemplateName     string
	Status           string
	Priority         int
	Copies           int
	VariablesSummary string
	CreatedAt        time.Time
	StartedAt        *time.Time
}

type QueueViewStats struct {
	Pending        int
	Processing     int
	Paused         int
	CompletedToday int
}

type QueuePage struct {
	PageBase
	Stats          QueueViewStats
	ProcessingJobs []QueueJobView
	PendingJobs    []QueueJobView
	PausedJobs     []QueueJobView
}

// nameLookup caches printer and template names for one request.
type nameLookup struct {
	c         *gin.Context
	printers  map[int64]string
	templates map[int64]string
}

func newNameLookup(c *gin.Context) *nameLookup {
	return &nameLookup{c: c, printers: map[int64]string{}, templates: map[int64]string{}}
}

func (n *nameLookup) printer(id int64) string {
	if name, ok := n.printers[id]; ok {
		return name
	}
	name := ""
	if p, err := db.Printers.GetPrinterByID(n.c.Request.Context(), id); err == nil {
		name = p.Name
	}
	n.printers[id] = name
	return name
}

func (n *nameLookup) template(id int64) string {
	if name, ok := n.templates[id]; ok {
		return name
	}
	name := ""
	if t, err := db.Templates.GetTemplateByID(n.c.Request.Context(), id); err == nil {
		name = t.Name
	}
	n.templates[id] = name
	return name
}

// variablesSummary renders a job's variables as "a=1, b=2" in key order.
func variablesSummary(variablesJSON string) string {
	var vars map[string]any
	if json.Unmarshal([]byte(variablesJSON), &vars) != nil || len(vars) == 0 {
		return ""
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, vars[k]))
	}
	s := strings.Join(parts, ", ")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return s
}

func (h *WebUIHandler) queueData(c *gin.Context) QueuePage {
	ctx := c.Request.Context()
	names := newNameLookup(c)
	data := QueuePage{PageBase: newPage("Print Queue", "queue")}

	load := func(status string) []QueueJobView {
		jobs, err := db.Jobs.ListJobs(ctx, db.JobFilter{
			Status: status, Limit: 100, OrderBy: "priority DESC, created_at", OrderDir: "ASC",
		})
		if err != nil {
			return nil
		}
		views := make([]QueueJobView, 0, len(jobs))
		for _, j := range jobs {
			views = append(views, QueueJobView{
				ID: j.ID, PrinterName: names.printer(j.PrinterID), TemplateName: names.template(j.TemplateID),
				Status: j.Status, Priority: j.Priority, Copies: j.Copies,
				VariablesSummary: variablesSummary(j.VariablesJSON), CreatedAt: j.CreatedAt, StartedAt: j.StartedAt,
			})
		}
		return views
	}
	data.ProcessingJobs = load("processing")
	data.PendingJobs = load("pending")
	data.PausedJobs = load("paused")

	data.Stats.Processing = len(data.ProcessingJobs)
	data.Stats.Pending = len(data.PendingJobs)
	data.Stats.Paused = len(data.PausedJobs)
	if h.queue != nil {
		qs := h.queue.GetStats()
		data.Stats.Processing, data.Stats.Pending, data.Stats.Paused = qs.Processing, qs.Pending, qs.Paused
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	_ = h.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM print_jobs WHERE status = 'completed' AND completed_at >= ?", today,
	).Scan(&data.Stats.CompletedToday)
	return data
}

func (h *WebUIHandler) QueuePage(c *gin.Context) {
	c.HTML(http.StatusOK, "queue", h.queueData(c))
}

// QueueContent renders only the job sections, for the page's 5 second refresh.
func (h *WebUIHandler) QueueContent(c *gin.Context) {
	c.HTML(http.StatusOK, "queue-sections", h.queueData(c))
}

// ---- history ------------------------------------------------------------

type HistoryJobView struct {
	ID           int64
	PrinterName  string
	TemplateName string
	Status       string
	CreatedAt    time.Time
	Duration     int64
}

type HistoryPrinterStat struct {
	PrinterName string
	Count       int64
	Percentage  int
}

type HistoryStats struct {
	TodaySuccessRate  float64
	WeekSuccessRate   float64
	MonthSuccessRate  float64
	AvgProcessingTime int64
	PrinterStats      []HistoryPrinterStat
}

type HistoryPagination struct {
	CurrentPage int
	Total       int
	From        int
	To          int
	HasPrev     bool
	HasNext     bool
	PrevPage    int
	NextPage    int
	Pages       []int
}

type HistoryPage struct {
	PageBase
	Printers   []PrinterView
	Jobs       []HistoryJobView
	JobIDs     []int64
	Stats      HistoryStats
	Pagination *HistoryPagination
}

const historyPageSize = 25

func (h *WebUIHandler) successRate(c *gin.Context, since time.Time) float64 {
	var done, failed int
	_ = h.db.QueryRowContext(c.Request.Context(),
		`SELECT COALESCE(SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END), 0)
		 FROM print_jobs WHERE created_at >= ?`, since).Scan(&done, &failed)
	if done+failed == 0 {
		return 0
	}
	return float64(done) * 100 / float64(done+failed)
}

func (h *WebUIHandler) historyStats(c *gin.Context) HistoryStats {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	stats := HistoryStats{
		TodaySuccessRate: h.successRate(c, today),
		WeekSuccessRate:  h.successRate(c, today.AddDate(0, 0, -6)),
		MonthSuccessRate: h.successRate(c, today.AddDate(0, 0, -29)),
	}
	ctx := c.Request.Context()
	var avg sql.NullFloat64
	_ = h.db.QueryRowContext(ctx,
		`SELECT AVG((julianday(completed_at) - julianday(started_at)) * 86400000.0)
		 FROM print_jobs WHERE status = 'completed' AND started_at IS NOT NULL AND completed_at IS NOT NULL`).Scan(&avg)
	if avg.Valid {
		stats.AvgProcessingTime = int64(avg.Float64)
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT COALESCE(p.name, 'Unknown'), COUNT(*) AS n FROM print_jobs j
		 LEFT JOIN printers p ON p.id = j.printer_id GROUP BY j.printer_id ORDER BY n DESC LIMIT 10`)
	if err == nil {
		defer rows.Close()
		var total int64
		for rows.Next() {
			var s HistoryPrinterStat
			if rows.Scan(&s.PrinterName, &s.Count) == nil {
				stats.PrinterStats = append(stats.PrinterStats, s)
				total += s.Count
			}
		}
		for i := range stats.PrinterStats {
			if total > 0 {
				stats.PrinterStats[i].Percentage = int(stats.PrinterStats[i].Count * 100 / total)
			}
		}
	}
	return stats
}

// historyFilter reads the filter, sort and page query the history table sends.
func historyFilter(c *gin.Context) (db.JobFilter, int) {
	filter := db.JobFilter{OrderBy: "created_at", OrderDir: "DESC"}
	if id, err := strconv.ParseInt(c.Query("printer"), 10, 64); err == nil && id > 0 {
		filter.PrinterID = id
	}
	switch s := c.Query("status"); s {
	case "completed", "failed", "cancelled":
		filter.Status = s
	}
	if t, err := time.Parse("2006-01-02", c.Query("dateFrom")); err == nil {
		filter.FromDate = &t
	}
	if t, err := time.Parse("2006-01-02", c.Query("dateTo")); err == nil {
		end := t.Add(24*time.Hour - time.Second)
		filter.ToDate = &end
	}
	switch c.Query("sort") {
	case "id":
		filter.OrderBy = "id"
	case "submitted":
		filter.OrderBy = "created_at"
	}
	if c.Query("dir") == "asc" {
		filter.OrderDir = "ASC"
	}
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	return filter, page
}

func (h *WebUIHandler) historyData(c *gin.Context) HistoryPage {
	filter, page := historyFilter(c)
	ctx := c.Request.Context()
	data := HistoryPage{PageBase: newPage("Job History", "history")}

	// History shows finished jobs only; with no status filter that is all three.
	finished := []string{"completed", "failed", "cancelled"}
	if filter.Status != "" {
		finished = []string{filter.Status}
	}
	countFilter := filter
	total := 0
	for _, status := range finished {
		countFilter.Status = status
		n, err := h.countJobs(c, countFilter)
		if err == nil {
			total += n
		}
	}

	// The database allows one connection, so read every row and close the
	// cursor before looking names up (a lookup inside the loop would wait on
	// the connection the open cursor holds).
	type historyRow struct {
		HistoryJobView
		printerID, templateID int64
	}
	var found []historyRow
	rows, err := h.db.QueryContext(ctx, historyQuery(filter, finished), historyArgs(filter, finished, page)...)
	if err == nil {
		for rows.Next() {
			var (
				r         historyRow
				printerID sql.NullInt64
				tplID     sql.NullInt64
				started   sql.NullTime
				completed sql.NullTime
			)
			if rows.Scan(&r.ID, &printerID, &tplID, &r.Status, &r.CreatedAt, &started, &completed) != nil {
				continue
			}
			r.printerID, r.templateID = printerID.Int64, tplID.Int64
			if started.Valid && completed.Valid {
				r.Duration = completed.Time.Sub(started.Time).Milliseconds()
			}
			found = append(found, r)
		}
		rows.Close()
	}
	names := newNameLookup(c)
	for _, r := range found {
		r.PrinterName = names.printer(r.printerID)
		r.TemplateName = names.template(r.templateID)
		data.Jobs = append(data.Jobs, r.HistoryJobView)
		data.JobIDs = append(data.JobIDs, r.ID)
	}
	if data.JobIDs == nil {
		data.JobIDs = []int64{}
	}

	pages := (total + historyPageSize - 1) / historyPageSize
	if pages > 1 {
		p := &HistoryPagination{
			CurrentPage: page, Total: total,
			From: (page-1)*historyPageSize + 1, To: (page-1)*historyPageSize + len(data.Jobs),
			HasPrev: page > 1, HasNext: page < pages, PrevPage: page - 1, NextPage: page + 1,
		}
		for i := 1; i <= pages && i <= 10; i++ {
			p.Pages = append(p.Pages, i)
		}
		data.Pagination = p
	}
	data.Printers = h.printerViews(c)
	return data
}

func (h *WebUIHandler) countJobs(c *gin.Context, f db.JobFilter) (int, error) {
	query := "SELECT COUNT(*) FROM print_jobs WHERE status = ?"
	args := []any{f.Status}
	if f.PrinterID > 0 {
		query += " AND printer_id = ?"
		args = append(args, f.PrinterID)
	}
	if f.FromDate != nil {
		query += " AND created_at >= ?"
		args = append(args, *f.FromDate)
	}
	if f.ToDate != nil {
		query += " AND created_at <= ?"
		args = append(args, *f.ToDate)
	}
	var n int
	err := h.db.QueryRowContext(c.Request.Context(), query, args...).Scan(&n)
	return n, err
}

func historyQuery(f db.JobFilter, statuses []string) string {
	q := "SELECT id, printer_id, template_id, status, created_at, started_at, completed_at FROM print_jobs WHERE status IN (" +
		strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",") + ")"
	if f.PrinterID > 0 {
		q += " AND printer_id = ?"
	}
	if f.FromDate != nil {
		q += " AND created_at >= ?"
	}
	if f.ToDate != nil {
		q += " AND created_at <= ?"
	}
	// OrderBy and OrderDir come from the fixed set in historyFilter.
	return q + fmt.Sprintf(" ORDER BY %s %s LIMIT ? OFFSET ?", f.OrderBy, f.OrderDir)
}

func historyArgs(f db.JobFilter, statuses []string, page int) []any {
	args := make([]any, 0, len(statuses)+5)
	for _, s := range statuses {
		args = append(args, s)
	}
	if f.PrinterID > 0 {
		args = append(args, f.PrinterID)
	}
	if f.FromDate != nil {
		args = append(args, *f.FromDate)
	}
	if f.ToDate != nil {
		args = append(args, *f.ToDate)
	}
	return append(args, historyPageSize, (page-1)*historyPageSize)
}

func (h *WebUIHandler) HistoryPage(c *gin.Context) {
	data := h.historyData(c)
	data.Stats = h.historyStats(c)
	c.HTML(http.StatusOK, "history", data)
}

// HistoryTable renders only the table, for filters, sorting and paging.
func (h *WebUIHandler) HistoryTable(c *gin.Context) {
	c.HTML(http.StatusOK, "history-table", h.historyData(c))
}

// ---- job modals ---------------------------------------------------------

type JobDetailView struct {
	ID           int64
	PrinterName  string
	PrinterIP    string
	TemplateName string
	Status       string
	Priority     int
	Copies       int
	ErrorMessage string
	TSPLContent  string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

type JobDetailData struct {
	Job                JobDetailView
	VariablesJSON      string
	WaitDuration       string
	ProcessingDuration string
	TotalDuration      string
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(100 * time.Millisecond).String()
}

func (h *WebUIHandler) JobDetailModal(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid job id")
		return
	}
	job, err := db.Jobs.GetJobByID(c.Request.Context(), id)
	if err != nil {
		c.String(http.StatusNotFound, "job not found")
		return
	}
	view := JobDetailView{
		ID: job.ID, Status: job.Status, Priority: job.Priority, Copies: job.Copies,
		ErrorMessage: job.ErrorMessage, TSPLContent: job.TSPLContent,
		CreatedAt: job.CreatedAt, StartedAt: job.StartedAt, CompletedAt: job.CompletedAt,
	}
	if p, err := db.Printers.GetPrinterByID(c.Request.Context(), job.PrinterID); err == nil {
		view.PrinterName, view.PrinterIP = p.Name, p.IPAddress
	}
	if t, err := db.Templates.GetTemplateByID(c.Request.Context(), job.TemplateID); err == nil {
		view.TemplateName = t.Name
	}
	data := JobDetailData{Job: view, VariablesJSON: job.VariablesJSON}
	if job.StartedAt != nil {
		data.WaitDuration = shortDuration(job.StartedAt.Sub(job.CreatedAt))
		if job.CompletedAt != nil {
			data.ProcessingDuration = shortDuration(job.CompletedAt.Sub(*job.StartedAt))
		}
	}
	if job.CompletedAt != nil {
		data.TotalDuration = shortDuration(job.CompletedAt.Sub(job.CreatedAt))
	}
	c.HTML(http.StatusOK, "job-detail", data)
}

func (h *WebUIHandler) JobNewModal(c *gin.Context) {
	c.HTML(http.StatusOK, "new-job", gin.H{})
}

// ---- settings -----------------------------------------------------------

type SettingsServer struct{ Port int }
type SettingsPaths struct{ Path string }

type SettingsConfigView struct {
	Server   SettingsServer
	Database SettingsPaths
	Archive  SettingsPaths
}

type WebhookView struct {
	ID      int64
	Name    string
	URL     string
	Events  []string
	Enabled bool
}

type AIConfigView struct {
	Enabled   bool
	HasAPIKey bool
	Model     string
}

type ArchiveConfigView struct {
	Enabled       bool
	Days          int
	Path          string
	HasPassphrase bool
}

type ArchiveFileView struct {
	Filename  string
	Size      string
	JobsCount int
	CreatedAt time.Time
}

type AuditView struct {
	ID          int64
	Timestamp   time.Time
	Action      string
	EntityType  string
	EntityID    int64
	IPAddress   string
	Details     string
	DetailsJSON string
}

type AuditFilterView struct {
	Action   string
	Entity   string
	FromDate string
	ToDate   string
}

type SettingsPagination struct {
	BaseURL     string
	CurrentPage int
	TotalPages  int
	TotalItems  int
	StartItem   int
	EndItem     int
	PageNumbers []int
}

type SettingsPage struct {
	PageBase
	Config        SettingsConfigView
	Webhooks      []WebhookView
	AIConfig      AIConfigView
	ArchiveConfig ArchiveConfigView
	Archives      []ArchiveFileView
	AuditLogs     []AuditView
	Filter        AuditFilterView
	Pagination    *SettingsPagination
}

const auditPageSize = 25

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (h *WebUIHandler) settingValue(c *gin.Context, key string) (string, bool) {
	s, err := db.Settings.GetSetting(c.Request.Context(), key)
	if err != nil {
		return "", false
	}
	return s.Value, true
}

func (h *WebUIHandler) settingsData(c *gin.Context) SettingsPage {
	ctx := c.Request.Context()
	data := SettingsPage{PageBase: newPage("Settings", "settings")}

	if cfg := h.deps.Config; cfg != nil {
		data.Config = SettingsConfigView{
			Server:   SettingsServer{Port: cfg.Server.Port},
			Database: SettingsPaths{Path: cfg.Database.Path},
			Archive:  SettingsPaths{Path: cfg.Database.ArchivePath},
		}
		data.ArchiveConfig = ArchiveConfigView{Enabled: true, Days: cfg.Database.ArchiveDays, Path: cfg.Database.ArchivePath}
	}
	if v, ok := h.settingValue(c, "archive_days"); ok {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			data.ArchiveConfig.Days = days
		}
	}
	if v, ok := h.settingValue(c, "archive_enabled"); ok {
		data.ArchiveConfig.Enabled = v == "true"
	}
	if a := h.deps.Archiver; a != nil {
		data.ArchiveConfig.HasPassphrase = a.HasPassphrase()
		data.ArchiveConfig.Path = a.GetArchivePath()
		if files, err := a.ListArchives(); err == nil {
			for _, f := range files {
				data.Archives = append(data.Archives, ArchiveFileView{
					Filename: f.Filename, Size: humanSize(f.Size), JobsCount: f.JobCount, CreatedAt: f.CreatedAt,
				})
			}
		}
	}
	if g := h.deps.AI; g != nil {
		data.AIConfig.HasAPIKey = g.IsConfigured()
		data.AIConfig.Model = g.GetModel()
	}
	if v, ok := h.settingValue(c, "ai_enabled"); ok {
		data.AIConfig.Enabled = v == "true"
	}
	if v, ok := h.settingValue(c, "ai_model"); ok && v != "" {
		data.AIConfig.Model = v
	}

	if hooks, err := db.Webhooks.ListWebhooks(ctx); err == nil {
		for _, w := range hooks {
			var events []string
			_ = json.Unmarshal([]byte(w.EventsJSON), &events)
			data.Webhooks = append(data.Webhooks, WebhookView{
				ID: w.ID, Name: w.Name, URL: w.URL, Events: events, Enabled: w.Enabled,
			})
		}
	}

	h.fillAudit(c, &data)
	return data
}

func (h *WebUIHandler) fillAudit(c *gin.Context, data *SettingsPage) {
	ctx := c.Request.Context()
	data.Filter = AuditFilterView{
		Action: c.Query("action"), Entity: c.Query("entity"),
		FromDate: c.Query("from_date"), ToDate: c.Query("to_date"),
	}
	filter := db.AuditFilter{Action: data.Filter.Action, EntityType: data.Filter.Entity}
	if t, err := time.Parse("2006-01-02", data.Filter.FromDate); err == nil {
		filter.FromDate = &t
	}
	if t, err := time.Parse("2006-01-02", data.Filter.ToDate); err == nil {
		end := t.Add(24*time.Hour - time.Second)
		filter.ToDate = &end
	}
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	total, _ := db.Audit.CountAuditLogs(ctx, filter)
	logs, err := db.Audit.ListAuditLogs(ctx, filter, auditPageSize, (page-1)*auditPageSize)
	if err != nil {
		return
	}
	for _, l := range logs {
		details := ""
		if l.DetailsJSON != "" && l.DetailsJSON != "{}" && l.DetailsJSON != "null" {
			details = l.DetailsJSON
		}
		data.AuditLogs = append(data.AuditLogs, AuditView{
			ID: l.ID, Timestamp: l.CreatedAt, Action: l.Action, EntityType: l.EntityType,
			EntityID: l.EntityID, IPAddress: l.IPAddress, Details: details, DetailsJSON: l.DetailsJSON,
		})
	}
	pages := (total + auditPageSize - 1) / auditPageSize
	if pages > 1 {
		p := &SettingsPagination{
			BaseURL: "/settings/audit", CurrentPage: page, TotalPages: pages, TotalItems: total,
			StartItem: (page-1)*auditPageSize + 1, EndItem: (page-1)*auditPageSize + len(logs),
		}
		for i := 1; i <= pages && i <= 10; i++ {
			p.PageNumbers = append(p.PageNumbers, i)
		}
		data.Pagination = p
	}
}

func (h *WebUIHandler) SettingsPage(c *gin.Context) {
	c.HTML(http.StatusOK, "settings", h.settingsData(c))
}

// SettingsAudit answers the audit filter form: just the table for HTMX, the
// whole settings page for a plain navigation (the Clear link).
func (h *WebUIHandler) SettingsAudit(c *gin.Context) {
	data := h.settingsData(c)
	if c.GetHeader("HX-Request") == "true" {
		c.HTML(http.StatusOK, "audit-table", data)
		return
	}
	c.HTML(http.StatusOK, "settings", data)
}
