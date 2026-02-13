package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/orrn/spool/internal/ai"
	"github.com/orrn/spool/internal/core"
	"github.com/orrn/spool/internal/db"
	"github.com/orrn/spool/internal/utils"
)

type AIHandler struct {
	geminiClient *ai.GeminiClient
	db           *sql.DB
	encryptionKey []byte
}

type GenerateTemplateRequest struct {
	Description string  `json:"description" binding:"required"`
	Image       string  `json:"image,omitempty"`
	WidthMM     float64 `json:"width_mm,omitempty"`
	HeightMM    float64 `json:"height_mm,omitempty"`
	DPI         int     `json:"dpi,omitempty"`
}

type GenerateTemplateResponse struct {
	Schema      GenerateTemplateSchema `json:"schema"`
	RawResponse string                 `json:"raw_response,omitempty"`
}

type GenerateTemplateSchema struct {
	Name      string                          `json:"name"`
	WidthMM   float64                         `json:"width_mm"`
	HeightMM  float64                         `json:"height_mm"`
	GapMM     float64                         `json:"gap_mm"`
	DPI       int                             `json:"dpi"`
	Elements  []map[string]interface{}        `json:"elements"`
	Variables map[string]VariableDefResponse `json:"variables"`
}

type VariableDefResponse struct {
	Type     string `json:"type"`
	Required bool   `json:"required"`
	Default  string `json:"default"`
}

type TestConnectionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type APIKeyRequest struct {
	APIKey string `json:"api_key" binding:"required"`
}

type AIConfigResponse struct {
	Configured bool   `json:"configured"`
	Model      string `json:"model,omitempty"`
}

func NewAIHandler(geminiClient *ai.GeminiClient, database *sql.DB, encryptionKey []byte) *AIHandler {
	return &AIHandler{
		geminiClient: geminiClient,
		db:           database,
		encryptionKey: encryptionKey,
	}
}

func (h *AIHandler) GenerateTemplate(c *gin.Context) {
	var req GenerateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !h.geminiClient.IsConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI service not configured. Please set the API key first."})
		return
	}

	if req.WidthMM <= 0 {
		req.WidthMM = 100
	}
	if req.HeightMM <= 0 {
		req.HeightMM = 50
	}
	if req.DPI <= 0 {
		req.DPI = 203
	}

	genReq := &ai.GenerateRequest{
		Description: req.Description,
		Image:       req.Image,
		WidthMM:     req.WidthMM,
		HeightMM:    req.HeightMM,
		DPI:         req.DPI,
	}

	schema, err := h.geminiClient.GenerateLabel(c.Request.Context(), genReq)
	if err != nil {
		if apiErr, ok := err.(*ai.GeminiError); ok {
			switch apiErr.Status {
			case "INVALID_ARGUMENT":
				if apiErr.Code == 400 {
					c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request to AI service: " + apiErr.Message})
					return
				}
			case "PERMISSION_DENIED":
				c.JSON(http.StatusForbidden, gin.H{"error": "API key invalid or quota exceeded"})
				return
			case "RESOURCE_EXHAUSTED":
				c.JSON(http.StatusTooManyRequests, gin.H{"error": "Rate limit exceeded. Please try again later."})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to generate template: %v", err)})
		return
	}

	response := h.convertSchema(schema)

	c.JSON(http.StatusOK, response)
}

func (h *AIHandler) TestConnection(c *gin.Context) {
	if !h.geminiClient.IsConfigured() {
		c.JSON(http.StatusOK, TestConnectionResponse{
			Success: false,
			Message: "API key not configured",
		})
		return
	}

	err := h.geminiClient.TestConnection(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusOK, TestConnectionResponse{
			Success: false,
			Message: err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, TestConnectionResponse{
		Success: true,
		Message: "Connection successful",
	})
}

func (h *AIHandler) SetAPIKey(c *gin.Context) {
	var req APIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if h.encryptionKey == nil || len(h.encryptionKey) != 32 {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "encryption key not configured"})
		return
	}

	encryptedKey, err := utils.Encrypt(req.APIKey, h.encryptionKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt api key"})
		return
	}

	if err := db.Settings.SetSetting(c.Request.Context(), "gemini_api_key", encryptedKey, true); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save api key"})
		return
	}

	h.geminiClient.SetAPIKey(req.APIKey)

	c.JSON(http.StatusOK, gin.H{"message": "API key saved successfully"})
}

func (h *AIHandler) GetConfig(c *gin.Context) {
	configured := h.geminiClient.IsConfigured()
	model := ""
	if configured {
		model = h.geminiClient.GetModel()
	}

	c.JSON(http.StatusOK, AIConfigResponse{
		Configured: configured,
		Model:      model,
	})
}

func (h *AIHandler) DeleteAPIKey(c *gin.Context) {
	if err := db.Settings.DeleteSetting(c.Request.Context(), "gemini_api_key"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete api key"})
		return
	}

	h.geminiClient.SetAPIKey("")

	c.JSON(http.StatusOK, gin.H{"message": "API key deleted"})
}

func (h *AIHandler) LoadAPIKey(ctx context.Context) error {
	setting, err := db.Settings.GetSetting(ctx, "gemini_api_key")
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("failed to get api key: %w", err)
	}

	if h.encryptionKey == nil || len(h.encryptionKey) != 32 {
		return fmt.Errorf("encryption key not configured")
	}

	decryptedKey, err := utils.Decrypt(setting.Value, h.encryptionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt api key: %w", err)
	}

	h.geminiClient.SetAPIKey(decryptedKey)
	return nil
}

func (h *AIHandler) convertSchema(schema *core.LabelSchema) GenerateTemplateResponse {
	elements := make([]map[string]interface{}, len(schema.Elements))
	for i, elem := range schema.Elements {
		elements[i] = map[string]interface{}{
			"type": elem.Type,
			"x":    elem.X,
			"y":    elem.Y,
		}
		if elem.Content != "" {
			elements[i]["content"] = elem.Content
		}
		if elem.Font != "" {
			elements[i]["font"] = elem.Font
		}
		if elem.Rotation != 0 {
			elements[i]["rotation"] = elem.Rotation
		}
		if elem.XScale != 0 {
			elements[i]["x_scale"] = elem.XScale
		}
		if elem.YScale != 0 {
			elements[i]["y_scale"] = elem.YScale
		}
		if elem.Symbology != "" {
			elements[i]["symbology"] = elem.Symbology
		}
		if elem.Height != 0 {
			elements[i]["height"] = elem.Height
		}
		if elem.Narrow != 0 {
			elements[i]["narrow"] = elem.Narrow
		}
		if elem.Wide != 0 {
			elements[i]["wide"] = elem.Wide
		}
		if elem.Level != "" {
			elements[i]["level"] = elem.Level
		}
		if elem.CellWidth != 0 {
			elements[i]["cell_width"] = elem.CellWidth
		}
		if elem.XEnd != 0 {
			elements[i]["x_end"] = elem.XEnd
		}
		if elem.YEnd != 0 {
			elements[i]["y_end"] = elem.YEnd
		}
		if elem.Thickness != 0 {
			elements[i]["thickness"] = elem.Thickness
		}
		if elem.X1 != 0 || elem.Y1 != 0 || elem.X2 != 0 || elem.Y2 != 0 {
			elements[i]["x1"] = elem.X1
			elements[i]["y1"] = elem.Y1
			elements[i]["x2"] = elem.X2
			elements[i]["y2"] = elem.Y2
		}
		if elem.Radius != 0 {
			elements[i]["radius"] = elem.Radius
		}
		if elem.XRadius != 0 || elem.YRadius != 0 {
			elements[i]["x_radius"] = elem.XRadius
			elements[i]["y_radius"] = elem.YRadius
		}
		if elem.Width != 0 {
			elements[i]["width"] = elem.Width
		}
		if elem.ImagePath != "" {
			elements[i]["image_path"] = elem.ImagePath
		}
	}

	variables := make(map[string]VariableDefResponse)
	for name, def := range schema.Variables {
		variables[name] = VariableDefResponse{
			Type:     def.Type,
			Required: def.Required,
			Default:  def.Default,
		}
	}

	return GenerateTemplateResponse{
		Schema: GenerateTemplateSchema{
			Name:      schema.Name,
			WidthMM:   schema.WidthMM,
			HeightMM:  schema.HeightMM,
			GapMM:     schema.GapMM,
			DPI:       schema.DPI,
			Elements:  elements,
			Variables: variables,
		},
	}
}

func RegisterAIRoutes(router *gin.RouterGroup, handler *AIHandler) {
	ai := router.Group("/ai")
	{
		ai.POST("/generate", handler.GenerateTemplate)
		ai.GET("/test", handler.TestConnection)
		ai.GET("/config", handler.GetConfig)
		ai.POST("/api-key", handler.SetAPIKey)
		ai.DELETE("/api-key", handler.DeleteAPIKey)
	}
}
