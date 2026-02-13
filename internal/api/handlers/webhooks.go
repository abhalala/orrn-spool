package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"orrn-spool/internal/db"
	"orrn-spool/internal/webhook"
)

type WebhookHandler struct {
	db            *sql.DB
	webhookSender *webhook.WebhookSender
	httpClient    *http.Client
}

type CreateWebhookRequest struct {
	Name   string   `json:"name" binding:"required"`
	URL    string   `json:"url" binding:"required,url"`
	Secret string   `json:"secret"`
	Events []string `json:"events" binding:"required"`
}

type UpdateWebhookRequest struct {
	Name    string   `json:"name"`
	URL     string   `json:"url" binding:"omitempty,url"`
	Secret  string   `json:"secret"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled"`
}

type WebhookResponse struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type TestWebhookResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func NewWebhookHandler(database *sql.DB, sender *webhook.WebhookSender) *WebhookHandler {
	return &WebhookHandler{
		db:            database,
		webhookSender: sender,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (h *WebhookHandler) ListWebhooks(c *gin.Context) {
	webhooks, err := db.Webhooks.ListWebhooks(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve webhooks",
		})
		return
	}

	responses := make([]WebhookResponse, 0, len(webhooks))
	for _, w := range webhooks {
		responses = append(responses, h.webhookToResponse(w))
	}

	c.JSON(http.StatusOK, responses)
}

func (h *WebhookHandler) CreateWebhook(c *gin.Context) {
	var req CreateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: err.Error(),
		})
		return
	}

	if len(req.Events) == 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: "At least one event must be specified",
		})
		return
	}

	for _, event := range req.Events {
		if !isValidEvent(event) {
			c.JSON(http.StatusBadRequest, ErrorResponse{
				Error:   "invalid_event",
				Message: fmt.Sprintf("Invalid event type: %s", event),
			})
			return
		}
	}

	eventsJSON, err := json.Marshal(req.Events)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "json_error",
			Message: "Failed to serialize events",
		})
		return
	}

	w := &db.Webhook{
		Name:       req.Name,
		URL:        req.URL,
		Secret:     req.Secret,
		EventsJSON: string(eventsJSON),
		Enabled:    true,
	}

	if err := db.Webhooks.CreateWebhook(c.Request.Context(), w); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to create webhook",
		})
		return
	}

	c.JSON(http.StatusCreated, h.webhookToResponse(w))
}

func (h *WebhookHandler) GetWebhook(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid webhook ID",
		})
		return
	}

	w, err := db.Webhooks.GetWebhookByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Webhook not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve webhook",
		})
		return
	}

	c.JSON(http.StatusOK, h.webhookToResponse(w))
}

func (h *WebhookHandler) UpdateWebhook(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid webhook ID",
		})
		return
	}

	w, err := db.Webhooks.GetWebhookByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Webhook not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve webhook",
		})
		return
	}

	var req UpdateWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "validation_error",
			Message: err.Error(),
		})
		return
	}

	if req.Name != "" {
		w.Name = req.Name
	}
	if req.URL != "" {
		w.URL = req.URL
	}
	if req.Secret != "" {
		w.Secret = req.Secret
	}
	if len(req.Events) > 0 {
		for _, event := range req.Events {
			if !isValidEvent(event) {
				c.JSON(http.StatusBadRequest, ErrorResponse{
					Error:   "invalid_event",
					Message: fmt.Sprintf("Invalid event type: %s", event),
				})
				return
			}
		}
		eventsJSON, err := json.Marshal(req.Events)
		if err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{
				Error:   "json_error",
				Message: "Failed to serialize events",
			})
			return
		}
		w.EventsJSON = string(eventsJSON)
	}
	if req.Enabled != nil {
		w.Enabled = *req.Enabled
	}

	if err := db.Webhooks.UpdateWebhook(c.Request.Context(), w); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to update webhook",
		})
		return
	}

	c.JSON(http.StatusOK, h.webhookToResponse(w))
}

func (h *WebhookHandler) DeleteWebhook(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid webhook ID",
		})
		return
	}

	_, err = db.Webhooks.GetWebhookByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Webhook not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve webhook",
		})
		return
	}

	if err := db.Webhooks.DeleteWebhook(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to delete webhook",
		})
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *WebhookHandler) TestWebhook(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_id",
			Message: "Invalid webhook ID",
		})
		return
	}

	w, err := db.Webhooks.GetWebhookByID(c.Request.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, ErrorResponse{
				Error:   "not_found",
				Message: "Webhook not found",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "database_error",
			Message: "Failed to retrieve webhook",
		})
		return
	}

	testPayload := map[string]interface{}{
		"test":      true,
		"message":   "Test webhook from TSC Spool",
		"timestamp": time.Now(),
		"webhook_id": id,
	}

	payloadBytes, err := json.Marshal(testPayload)
	if err != nil {
		c.JSON(http.StatusInternalServerError, TestWebhookResponse{
			Success: false,
			Message: "Failed to marshal test payload",
		})
		return
	}

	req, err := http.NewRequest("POST", w.URL, bytes.NewReader(payloadBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, TestWebhookResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to create request: %v", err),
		})
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Event", "test")
	req.Header.Set("X-Webhook-Test", "true")

	if w.Secret != "" {
		signature := computeSignature(payloadBytes, w.Secret)
		req.Header.Set("X-Webhook-Signature", signature)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		c.JSON(http.StatusOK, TestWebhookResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to send webhook: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		c.JSON(http.StatusOK, TestWebhookResponse{
			Success: false,
			Message: fmt.Sprintf("Webhook returned status %d", resp.StatusCode),
		})
		return
	}

	c.JSON(http.StatusOK, TestWebhookResponse{
		Success: true,
		Message: fmt.Sprintf("Webhook test successful (status %d)", resp.StatusCode),
	})
}

func (h *WebhookHandler) webhookToResponse(w *db.Webhook) WebhookResponse {
	var events []string
	if w.EventsJSON != "" {
		json.Unmarshal([]byte(w.EventsJSON), &events)
	}
	if events == nil {
		events = []string{}
	}

	return WebhookResponse{
		ID:        w.ID,
		Name:      w.Name,
		URL:       w.URL,
		Events:    events,
		Enabled:   w.Enabled,
		CreatedAt: w.CreatedAt,
	}
}

func isValidEvent(event string) bool {
	validEvents := map[string]bool{
		string(webhook.EventJobStarted):           true,
		string(webhook.EventJobCompleted):         true,
		string(webhook.EventJobFailed):            true,
		string(webhook.EventPrinterStatusChanged): true,
		string(webhook.EventQueueStatus):          true,
	}
	return validEvents[event]
}

func computeSignature(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

func RegisterWebhookRoutes(r *gin.RouterGroup, h *WebhookHandler) {
	r.GET("/webhooks", h.ListWebhooks)
	r.POST("/webhooks", h.CreateWebhook)
	r.GET("/webhooks/:id", h.GetWebhook)
	r.PUT("/webhooks/:id", h.UpdateWebhook)
	r.DELETE("/webhooks/:id", h.DeleteWebhook)
	r.POST("/webhooks/:id/test", h.TestWebhook)
}
