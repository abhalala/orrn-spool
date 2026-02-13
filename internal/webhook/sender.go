package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"orrn-spool/internal/core"
	"orrn-spool/internal/db"
)

type WebhookEvent string

const (
	EventJobStarted           WebhookEvent = "job_started"
	EventJobCompleted         WebhookEvent = "job_completed"
	EventJobFailed            WebhookEvent = "job_failed"
	EventPrinterStatusChanged WebhookEvent = "printer_status_changed"
	EventQueueStatus          WebhookEvent = "queue_status"
)

type WebhookPayload struct {
	Event     string      `json:"event"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
	Signature string      `json:"signature,omitempty"`
}

type JobEventData struct {
	JobID        int64  `json:"job_id"`
	PrinterID    int64  `json:"printer_id"`
	TemplateID   int64  `json:"template_id"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message,omitempty"`
	Duration     int64  `json:"duration_ms,omitempty"`
	RetryCount   int    `json:"retry_count,omitempty"`
}

type PrinterStatusData struct {
	PrinterID      int64     `json:"printer_id"`
	PrinterName    string    `json:"printer_name"`
	PreviousStatus string    `json:"previous_status"`
	NewStatus      string    `json:"new_status"`
	PrinterState   string    `json:"printer_state"`
	Warning        string    `json:"warning"`
	Error          string    `json:"error"`
	MediaError     string    `json:"media_error"`
	IsOnline       bool      `json:"is_online"`
	Timestamp      time.Time `json:"timestamp"`
}

type QueueStatusData struct {
	Pending    int `json:"pending"`
	Processing int `json:"processing"`
	Paused     int `json:"paused"`
	Failed     int `json:"failed"`
	Total      int `json:"total"`
}

type WebhookConfig struct {
	RetryCount  int
	RetryDelay  time.Duration
	Timeout     time.Duration
	WorkerCount int
	QueueSize   int
}

type webhookTask struct {
	webhookID int64
	event     WebhookEvent
	payload   *WebhookPayload
	attempt   int
}

type WebhookSender struct {
	db         *sql.DB
	httpClient *http.Client
	retryCount int
	retryDelay time.Duration
	queue      chan *webhookTask
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

func NewWebhookSender(database *sql.DB, config WebhookConfig) *WebhookSender {
	if config.RetryCount <= 0 {
		config.RetryCount = 3
	}
	if config.RetryDelay <= 0 {
		config.RetryDelay = 5 * time.Second
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	if config.WorkerCount <= 0 {
		config.WorkerCount = 3
	}
	if config.QueueSize <= 0 {
		config.QueueSize = 100
	}

	return &WebhookSender{
		db: database,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		retryCount: config.RetryCount,
		retryDelay: config.RetryDelay,
		queue:      make(chan *webhookTask, config.QueueSize),
		stopCh:     make(chan struct{}),
	}
}

func (s *WebhookSender) Start() {
	for i := 0; i < s.retryCount; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}
}

func (s *WebhookSender) Stop() {
	close(s.stopCh)
	s.wg.Wait()
}

func (s *WebhookSender) SendJobStarted(jobID, printerID, templateID int64) {
	data := &JobEventData{
		JobID:      jobID,
		PrinterID:  printerID,
		TemplateID: templateID,
		Status:     "started",
	}
	s.enqueue(EventJobStarted, data)
}

func (s *WebhookSender) SendJobCompleted(jobID int64, durationMs int64) {
	data := &JobEventData{
		JobID:    jobID,
		Status:   "completed",
		Duration: durationMs,
	}
	s.enqueue(EventJobCompleted, data)
}

func (s *WebhookSender) SendJobFailed(jobID int64, errMsg string, retryCount int) {
	data := &JobEventData{
		JobID:        jobID,
		Status:       "failed",
		ErrorMessage: errMsg,
		RetryCount:   retryCount,
	}
	s.enqueue(EventJobFailed, data)
}

func (s *WebhookSender) SendPrinterStatusChange(printerID int64, printerName, prevStatus, newStatus string, status *core.PrinterStatus) error {
	data := &PrinterStatusData{
		PrinterID:      printerID,
		PrinterName:    printerName,
		PreviousStatus: prevStatus,
		NewStatus:      newStatus,
		Timestamp:      time.Now(),
	}
	if status != nil {
		data.PrinterState = status.PrinterState
		data.Warning = status.Warning
		data.Error = status.Error
		data.MediaError = status.MediaError
		data.IsOnline = status.IsOnline
	}
	s.enqueue(EventPrinterStatusChanged, data)
	return nil
}

func (s *WebhookSender) SendPrintComplete(printerID int64, jobID int64, success bool, errorMsg string) error {
	if success {
		s.SendJobCompleted(jobID, 0)
	} else {
		s.SendJobFailed(jobID, errorMsg, 0)
	}
	return nil
}

func (s *WebhookSender) SendQueueStatus(stats QueueStatusData) {
	s.enqueue(EventQueueStatus, stats)
}

func (s *WebhookSender) enqueue(event WebhookEvent, data interface{}) {
	webhooks, err := s.getActiveWebhooksForEvent(event)
	if err != nil {
		log.Printf("[webhook] failed to get webhooks for event %s: %v", event, err)
		return
	}

	for _, webhook := range webhooks {
		task := &webhookTask{
			webhookID: webhook.ID,
			event:     event,
			payload: &WebhookPayload{
				Event:     string(event),
				Timestamp: time.Now(),
				Data:      data,
			},
			attempt: 0,
		}

		select {
		case s.queue <- task:
		default:
			log.Printf("[webhook] queue full, dropping webhook %d for event %s", webhook.ID, event)
		}
	}
}

func (s *WebhookSender) getActiveWebhooksForEvent(event WebhookEvent) ([]*db.Webhook, error) {
	query := `SELECT id, name, url, secret, events_json, enabled, created_at FROM webhooks WHERE enabled = 1 AND events_json LIKE ?`
	eventPattern := fmt.Sprintf("%%\"%s\"%%", event)
	
	rows, err := s.db.Query(query, eventPattern)
	if err != nil {
		return nil, fmt.Errorf("query webhooks: %w", err)
	}
	defer rows.Close()

	var webhooks []*db.Webhook
	for rows.Next() {
		w := &db.Webhook{}
		var enabled int
		err := rows.Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &w.EventsJSON, &enabled, &w.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("scan webhook: %w", err)
		}
		w.Enabled = enabled == 1
		webhooks = append(webhooks, w)
	}
	return webhooks, nil
}

func (s *WebhookSender) getWebhookByID(id int64) (*db.Webhook, error) {
	query := `SELECT id, name, url, secret, events_json, enabled, created_at FROM webhooks WHERE id = ?`
	w := &db.Webhook{}
	var enabled int
	err := s.db.QueryRow(query, id).Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &w.EventsJSON, &enabled, &w.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get webhook %d: %w", id, err)
	}
	w.Enabled = enabled == 1
	return w, nil
}

func (s *WebhookSender) worker(id int) {
	defer s.wg.Done()
	
	for {
		select {
		case <-s.stopCh:
			return
		case task := <-s.queue:
			if err := s.sendWithRetry(task); err != nil {
				log.Printf("[webhook worker %d] failed to send webhook %d for event %s after %d attempts: %v", 
					id, task.webhookID, task.event, task.attempt, err)
			}
		}
	}
}

func (s *WebhookSender) sendWithRetry(task *webhookTask) error {
	webhook, err := s.getWebhookByID(task.webhookID)
	if err != nil {
		return fmt.Errorf("get webhook: %w", err)
	}

	var lastErr error
	for task.attempt < s.retryCount {
		task.attempt++
		
		err := s.sendRequest(webhook, task.payload)
		if err == nil {
			return nil
		}
		
		lastErr = err
		
		if isClientError(err) {
			log.Printf("[webhook] client error for webhook %d, not retrying: %v", webhook.ID, err)
			return err
		}

		if task.attempt < s.retryCount {
			backoff := s.retryDelay * time.Duration(1<<(task.attempt-1))
			log.Printf("[webhook] retry %d/%d for webhook %d in %v: %v", 
				task.attempt, s.retryCount, webhook.ID, backoff, err)
			
			select {
			case <-s.stopCh:
				return fmt.Errorf("shutdown requested")
			case <-time.After(backoff):
			}
		}
	}
	
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (s *WebhookSender) sendRequest(webhook *db.Webhook, payload *WebhookPayload) error {
	payloadBytes, err := json.Marshal(payload.Data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	if webhook.Secret != "" {
		payload.Signature = s.signPayload(payloadBytes, webhook.Secret)
	}

	fullPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequest("POST", webhook.URL, bytes.NewReader(fullPayload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", payload.Signature)
	req.Header.Set("X-Webhook-Event", payload.Event)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("http error: %d", resp.StatusCode)
	}

	return nil
}

func (s *WebhookSender) signPayload(payload []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

func isClientError(err error) bool {
	if err == nil {
		return false
	}
	
	errStr := err.Error()
	if strings.Contains(errStr, "http error: 4") {
		return true
	}
	return false
}
