package db

import (
	"database/sql"
	"time"
)

type Printer struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	IPAddress     string     `json:"ip_address"`
	Port          int        `json:"port"`
	DPI           int        `json:"dpi"`
	LabelWidthMM  float64    `json:"label_width_mm"`
	LabelHeightMM float64    `json:"label_height_mm"`
	GapMM         float64    `json:"gap_mm"`
	Status        string     `json:"status"`
	LastSeenAt    *time.Time `json:"last_seen_at"`
	TotalPrints   int64      `json:"total_prints"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type LabelTemplate struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	SchemaJSON  string    `json:"schema_json"`
	WidthMM     float64   `json:"width_mm"`
	HeightMM    float64   `json:"height_mm"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PrintJob struct {
	ID            int64      `json:"id"`
	PrinterID     int64      `json:"printer_id"`
	TemplateID    int64      `json:"template_id"`
	VariablesJSON string     `json:"variables_json"`
	TSPLContent   string     `json:"tspl_content"`
	Status        string     `json:"status"`
	Priority      int        `json:"priority"`
	RetryCount    int        `json:"retry_count"`
	ErrorMessage  string     `json:"error_message"`
	Copies        int        `json:"copies"`
	SubmittedBy   string     `json:"submitted_by"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at"`
}

type PrintCounter struct {
	ID        int64     `json:"id"`
	PrinterID int64     `json:"printer_id"`
	Date      time.Time `json:"date"`
	Count     int64     `json:"count"`
}

type Webhook struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Secret     string    `json:"secret,omitempty"`
	EventsJSON string    `json:"events_json"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

type Setting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Encrypted bool      `json:"encrypted"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AuditLog struct {
	ID          int64     `json:"id"`
	Action      string    `json:"action"`
	EntityType  string    `json:"entity_type"`
	EntityID    int64     `json:"entity_id"`
	DetailsJSON string    `json:"details_json"`
	IPAddress   string    `json:"ip_address"`
	CreatedAt   time.Time `json:"created_at"`
}

type ArchiveJob struct {
	ID            int64     `json:"id"`
	OriginalJobID int64     `json:"original_job_id"`
	ArchiveFile   string    `json:"archive_file"`
	ArchivedAt    time.Time `json:"archived_at"`
}

type JobFilter struct {
	PrinterID int64
	Status    string
	FromDate  *time.Time
	ToDate    *time.Time
	OrderBy   string
	OrderDir  string
	Limit     int
	Offset    int
}

type AuditFilter struct {
	Action     string
	EntityType string
	EntityID   int64
}
