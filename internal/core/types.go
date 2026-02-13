package core

import (
	"time"
)

type WebhookSender interface {
	SendPrinterStatusChange(printerID int64, printerName, oldStatus, newStatus string, details *PrinterStatus) error
	SendPrintComplete(printerID int64, jobID int64, success bool, errorMsg string) error
}

type PrinterStatus struct {
	RawStatus    [4]byte
	PrinterState string
	Warning      string
	Error        string
	MediaError   string
	IsOnline     bool
	CanPrint     bool
	LastChecked  time.Time
}

type Printer struct {
	ID            int64
	Name          string
	IPAddress     string
	Port          int
	DPI           int
	LabelWidthMM  float64
	LabelHeightMM float64
	GapMM         float64
	Status        string
	LastSeenAt    *time.Time
	TotalPrints   int64
}

type PrinterStatusChange struct {
	PrinterID   int64          `json:"printer_id"`
	PrinterName string         `json:"printer_name"`
	OldStatus   string         `json:"old_status"`
	NewStatus   string         `json:"new_status"`
	Details     *PrinterStatus `json:"details,omitempty"`
	Timestamp   time.Time      `json:"timestamp"`
}
