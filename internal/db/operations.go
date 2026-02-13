package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type PrinterOperations struct{}

func (o *PrinterOperations) CreatePrinter(ctx context.Context, p *Printer) error {
	result, err := GetDB().ExecContext(ctx, InsertPrinter,
		p.Name, p.IPAddress, p.Port, p.DPI,
		p.LabelWidthMM, p.LabelHeightMM, p.GapMM, p.Status)
	if err != nil {
		return fmt.Errorf("failed to create printer: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get printer id: %w", err)
	}
	p.ID = id
	return nil
}

func (o *PrinterOperations) GetPrinterByID(ctx context.Context, id int64) (*Printer, error) {
	p := &Printer{}
	err := GetDB().QueryRowContext(ctx, GetPrinterByID, id).Scan(
		&p.ID, &p.Name, &p.IPAddress, &p.Port, &p.DPI,
		&p.LabelWidthMM, &p.LabelHeightMM, &p.GapMM, &p.Status,
		&p.LastSeenAt, &p.TotalPrints, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get printer: %w", err)
	}
	return p, nil
}

func (o *PrinterOperations) GetPrinterByIP(ctx context.Context, ip string) (*Printer, error) {
	p := &Printer{}
	err := GetDB().QueryRowContext(ctx, GetPrinterByIP, ip).Scan(
		&p.ID, &p.Name, &p.IPAddress, &p.Port, &p.DPI,
		&p.LabelWidthMM, &p.LabelHeightMM, &p.GapMM, &p.Status,
		&p.LastSeenAt, &p.TotalPrints, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get printer by ip: %w", err)
	}
	return p, nil
}

func (o *PrinterOperations) ListPrinters(ctx context.Context) ([]*Printer, error) {
	rows, err := GetDB().QueryContext(ctx, ListPrinters)
	if err != nil {
		return nil, fmt.Errorf("failed to list printers: %w", err)
	}
	defer rows.Close()

	var printers []*Printer
	for rows.Next() {
		p := &Printer{}
		if err := rows.Scan(
			&p.ID, &p.Name, &p.IPAddress, &p.Port, &p.DPI,
			&p.LabelWidthMM, &p.LabelHeightMM, &p.GapMM, &p.Status,
			&p.LastSeenAt, &p.TotalPrints, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan printer: %w", err)
		}
		printers = append(printers, p)
	}
	return printers, rows.Err()
}

func (o *PrinterOperations) UpdatePrinter(ctx context.Context, p *Printer) error {
	_, err := GetDB().ExecContext(ctx, UpdatePrinter,
		p.Name, p.IPAddress, p.Port, p.DPI,
		p.LabelWidthMM, p.LabelHeightMM, p.GapMM, p.ID)
	if err != nil {
		return fmt.Errorf("failed to update printer: %w", err)
	}
	return nil
}

func (o *PrinterOperations) UpdatePrinterStatus(ctx context.Context, id int64, status string, lastSeen *time.Time) error {
	if lastSeen != nil {
		_, err := GetDB().ExecContext(ctx, UpdatePrinterStatus, status, id)
		return err
	}
	_, err := GetDB().ExecContext(ctx, UpdatePrinterStatus, status, id)
	return err
}

func (o *PrinterOperations) IncrementPrintCount(ctx context.Context, id int64) error {
	_, err := GetDB().ExecContext(ctx, IncrementPrinterPrints, 1, id)
	if err != nil {
		return fmt.Errorf("failed to increment print count: %w", err)
	}
	return nil
}

func (o *PrinterOperations) DeletePrinter(ctx context.Context, id int64) error {
	_, err := GetDB().ExecContext(ctx, DeletePrinter, id)
	if err != nil {
		return fmt.Errorf("failed to delete printer: %w", err)
	}
	return nil
}

type TemplateOperations struct{}

func (o *TemplateOperations) CreateTemplate(ctx context.Context, t *LabelTemplate) error {
	result, err := GetDB().ExecContext(ctx, InsertTemplate,
		t.Name, t.Description, t.SchemaJSON, t.WidthMM, t.HeightMM)
	if err != nil {
		return fmt.Errorf("failed to create template: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get template id: %w", err)
	}
	t.ID = id
	return nil
}

func (o *TemplateOperations) GetTemplateByID(ctx context.Context, id int64) (*LabelTemplate, error) {
	t := &LabelTemplate{}
	err := GetDB().QueryRowContext(ctx, GetTemplateByID, id).Scan(
		&t.ID, &t.Name, &t.Description, &t.SchemaJSON,
		&t.WidthMM, &t.HeightMM, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get template: %w", err)
	}
	return t, nil
}

func (o *TemplateOperations) GetTemplateByName(ctx context.Context, name string) (*LabelTemplate, error) {
	t := &LabelTemplate{}
	err := GetDB().QueryRowContext(ctx, GetTemplateByName, name).Scan(
		&t.ID, &t.Name, &t.Description, &t.SchemaJSON,
		&t.WidthMM, &t.HeightMM, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get template by name: %w", err)
	}
	return t, nil
}

func (o *TemplateOperations) ListTemplates(ctx context.Context) ([]*LabelTemplate, error) {
	rows, err := GetDB().QueryContext(ctx, ListTemplates)
	if err != nil {
		return nil, fmt.Errorf("failed to list templates: %w", err)
	}
	defer rows.Close()

	var templates []*LabelTemplate
	for rows.Next() {
		t := &LabelTemplate{}
		if err := rows.Scan(
			&t.ID, &t.Name, &t.Description, &t.SchemaJSON,
			&t.WidthMM, &t.HeightMM, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan template: %w", err)
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

func (o *TemplateOperations) UpdateTemplate(ctx context.Context, t *LabelTemplate) error {
	_, err := GetDB().ExecContext(ctx, UpdateTemplate,
		t.Name, t.Description, t.SchemaJSON, t.WidthMM, t.HeightMM, t.ID)
	if err != nil {
		return fmt.Errorf("failed to update template: %w", err)
	}
	return nil
}

func (o *TemplateOperations) DeleteTemplate(ctx context.Context, id int64) error {
	_, err := GetDB().ExecContext(ctx, DeleteTemplate, id)
	if err != nil {
		return fmt.Errorf("failed to delete template: %w", err)
	}
	return nil
}

type JobOperations struct{}

func (o *JobOperations) CreateJob(ctx context.Context, j *PrintJob) error {
	result, err := GetDB().ExecContext(ctx, InsertJob,
		j.PrinterID, j.TemplateID, j.VariablesJSON, j.TSPLContent,
		j.Priority, j.Copies, j.SubmittedBy)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get job id: %w", err)
	}
	j.ID = id
	return nil
}

func (o *JobOperations) GetJobByID(ctx context.Context, id int64) (*PrintJob, error) {
	j := &PrintJob{}
	err := GetDB().QueryRowContext(ctx, GetJobByID, id).Scan(
		&j.ID, &j.PrinterID, &j.TemplateID, &j.VariablesJSON, &j.TSPLContent,
		&j.Status, &j.Priority, &j.RetryCount, &j.ErrorMessage, &j.Copies,
		&j.SubmittedBy, &j.CreatedAt, &j.StartedAt, &j.CompletedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get job: %w", err)
	}
	return j, nil
}

func (o *JobOperations) GetJobsByStatus(ctx context.Context, status string, limit, offset int) ([]*PrintJob, error) {
	rows, err := GetDB().QueryContext(ctx, GetJobsByStatus, status, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get jobs by status: %w", err)
	}
	defer rows.Close()

	return scanJobs(rows)
}

func (o *JobOperations) GetPendingJobs(ctx context.Context, limit int) ([]*PrintJob, error) {
	query := `
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs WHERE status = 'pending' ORDER BY priority DESC, created_at ASC LIMIT ?
	`
	rows, err := GetDB().QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending jobs: %w", err)
	}
	defer rows.Close()

	return scanJobs(rows)
}

func (o *JobOperations) UpdateJobStatus(ctx context.Context, id int64, status string, errorMsg string) error {
	var startedAt, completedAt interface{}
	now := time.Now()

	switch status {
	case "processing":
		startedAt = now
	case "completed", "failed", "cancelled":
		completedAt = now
	}

	_, err := GetDB().ExecContext(ctx, UpdateJobStatus, status, errorMsg, startedAt, completedAt, id)
	if err != nil {
		return fmt.Errorf("failed to update job status: %w", err)
	}
	return nil
}

func (o *JobOperations) UpdateJobProcessing(ctx context.Context, id int64, tspl string, startedAt time.Time) error {
	_, err := GetDB().ExecContext(ctx, "UPDATE print_jobs SET tspl_content = ?, status = 'processing', started_at = ? WHERE id = ?", tspl, startedAt, id)
	if err != nil {
		return fmt.Errorf("failed to update job processing: %w", err)
	}
	return nil
}

func (o *JobOperations) IncrementRetryCount(ctx context.Context, id int64) error {
	_, err := GetDB().ExecContext(ctx, IncrementJobRetry, id)
	if err != nil {
		return fmt.Errorf("failed to increment retry count: %w", err)
	}
	return nil
}

func (o *JobOperations) ListJobs(ctx context.Context, filter JobFilter) ([]*PrintJob, error) {
	var conditions []string
	var args []interface{}

	if filter.PrinterID > 0 {
		conditions = append(conditions, "printer_id = ?")
		args = append(args, filter.PrinterID)
	}
	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.FromDate != nil {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, filter.FromDate)
	}
	if filter.ToDate != nil {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, filter.ToDate)
	}

	orderBy := "created_at"
	if filter.OrderBy != "" {
		orderBy = filter.OrderBy
	}
	orderDir := "DESC"
	if filter.OrderDir != "" {
		orderDir = filter.OrderDir
	}

	query := "SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at FROM print_jobs"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += fmt.Sprintf(" ORDER BY %s %s", orderBy, orderDir)

	limit := 100
	if filter.Limit > 0 {
		limit = filter.Limit
	}
	offset := filter.Offset

	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := GetDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	return scanJobs(rows)
}

func (o *JobOperations) CountJobsByStatus(ctx context.Context, status string) (int64, error) {
	var count int64
	err := GetDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM print_jobs WHERE status = ?", status).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count jobs by status: %w", err)
	}
	return count, nil
}

func (o *JobOperations) DeleteJob(ctx context.Context, id int64) error {
	_, err := GetDB().ExecContext(ctx, DeleteJob, id)
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}
	return nil
}

func scanJobs(rows *sql.Rows) ([]*PrintJob, error) {
	var jobs []*PrintJob
	for rows.Next() {
		j := &PrintJob{}
		if err := rows.Scan(
			&j.ID, &j.PrinterID, &j.TemplateID, &j.VariablesJSON, &j.TSPLContent,
			&j.Status, &j.Priority, &j.RetryCount, &j.ErrorMessage, &j.Copies,
			&j.SubmittedBy, &j.CreatedAt, &j.StartedAt, &j.CompletedAt); err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

type WebhookOperations struct{}

func (o *WebhookOperations) CreateWebhook(ctx context.Context, w *Webhook) error {
	result, err := GetDB().ExecContext(ctx, InsertWebhook,
		w.Name, w.URL, w.Secret, w.EventsJSON, w.Enabled)
	if err != nil {
		return fmt.Errorf("failed to create webhook: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get webhook id: %w", err)
	}
	w.ID = id
	return nil
}

func (o *WebhookOperations) GetWebhookByID(ctx context.Context, id int64) (*Webhook, error) {
	w := &Webhook{}
	err := GetDB().QueryRowContext(ctx, GetWebhookByID, id).Scan(
		&w.ID, &w.Name, &w.URL, &w.Secret, &w.EventsJSON, &w.Enabled, &w.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get webhook: %w", err)
	}
	return w, nil
}

func (o *WebhookOperations) ListWebhooks(ctx context.Context) ([]*Webhook, error) {
	rows, err := GetDB().QueryContext(ctx, ListWebhooks)
	if err != nil {
		return nil, fmt.Errorf("failed to list webhooks: %w", err)
	}
	defer rows.Close()

	var webhooks []*Webhook
	for rows.Next() {
		w := &Webhook{}
		if err := rows.Scan(
			&w.ID, &w.Name, &w.URL, &w.Secret, &w.EventsJSON, &w.Enabled, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan webhook: %w", err)
		}
		webhooks = append(webhooks, w)
	}
	return webhooks, rows.Err()
}

func (o *WebhookOperations) ListActiveWebhooksForEvent(ctx context.Context, event string) ([]*Webhook, error) {
	pattern := "%\"" + event + "\"%"
	rows, err := GetDB().QueryContext(ctx, ListWebhooksForEvent, pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list webhooks for event: %w", err)
	}
	defer rows.Close()

	var webhooks []*Webhook
	for rows.Next() {
		w := &Webhook{}
		if err := rows.Scan(
			&w.ID, &w.Name, &w.URL, &w.Secret, &w.EventsJSON, &w.Enabled, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan webhook: %w", err)
		}
		webhooks = append(webhooks, w)
	}
	return webhooks, rows.Err()
}

func (o *WebhookOperations) UpdateWebhook(ctx context.Context, w *Webhook) error {
	_, err := GetDB().ExecContext(ctx, UpdateWebhook,
		w.Name, w.URL, w.Secret, w.EventsJSON, w.Enabled, w.ID)
	if err != nil {
		return fmt.Errorf("failed to update webhook: %w", err)
	}
	return nil
}

func (o *WebhookOperations) DeleteWebhook(ctx context.Context, id int64) error {
	_, err := GetDB().ExecContext(ctx, DeleteWebhook, id)
	if err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}
	return nil
}

type SettingsOperations struct{}

func (o *SettingsOperations) GetSetting(ctx context.Context, key string) (*Setting, error) {
	s := &Setting{Key: key}
	err := GetDB().QueryRowContext(ctx, GetSetting, key).Scan(&s.Value, &s.Encrypted)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("failed to get setting: %w", err)
	}
	return s, nil
}

func (o *SettingsOperations) SetSetting(ctx context.Context, key, value string, encrypted bool) error {
	_, err := GetDB().ExecContext(ctx, SetSetting, key, value, encrypted, value, encrypted)
	if err != nil {
		return fmt.Errorf("failed to set setting: %w", err)
	}
	return nil
}

func (o *SettingsOperations) DeleteSetting(ctx context.Context, key string) error {
	_, err := GetDB().ExecContext(ctx, DeleteSetting, key)
	if err != nil {
		return fmt.Errorf("failed to delete setting: %w", err)
	}
	return nil
}

type AuditOperations struct{}

func (o *AuditOperations) CreateAuditLog(ctx context.Context, log *AuditLog) error {
	result, err := GetDB().ExecContext(ctx, InsertAuditLog,
		log.Action, log.EntityType, log.EntityID, log.DetailsJSON, log.IPAddress)
	if err != nil {
		return fmt.Errorf("failed to create audit log: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get audit log id: %w", err)
	}
	log.ID = id
	return nil
}

func (o *AuditOperations) ListAuditLogs(ctx context.Context, filter AuditFilter, limit, offset int) ([]*AuditLog, error) {
	var conditions []string
	var args []interface{}

	if filter.Action != "" {
		conditions = append(conditions, "action = ?")
		args = append(args, filter.Action)
	}
	if filter.EntityType != "" {
		conditions = append(conditions, "entity_type = ?")
		args = append(args, filter.EntityType)
	}
	if filter.EntityID > 0 {
		conditions = append(conditions, "entity_id = ?")
		args = append(args, filter.EntityID)
	}

	query := "SELECT id, action, entity_type, entity_id, details_json, ip_address, created_at FROM audit_log"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := GetDB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list audit logs: %w", err)
	}
	defer rows.Close()

	var logs []*AuditLog
	for rows.Next() {
		log := &AuditLog{}
		if err := rows.Scan(
			&log.ID, &log.Action, &log.EntityType, &log.EntityID,
			&log.DetailsJSON, &log.IPAddress, &log.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan audit log: %w", err)
		}
		logs = append(logs, log)
	}
	return logs, rows.Err()
}

type CounterOperations struct{}

func (o *CounterOperations) IncrementDailyCounter(ctx context.Context, printerID int64, date time.Time) error {
	dateStr := date.Format("2006-01-02")
	_, err := GetDB().ExecContext(ctx, InsertPrintCounter, printerID, dateStr, 1, 1)
	if err != nil {
		return fmt.Errorf("failed to increment daily counter: %w", err)
	}
	return nil
}

func (o *CounterOperations) GetCounters(ctx context.Context, printerID int64, from, to time.Time) ([]*PrintCounter, error) {
	fromStr := from.Format("2006-01-02")
	toStr := to.Format("2006-01-02")
	rows, err := GetDB().QueryContext(ctx, GetPrintCountersByDateRange, printerID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("failed to get counters: %w", err)
	}
	defer rows.Close()

	var counters []*PrintCounter
	for rows.Next() {
		c := &PrintCounter{}
		var dateStr string
		if err := rows.Scan(&c.ID, &c.PrinterID, &dateStr, &c.Count); err != nil {
			return nil, fmt.Errorf("failed to scan counter: %w", err)
		}
		c.Date, _ = time.Parse("2006-01-02", dateStr)
		counters = append(counters, c)
	}
	return counters, rows.Err()
}

type ArchiveOperations struct{}

func (o *ArchiveOperations) CreateArchiveJob(ctx context.Context, a *ArchiveJob) error {
	result, err := GetDB().ExecContext(ctx, InsertArchiveJob, a.OriginalJobID, a.ArchiveFile)
	if err != nil {
		return fmt.Errorf("failed to create archive job: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get archive job id: %w", err)
	}
	a.ID = id
	return nil
}

func (o *ArchiveOperations) GetArchiveJobs(ctx context.Context, limit, offset int) ([]*ArchiveJob, error) {
	rows, err := GetDB().QueryContext(ctx, ListArchiveJobs, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get archive jobs: %w", err)
	}
	defer rows.Close()

	var archives []*ArchiveJob
	for rows.Next() {
		a := &ArchiveJob{}
		if err := rows.Scan(&a.ID, &a.OriginalJobID, &a.ArchiveFile, &a.ArchivedAt); err != nil {
			return nil, fmt.Errorf("failed to scan archive job: %w", err)
		}
		archives = append(archives, a)
	}
	return archives, rows.Err()
}

var (
	Printers  = &PrinterOperations{}
	Templates = &TemplateOperations{}
	Jobs      = &JobOperations{}
	Webhooks  = &WebhookOperations{}
	Settings  = &SettingsOperations{}
	Audit     = &AuditOperations{}
	Counters  = &CounterOperations{}
	Archive   = &ArchiveOperations{}
)
