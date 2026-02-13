package handlers

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
)

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

type CreatePrinterRequest struct {
	Name          string  `json:"name" binding:"required"`
	IPAddress     string  `json:"ip_address" binding:"required,ip_addr"`
	Port          int     `json:"port"`
	DPI           int     `json:"dpi"`
	LabelWidthMM  float64 `json:"label_width_mm" binding:"required,gt=0"`
	LabelHeightMM float64 `json:"label_height_mm" binding:"required,gt=0"`
	GapMM         float64 `json:"gap_mm"`
}

type UpdatePrinterRequest struct {
	Name          string  `json:"name"`
	IPAddress     string  `json:"ip_address" binding:"omitempty,ip_addr"`
	Port          int     `json:"port"`
	DPI           int     `json:"dpi"`
	LabelWidthMM  float64 `json:"label_width_mm" binding:"omitempty,gt=0"`
	LabelHeightMM float64 `json:"label_height_mm" binding:"omitempty,gt=0"`
	GapMM         float64 `json:"gap_mm"`
}

type PrinterResponse struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	IPAddress     string     `json:"ip_address"`
	Port          int        `json:"port"`
	DPI           int        `json:"dpi"`
	LabelWidthMM  float64    `json:"label_width_mm"`
	LabelHeightMM float64    `json:"label_height_mm"`
	GapMM         float64    `json:"gap_mm"`
	Status        string     `json:"status"`
	CanPrint      bool       `json:"can_print"`
	LastSeenAt    *time.Time `json:"last_seen_at,omitempty"`
	TotalPrints   int64      `json:"total_prints"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type PrinterStatusResponse struct {
	ID           int64     `json:"id"`
	Status       string    `json:"status"`
	PrinterState string    `json:"printer_state"`
	Warning      string    `json:"warning"`
	Error        string    `json:"error"`
	MediaError   string    `json:"media_error"`
	IsOnline     bool      `json:"is_online"`
	CanPrint     bool      `json:"can_print"`
	LastChecked  time.Time `json:"last_checked"`
}

type TestPrintRequest struct {
	TemplateID int64             `json:"template_id"`
	Variables  map[string]string `json:"variables"`
}

type PrinterCountersResponse struct {
	PrinterID int64          `json:"printer_id"`
	Total     int64          `json:"total"`
	Today     int64          `json:"today"`
	ByDate    []CounterEntry `json:"by_date"`
}

type CounterEntry struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type PrinterHandler struct {
	db             *sql.DB
	printerManager *core.PrinterManager
}

func NewPrinterHandler(database *sql.DB, printerManager *core.PrinterManager) *PrinterHandler {
	return &PrinterHandler{
		db:             database,
		printerManager: printerManager,
	}
}

func (h *PrinterHandler) ListPrinters(c *gin.Context) {
	printers, err := db.Printers.ListPrinters(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve printers",
		})
		return
	}

	responses := make([]PrinterResponse, 0, len(printers))
	for _, p := range printers {
		responses = append(responses, h.printerToResponse(p))
	}

	c.JSON(http.StatusOK, responses)
}

func (h *PrinterHandler) CreatePrinter(c *gin.Context) {
	var req CreatePrinterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: err.Error(),
		})
		return
	}

	var existingName int
	err := h.db.QueryRowContext(c.Request.Context(),
		"SELECT 1 FROM printers WHERE name = ?", req.Name).Scan(&existingName)
	if err == nil {
		c.JSON(http.StatusConflict, ErrorResponse{
			Error:   "duplicate_name",
			Message: "Printer with this name already exists",
		})
		return
	}

	port := req.Port
	if port == 0 {
		port = 9100
	}

	dpi := req.DPI
	if dpi == 0 {
		dpi = 203
	}

	printer := &db.Printer{
		Name:          req.Name,
		IPAddress:     req.IPAddress,
		Port:          port,
		DPI:           dpi,
		LabelWidthMM:  req.LabelWidthMM,
		LabelHeightMM: req.LabelHeightMM,
		GapMM:         req.GapMM,
		Status:        "unknown",
	}

	err = db.Printers.CreatePrinter(c.Request.Context(), printer)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to create printer",
		})
		return
	}

	corePrinter := &core.Printer{
		ID:            printer.ID,
		Name:          printer.Name,
		IPAddress:     printer.IPAddress,
		Port:          printer.Port,
		DPI:           printer.DPI,
		LabelWidthMM:  printer.LabelWidthMM,
		LabelHeightMM: printer.LabelHeightMM,
		GapMM:         printer.GapMM,
		Status:        printer.Status,
		LastSeenAt:    printer.LastSeenAt,
		TotalPrints:   printer.TotalPrints,
	}

	if err := h.printerManager.AddPrinter(corePrinter); err != nil {
		if err == core.ErrPrinterAlreadyExists {
			c.JSON(http.StatusConflict, ErrorResponse{
				Error:   "duplicate_printer",
				Message: "Printer already exists in manager",
			})
			return
		}
	}

	c.JSON(http.StatusCreated, h.printerToResponse(printer))
}

func (h *PrinterHandler) GetPrinter(c *gin.Context) {
	id, err := h.parsePrinterID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid printer ID",
		})
		return
	}

	printer, err := db.Printers.GetPrinterByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve printer",
		})
		return
	}

	c.JSON(http.StatusOK, h.printerToResponse(printer))
}

func (h *PrinterHandler) UpdatePrinter(c *gin.Context) {
	id, err := h.parsePrinterID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid printer ID",
		})
		return
	}

	var req UpdatePrinterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: err.Error(),
		})
		return
	}

	printer, err := db.Printers.GetPrinterByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve printer",
		})
		return
	}

	if req.Name != "" {
		var existingName int
		err := h.db.QueryRowContext(c.Request.Context(),
			"SELECT 1 FROM printers WHERE name = ? AND id != ?", req.Name, id).Scan(&existingName)
		if err == nil {
			c.JSON(http.StatusConflict, ErrorResponse{
				Error:   "duplicate_name",
				Message: "Printer with this name already exists",
			})
			return
		}
		printer.Name = req.Name
	}
	if req.IPAddress != "" {
		printer.IPAddress = req.IPAddress
	}
	if req.Port != 0 {
		printer.Port = req.Port
	}
	if req.DPI != 0 {
		printer.DPI = req.DPI
	}
	if req.LabelWidthMM != 0 {
		printer.LabelWidthMM = req.LabelWidthMM
	}
	if req.LabelHeightMM != 0 {
		printer.LabelHeightMM = req.LabelHeightMM
	}
	if req.GapMM != 0 {
		printer.GapMM = req.GapMM
	}

	err = db.Printers.UpdatePrinter(c.Request.Context(), printer)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to update printer",
		})
		return
	}

	corePrinter := &core.Printer{
		ID:            printer.ID,
		Name:          printer.Name,
		IPAddress:     printer.IPAddress,
		Port:          printer.Port,
		DPI:           printer.DPI,
		LabelWidthMM:  printer.LabelWidthMM,
		LabelHeightMM: printer.LabelHeightMM,
		GapMM:         printer.GapMM,
		Status:        printer.Status,
		LastSeenAt:    printer.LastSeenAt,
		TotalPrints:   printer.TotalPrints,
	}

	if err := h.printerManager.UpdatePrinter(corePrinter); err != nil {
		if err == core.ErrPrinterNotFound {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found in manager",
			})
			return
		}
	}

	c.JSON(http.StatusOK, h.printerToResponse(printer))
}

func (h *PrinterHandler) DeletePrinter(c *gin.Context) {
	id, err := h.parsePrinterID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid printer ID",
		})
		return
	}

	var pendingCount int
	err = h.db.QueryRowContext(c.Request.Context(),
		"SELECT COUNT(*) FROM print_jobs WHERE printer_id = ? AND status IN ('pending', 'processing')", id).Scan(&pendingCount)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to check for pending jobs",
		})
		return
	}

	if pendingCount > 0 {
		c.JSON(http.StatusConflict, ErrorResponse{
			Error:   "has_pending_jobs",
			Message: fmt.Sprintf("Cannot delete printer with %d pending jobs", pendingCount),
		})
		return
	}

	_, err = db.Printers.GetPrinterByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve printer",
		})
		return
	}

	err = db.Printers.DeletePrinter(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to delete printer",
		})
		return
	}

	if err := h.printerManager.RemovePrinter(id); err != nil {
		if err == core.ErrPrinterNotFound {
		}
	}

	c.Status(http.StatusNoContent)
}

func (h *PrinterHandler) GetPrinterStatus(c *gin.Context) {
	id, err := h.parsePrinterID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid printer ID",
		})
		return
	}

	status, err := h.printerManager.CheckStatus(id)
	if err != nil {
		if err == core.ErrPrinterNotFound {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
			return
		}

		printer, dbErr := db.Printers.GetPrinterByID(c.Request.Context(), id)
		if dbErr == nil {
			c.JSON(http.StatusOK, PrinterStatusResponse{
				ID:           id,
				Status:       printer.Status,
				PrinterState: "unknown",
				Warning:      "none",
				Error:        "connection_failed",
				MediaError:   "none",
				IsOnline:     false,
				CanPrint:     false,
				LastChecked:  time.Now(),
			})
			return
		}

		c.JSON(http.StatusOK, PrinterStatusResponse{
			ID:           id,
			Status:       "offline",
			PrinterState: "unknown",
			Warning:      "none",
			Error:        "none",
			MediaError:   "none",
			IsOnline:     false,
			CanPrint:     false,
			LastChecked:  time.Now(),
		})
		return
	}

	printer, _ := db.Printers.GetPrinterByID(c.Request.Context(), id)
	statusStr := "offline"
	if printer != nil {
		statusStr = printer.Status
	}

	c.JSON(http.StatusOK, PrinterStatusResponse{
		ID:           id,
		Status:       statusStr,
		PrinterState: status.PrinterState,
		Warning:      status.Warning,
		Error:        status.Error,
		MediaError:   status.MediaError,
		IsOnline:     status.IsOnline,
		CanPrint:     status.CanPrint,
		LastChecked:  status.LastChecked,
	})
}

func (h *PrinterHandler) TestPrinter(c *gin.Context) {
	id, err := h.parsePrinterID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid printer ID",
		})
		return
	}

	var req TestPrintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
	}

	printer, err := db.Printers.GetPrinterByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve printer",
		})
		return
	}

	var tsplContent string

	if req.TemplateID != 0 {
		template, err := db.Templates.GetTemplateByID(c.Request.Context(), req.TemplateID)
		if err != nil {
			if err == sql.ErrNoRows {
				c.JSON(http.StatusBadRequest, ErrorResponse{
					Error:   "template_not_found",
					Message: "Specified template not found",
				})
				return
			}
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:   "database_error",
				Message: "Failed to retrieve template",
			})
			return
		}

		generator := core.NewTSPL2Generator()
		schema, err := generator.ParseSchema(template.SchemaJSON)
		if err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:   "template_error",
				Message: "Failed to parse template schema",
			})
			return
		}

		if req.Variables == nil {
			req.Variables = make(map[string]string)
		}

		tsplContent, err = generator.Generate(schema, req.Variables)
		if err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "generation_error",
				Message: err.Error(),
			})
			return
		}
	} else {
		tsplContent = h.generateTestLabel(printer)
	}

	err = h.printerManager.Print(id, tsplContent, 1)
	if err != nil {
		switch err {
		case core.ErrPrinterNotFound:
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
		case core.ErrPrinterOffline:
			c.JSON(http.StatusServiceUnavailable, ErrorResponse{
				Error:   "printer_offline",
				Message: "Printer is offline",
			})
		case core.ErrPrinterCannotPrint:
			c.JSON(http.StatusServiceUnavailable, ErrorResponse{
				Error:   "cannot_print",
				Message: "Printer cannot print in current state",
			})
		default:
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:   "print_error",
				Message: err.Error(),
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Test print sent successfully",
	})
}

func (h *PrinterHandler) PausePrinter(c *gin.Context) {
	id, err := h.parsePrinterID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid printer ID",
		})
		return
	}

	err = h.printerManager.PausePrinter(id)
	if err != nil {
		if err == core.ErrPrinterNotFound {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "pause_error",
			Message: "Failed to pause printer",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Printer paused successfully",
	})
}

func (h *PrinterHandler) ResumePrinter(c *gin.Context) {
	id, err := h.parsePrinterID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid printer ID",
		})
		return
	}

	err = h.printerManager.ResumePrinter(id)
	if err != nil {
		if err == core.ErrPrinterNotFound {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "resume_error",
			Message: "Failed to resume printer",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Printer resumed successfully",
	})
}

func (h *PrinterHandler) GetPrinterCounters(c *gin.Context) {
	id, err := h.parsePrinterID(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid printer ID",
		})
		return
	}

	_, err = db.Printers.GetPrinterByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Printer not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve printer",
		})
		return
	}

	now := time.Now()
	thirtyDaysAgo := now.AddDate(0, 0, -30)

	counters, err := db.Counters.GetCounters(c.Request.Context(), id, thirtyDaysAgo, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve counters",
		})
		return
	}

	var total int64
	var today int64
	todayStr := now.Format("2006-01-02")

	byDate := make([]CounterEntry, 0, len(counters))
	for _, c := range counters {
		total += c.Count
		dateStr := c.Date.Format("2006-01-02")
		if dateStr == todayStr {
			today = c.Count
		}
		byDate = append(byDate, CounterEntry{
			Date:  dateStr,
			Count: c.Count,
		})
	}

	c.JSON(http.StatusOK, PrinterCountersResponse{
		PrinterID: id,
		Total:     total,
		Today:     today,
		ByDate:    byDate,
	})
}

func (h *PrinterHandler) parsePrinterID(c *gin.Context) (int64, error) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, fmt.Errorf("invalid printer ID")
	}
	return id, nil
}

func (h *PrinterHandler) printerToResponse(p *db.Printer) PrinterResponse {
	canPrint := p.Status == "online" || p.Status == "idle" || p.Status == "standby"
	return PrinterResponse{
		ID:            p.ID,
		Name:          p.Name,
		IPAddress:     p.IPAddress,
		Port:          p.Port,
		DPI:           p.DPI,
		LabelWidthMM:  p.LabelWidthMM,
		LabelHeightMM: p.LabelHeightMM,
		GapMM:         p.GapMM,
		Status:        p.Status,
		CanPrint:      canPrint,
		LastSeenAt:    p.LastSeenAt,
		TotalPrints:   p.TotalPrints,
		CreatedAt:     p.CreatedAt,
		UpdatedAt:     p.UpdatedAt,
	}
}

func (h *PrinterHandler) generateTestLabel(p *db.Printer) string {
	dpi := p.DPI
	if dpi == 0 {
		dpi = 203
	}

	dotsPerMM := float64(dpi) / 25.4
	width := int(p.LabelWidthMM * dotsPerMM)
	height := int(p.LabelHeightMM * dotsPerMM)
	centerX := width / 2
	centerY := height / 2

	return fmt.Sprintf(`SIZE %d dot,%d dot
GAP %.0f mm,0 mm
DIRECTION 0
CLS
TEXT %d,%d,"3",0,2,2,"TEST LABEL"
TEXT %d,%d,"3",0,1,1,"Printer: %s"
BARCODE %d,%d,"128",60,0,2,2,2,"%s"
PRINT 1
`, width, height, p.GapMM, centerX-80, centerY-40, centerX-100, centerY+20, p.Name, centerX-100, centerY+60, p.IPAddress)
}
