package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
)

type CreateTemplateRequest struct {
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description"`
	SchemaJSON  string  `json:"schema_json" binding:"required"`
	WidthMM     float64 `json:"width_mm" binding:"required,gt=0"`
	HeightMM    float64 `json:"height_mm" binding:"required,gt=0"`
}

type LabelSchemaJSON struct {
	Name      string                     `json:"name"`
	WidthMM   float64                    `json:"width_mm" binding:"required,gt=0"`
	HeightMM  float64                    `json:"height_mm" binding:"required,gt=0"`
	GapMM     float64                    `json:"gap_mm"`
	DPI       int                        `json:"dpi"`
	Elements  []map[string]interface{}   `json:"elements" binding:"required"`
	Variables map[string]VariableDefJSON `json:"variables"`
}

type VariableDefJSON struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  string `json:"default"`
}

type UpdateTemplateRequest struct {
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	SchemaJSON  *string  `json:"schema_json"`
	WidthMM     *float64 `json:"width_mm" binding:"omitempty,gt=0"`
	HeightMM    *float64 `json:"height_mm" binding:"omitempty,gt=0"`
}

type TemplateResponse struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SchemaJSON  string    `json:"schema_json"`
	WidthMM     float64   `json:"width_mm"`
	HeightMM    float64   `json:"height_mm"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PreviewRequest struct {
	Variables map[string]string `json:"variables"`
}

type PreviewResponse struct {
	TSPLContent string            `json:"tspl_content"`
	Variables   map[string]string `json:"variables_used"`
}

type ValidateResponse struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type QuickPrintRequest struct {
	PrinterID int64             `json:"printer_id" binding:"required"`
	Variables map[string]string `json:"variables" binding:"required"`
	Copies    int               `json:"copies"`
	// JobKey makes the print exactly-once; see docs/print-exactly-once.md.
	JobKey string `json:"job_key"`
}

type QuickPrintResponse struct {
	JobID int64 `json:"job_id"`
}

type TemplateHandler struct {
	db            *sql.DB
	tsplGenerator *core.TSPL2Generator
	queue         *core.Queue
}

func NewTemplateHandler(database *sql.DB, generator *core.TSPL2Generator, queue *core.Queue) *TemplateHandler {
	return &TemplateHandler{
		db:            database,
		tsplGenerator: generator,
		queue:         queue,
	}
}

func (h *TemplateHandler) ListTemplates(c *gin.Context) {
	templates, err := db.Templates.ListTemplates(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list templates"})
		return
	}

	response := make([]TemplateResponse, 0, len(templates))
	for _, t := range templates {
		response = append(response, h.templateToResponse(t))
	}

	c.JSON(http.StatusOK, response)
}

func (h *TemplateHandler) CreateTemplate(c *gin.Context) {
	var req CreateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.validateTemplateSchema(req.SchemaJSON, req.WidthMM, req.HeightMM); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	_, err := db.Templates.GetTemplateByName(c.Request.Context(), req.Name)
	if err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "template with this name already exists"})
		return
	}
	if err != sql.ErrNoRows {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check template name"})
		return
	}

	template := &db.LabelTemplate{
		Name:        req.Name,
		Description: req.Description,
		SchemaJSON:  req.SchemaJSON,
		WidthMM:     req.WidthMM,
		HeightMM:    req.HeightMM,
	}

	if err := db.Templates.CreateTemplate(c.Request.Context(), template); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create template"})
		return
	}

	created, err := db.Templates.GetTemplateByID(c.Request.Context(), template.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch created template"})
		return
	}

	c.JSON(http.StatusCreated, h.templateToResponse(created))
}

func (h *TemplateHandler) GetTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template id"})
		return
	}

	template, err := db.Templates.GetTemplateByID(c.Request.Context(), id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	c.JSON(http.StatusOK, h.templateToResponse(template))
}

func (h *TemplateHandler) UpdateTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template id"})
		return
	}

	template, err := db.Templates.GetTemplateByID(c.Request.Context(), id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	var req UpdateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != "" && req.Name != template.Name {
		existing, err := db.Templates.GetTemplateByName(c.Request.Context(), req.Name)
		if err == nil && existing.ID != id {
			c.JSON(http.StatusConflict, gin.H{"error": "template with this name already exists"})
			return
		}
		if err != nil && err != sql.ErrNoRows {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check template name"})
			return
		}
		template.Name = req.Name
	}

	if req.Description != nil {
		template.Description = *req.Description
	}

	if req.SchemaJSON != nil || req.WidthMM != nil || req.HeightMM != nil {
		schemaJSON, widthMM, heightMM := template.SchemaJSON, template.WidthMM, template.HeightMM
		if req.SchemaJSON != nil {
			schemaJSON = *req.SchemaJSON
		}
		if req.WidthMM != nil {
			widthMM = *req.WidthMM
		}
		if req.HeightMM != nil {
			heightMM = *req.HeightMM
		}
		if err := h.validateTemplateSchema(schemaJSON, widthMM, heightMM); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		template.SchemaJSON = schemaJSON
		template.WidthMM = widthMM
		template.HeightMM = heightMM
	}

	if err := db.Templates.UpdateTemplate(c.Request.Context(), template); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update template"})
		return
	}

	updated, err := db.Templates.GetTemplateByID(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch updated template"})
		return
	}

	c.JSON(http.StatusOK, h.templateToResponse(updated))
}

func (h *TemplateHandler) DeleteTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template id"})
		return
	}

	_, err = db.Templates.GetTemplateByID(c.Request.Context(), id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	var pendingCount int
	err = h.db.QueryRowContext(c.Request.Context(),
		"SELECT COUNT(*) FROM print_jobs WHERE template_id = ? AND status IN ('pending', 'processing')", id).Scan(&pendingCount)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check pending jobs"})
		return
	}
	if pendingCount > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot delete template with pending jobs"})
		return
	}

	if err := db.Templates.DeleteTemplate(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete template"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "template deleted"})
}

func (h *TemplateHandler) PreviewTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template id"})
		return
	}

	template, err := db.Templates.GetTemplateByID(c.Request.Context(), id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	var req PreviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Variables = make(map[string]string)
	}

	schema, err := h.tsplGenerator.ParseSchema(template.SchemaJSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template schema"})
		return
	}

	variables := h.tsplGenerator.MergeVariablesWithDefaults(schema, req.Variables)

	tsplContent, err := h.tsplGenerator.Generate(schema, variables)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to generate preview: %v", err)})
		return
	}

	c.JSON(http.StatusOK, PreviewResponse{
		TSPLContent: tsplContent,
		Variables:   variables,
	})
}

func (h *TemplateHandler) ValidateTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template id"})
		return
	}

	template, err := db.Templates.GetTemplateByID(c.Request.Context(), id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	var schema LabelSchemaJSON
	if err := json.Unmarshal([]byte(template.SchemaJSON), &schema); err != nil {
		c.JSON(http.StatusOK, ValidateResponse{
			Valid:  false,
			Errors: []string{"invalid schema JSON format"},
		})
		return
	}

	errors := validateSchema(&schema)
	warnings := validateSchemaWarnings(&schema)

	c.JSON(http.StatusOK, ValidateResponse{
		Valid:    len(errors) == 0,
		Errors:   errors,
		Warnings: warnings,
	})
}

func (h *TemplateHandler) PrintTemplate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template id"})
		return
	}

	template, err := db.Templates.GetTemplateByID(c.Request.Context(), id)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get template"})
		return
	}

	var req QuickPrintRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	jobKey, ok := jobKeyFromRequest(c, req.JobKey)
	if !ok {
		return
	}
	if jobKey != "" {
		copies := req.Copies
		if copies < 1 {
			copies = 1
		}
		keyVars, _ := json.Marshal(req.Variables)
		if replayDuplicateJob(c, jobKey, req.PrinterID, id, string(keyVars), copies) {
			return
		}
	}

	_, err = db.Printers.GetPrinterByID(c.Request.Context(), req.PrinterID)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": "printer not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get printer"})
		return
	}

	schema, err := h.tsplGenerator.ParseSchema(template.SchemaJSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid template schema"})
		return
	}

	if err := h.tsplGenerator.ValidateVariables(schema, req.Variables); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tsplContent, err := h.tsplGenerator.Generate(schema, req.Variables)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("failed to generate TSPL: %v", err)})
		return
	}

	variablesJSON, _ := json.Marshal(req.Variables)
	copies := req.Copies
	if copies < 1 {
		copies = 1
	}

	job := &core.Job{
		PrinterID:     req.PrinterID,
		TemplateID:    id,
		VariablesJSON: string(variablesJSON),
		TSPLContent:   tsplContent,
		Copies:        copies,
		Status:        core.JobStatusPending,
		JobKey:        jobKey,
	}

	jobID, created, err := h.queue.EnqueueOnce(job)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue job"})
		return
	}
	if !created {
		replayExistingJob(c, jobID, jobKey)
		return
	}

	c.JSON(http.StatusAccepted, QuickPrintResponse{JobID: jobID})
}

func (h *TemplateHandler) templateToResponse(t *db.LabelTemplate) TemplateResponse {
	return TemplateResponse{
		ID:          t.ID,
		Name:        t.Name,
		Description: t.Description,
		SchemaJSON:  t.SchemaJSON,
		WidthMM:     t.WidthMM,
		HeightMM:    t.HeightMM,
		CreatedAt:   t.CreatedAt,
		UpdatedAt:   t.UpdatedAt,
	}
}

func (h *TemplateHandler) validateTemplateSchema(schemaJSON string, widthMM, heightMM float64) error {
	var schema LabelSchemaJSON
	if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
		return fmt.Errorf("invalid schema_json: %w", err)
	}
	if errors := validateSchema(&schema); len(errors) > 0 {
		return fmt.Errorf("invalid schema: %s", strings.Join(errors, "; "))
	}
	if widthMM <= 0 || heightMM <= 0 {
		return fmt.Errorf("width_mm and height_mm must be greater than 0")
	}
	if schema.WidthMM != widthMM || schema.HeightMM != heightMM {
		return fmt.Errorf("schema dimensions must match width_mm and height_mm")
	}
	if _, err := h.tsplGenerator.ParseSchema(schemaJSON); err != nil {
		return fmt.Errorf("invalid schema_json: %w", err)
	}
	return nil
}

func validateSchema(schema *LabelSchemaJSON) []string {
	var errors []string

	if schema.WidthMM <= 0 {
		errors = append(errors, "width_mm must be greater than 0")
	}
	if schema.HeightMM <= 0 {
		errors = append(errors, "height_mm must be greater than 0")
	}
	if len(schema.Elements) == 0 {
		errors = append(errors, "schema must have at least one element")
	}

	for i, elem := range schema.Elements {
		elemErrors := validateElement(elem, i)
		errors = append(errors, elemErrors...)
	}

	for varName, varDef := range schema.Variables {
		if varDef.Type == "" {
			errors = append(errors, fmt.Sprintf("variable '%s' missing type", varName))
		}
		if varDef.Required && varDef.Default != "" {
			errors = append(errors, fmt.Sprintf("variable '%s' is required but has a default value", varName))
		}
	}

	return errors
}

func validateSchemaWarnings(schema *LabelSchemaJSON) []string {
	var warnings []string

	if schema.DPI == 0 {
		warnings = append(warnings, "DPI not specified, will default to 203")
	}
	if schema.GapMM == 0 {
		warnings = append(warnings, "gap_mm not specified, may cause alignment issues")
	}

	for varName, varDef := range schema.Variables {
		if varDef.Required && varDef.Default == "" {
			warnings = append(warnings, fmt.Sprintf("variable '%s' is required with no default, preview may fail", varName))
		}
	}

	return warnings
}

func validateElement(elem map[string]interface{}, index int) []string {
	var errors []string
	prefix := fmt.Sprintf("element[%d]", index)

	elemType, ok := elem["type"].(string)
	if !ok {
		return []string{fmt.Sprintf("%s: missing or invalid 'type' field", prefix)}
	}

	switch elemType {
	case "text":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: text element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: text element missing 'y'", prefix))
		}
		if _, ok := elem["content"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: text element missing 'content'", prefix))
		}

	case "barcode":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: barcode element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: barcode element missing 'y'", prefix))
		}
		if _, ok := elem["content"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: barcode element missing 'content'", prefix))
		}

	case "qrcode":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: qrcode element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: qrcode element missing 'y'", prefix))
		}
		if _, ok := elem["content"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: qrcode element missing 'content'", prefix))
		}

	case "pdf417":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: pdf417 element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: pdf417 element missing 'y'", prefix))
		}
		if _, ok := elem["content"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: pdf417 element missing 'content'", prefix))
		}

	case "datamatrix":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: datamatrix element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: datamatrix element missing 'y'", prefix))
		}
		if _, ok := elem["content"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: datamatrix element missing 'content'", prefix))
		}

	case "box":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: box element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: box element missing 'y'", prefix))
		}
		if _, ok := elem["x_end"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: box element missing 'x_end'", prefix))
		}
		if _, ok := elem["y_end"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: box element missing 'y_end'", prefix))
		}

	case "line":
		if _, ok := elem["x1"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: line element missing 'x1'", prefix))
		}
		if _, ok := elem["y1"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: line element missing 'y1'", prefix))
		}
		if _, ok := elem["x2"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: line element missing 'x2'", prefix))
		}
		if _, ok := elem["y2"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: line element missing 'y2'", prefix))
		}

	case "circle":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: circle element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: circle element missing 'y'", prefix))
		}
		if _, ok := elem["radius"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: circle element missing 'radius'", prefix))
		}

	case "ellipse":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: ellipse element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: ellipse element missing 'y'", prefix))
		}
		if _, ok := elem["x_radius"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: ellipse element missing 'x_radius'", prefix))
		}
		if _, ok := elem["y_radius"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: ellipse element missing 'y_radius'", prefix))
		}

	case "block":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: block element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: block element missing 'y'", prefix))
		}
		if _, ok := elem["width"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: block element missing 'width'", prefix))
		}
		if _, ok := elem["height"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: block element missing 'height'", prefix))
		}
		if _, ok := elem["content"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: block element missing 'content'", prefix))
		}

	case "image":
		if _, ok := elem["x"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: image element missing 'x'", prefix))
		}
		if _, ok := elem["y"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: image element missing 'y'", prefix))
		}
		if _, ok := elem["image_path"]; !ok {
			errors = append(errors, fmt.Sprintf("%s: image element missing 'image_path'", prefix))
		}

	default:
		errors = append(errors, fmt.Sprintf("%s: unknown element type '%s'", prefix, elemType))
	}

	return errors
}

func RegisterTemplateRoutes(router *gin.RouterGroup, handler *TemplateHandler) {
	templates := router.Group("/templates")
	{
		templates.GET("", handler.ListTemplates)
		templates.POST("", handler.CreateTemplate)
		templates.GET("/:id", handler.GetTemplate)
		templates.PUT("/:id", handler.UpdateTemplate)
		templates.DELETE("/:id", handler.DeleteTemplate)
		templates.POST("/:id/preview", handler.PreviewTemplate)
		templates.POST("/:id/validate", handler.ValidateTemplate)
		templates.POST("/:id/print", handler.PrintTemplate)
	}
}
