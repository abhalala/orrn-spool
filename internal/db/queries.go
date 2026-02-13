package db

const (
	InsertPrinter = `
		INSERT INTO printers (name, ip_address, port, dpi, label_width_mm, label_height_mm, gap_mm, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`

	GetPrinterByID = `
		SELECT id, name, ip_address, port, dpi, label_width_mm, label_height_mm, gap_mm, status, last_seen_at, total_prints, created_at, updated_at
		FROM printers WHERE id = ?
	`

	GetPrinterByIP = `
		SELECT id, name, ip_address, port, dpi, label_width_mm, label_height_mm, gap_mm, status, last_seen_at, total_prints, created_at, updated_at
		FROM printers WHERE ip_address = ?
	`

	ListPrinters = `
		SELECT id, name, ip_address, port, dpi, label_width_mm, label_height_mm, gap_mm, status, last_seen_at, total_prints, created_at, updated_at
		FROM printers ORDER BY name ASC
	`

	ListPrintersByStatus = `
		SELECT id, name, ip_address, port, dpi, label_width_mm, label_height_mm, gap_mm, status, last_seen_at, total_prints, created_at, updated_at
		FROM printers WHERE status = ? ORDER BY name ASC
	`

	UpdatePrinter = `
		UPDATE printers SET
			name = ?, ip_address = ?, port = ?, dpi = ?,
			label_width_mm = ?, label_height_mm = ?, gap_mm = ?
		WHERE id = ?
	`

	UpdatePrinterStatus = `
		UPDATE printers SET status = ?, last_seen_at = CURRENT_TIMESTAMP WHERE id = ?
	`

	IncrementPrinterPrints = `
		UPDATE printers SET total_prints = total_prints + ? WHERE id = ?
	`

	DeletePrinter = `DELETE FROM printers WHERE id = ?`
)

const (
	InsertTemplate = `
		INSERT INTO label_templates (name, description, schema_json, width_mm, height_mm)
		VALUES (?, ?, ?, ?, ?)
	`

	GetTemplateByID = `
		SELECT id, name, description, schema_json, width_mm, height_mm, created_at, updated_at
		FROM label_templates WHERE id = ?
	`

	GetTemplateByName = `
		SELECT id, name, description, schema_json, width_mm, height_mm, created_at, updated_at
		FROM label_templates WHERE name = ?
	`

	ListTemplates = `
		SELECT id, name, description, schema_json, width_mm, height_mm, created_at, updated_at
		FROM label_templates ORDER BY name ASC
	`

	UpdateTemplate = `
		UPDATE label_templates SET
			name = ?, description = ?, schema_json = ?, width_mm = ?, height_mm = ?
		WHERE id = ?
	`

	DeleteTemplate = `DELETE FROM label_templates WHERE id = ?`
)

const (
	InsertJob = `
		INSERT INTO print_jobs (printer_id, template_id, variables_json, tspl_content, priority, copies, submitted_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	GetJobByID = `
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs WHERE id = ?
	`

	GetJobsByStatus = `
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs WHERE status = ? ORDER BY priority DESC, created_at ASC LIMIT ?
	`

	GetJobsByPrinter = `
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs WHERE printer_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?
	`

	ListJobs = `
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs ORDER BY created_at DESC LIMIT ? OFFSET ?
	`

	ListJobsWithFilter = `
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs WHERE status IN (?) ORDER BY created_at DESC LIMIT ? OFFSET ?
	`

	UpdateJobStatus = `
		UPDATE print_jobs SET status = ?, error_message = ?, started_at = ?, completed_at = ? WHERE id = ?
	`

	UpdateJobProcessing = `
		UPDATE print_jobs SET status = 'processing', started_at = CURRENT_TIMESTAMP WHERE id = ?
	`

	IncrementJobRetry = `
		UPDATE print_jobs SET retry_count = retry_count + 1, status = 'pending' WHERE id = ?
	`

	CountJobsByStatus = `
		SELECT status, COUNT(*) as count FROM print_jobs GROUP BY status
	`

	CountPendingJobs = `
		SELECT COUNT(*) FROM print_jobs WHERE status IN ('pending', 'paused')
	`

	CountJobsByPrinter = `
		SELECT COUNT(*) FROM print_jobs WHERE printer_id = ?
	`

	DeleteJob = `DELETE FROM print_jobs WHERE id = ?`

	DeleteCompletedJobs = `
		DELETE FROM print_jobs WHERE status IN ('completed', 'cancelled') AND completed_at < datetime('now', ?)
	`

	GetJobsForArchival = `
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs WHERE status IN ('completed', 'failed', 'cancelled') AND completed_at < datetime('now', ?)
	`
)

const (
	InsertWebhook = `
		INSERT INTO webhooks (name, url, secret, events_json, enabled)
		VALUES (?, ?, ?, ?, ?)
	`

	GetWebhookByID = `
		SELECT id, name, url, secret, events_json, enabled, created_at
		FROM webhooks WHERE id = ?
	`

	ListWebhooks = `
		SELECT id, name, url, secret, events_json, enabled, created_at
		FROM webhooks ORDER BY name ASC
	`

	ListEnabledWebhooks = `
		SELECT id, name, url, secret, events_json, enabled, created_at
		FROM webhooks WHERE enabled = 1 ORDER BY name ASC
	`

	ListWebhooksForEvent = `
		SELECT id, name, url, secret, events_json, enabled, created_at
		FROM webhooks WHERE enabled = 1 AND events_json LIKE ?
	`

	UpdateWebhook = `
		UPDATE webhooks SET name = ?, url = ?, secret = ?, events_json = ?, enabled = ? WHERE id = ?
	`

	DeleteWebhook = `DELETE FROM webhooks WHERE id = ?`
)

const (
	GetSetting = `SELECT value, encrypted FROM settings WHERE key = ?`

	SetSetting = `
		INSERT INTO settings (key, value, encrypted, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = ?, encrypted = ?, updated_at = CURRENT_TIMESTAMP
	`

	DeleteSetting = `DELETE FROM settings WHERE key = ?`

	ListSettings = `SELECT key, value, encrypted, updated_at FROM settings ORDER BY key ASC`
)

const (
	InsertAuditLog = `
		INSERT INTO audit_log (action, entity_type, entity_id, details_json, ip_address)
		VALUES (?, ?, ?, ?, ?)
	`

	ListAuditLog = `
		SELECT id, action, entity_type, entity_id, details_json, ip_address, created_at
		FROM audit_log ORDER BY created_at DESC LIMIT ? OFFSET ?
	`

	ListAuditLogByEntity = `
		SELECT id, action, entity_type, entity_id, details_json, ip_address, created_at
		FROM audit_log WHERE entity_type = ? AND entity_id = ? ORDER BY created_at DESC
	`

	ListAuditLogByAction = `
		SELECT id, action, entity_type, entity_id, details_json, ip_address, created_at
		FROM audit_log WHERE action = ? ORDER BY created_at DESC LIMIT ? OFFSET ?
	`
)

const (
	InsertArchiveJob = `
		INSERT INTO archive_jobs (original_job_id, archive_file)
		VALUES (?, ?)
	`

	GetArchiveJobByID = `
		SELECT id, original_job_id, archive_file, archived_at
		FROM archive_jobs WHERE id = ?
	`

	GetArchiveJobByOriginalID = `
		SELECT id, original_job_id, archive_file, archived_at
		FROM archive_jobs WHERE original_job_id = ?
	`

	ListArchiveJobs = `
		SELECT id, original_job_id, archive_file, archived_at
		FROM archive_jobs ORDER BY archived_at DESC LIMIT ? OFFSET ?
	`

	DeleteArchiveJob = `DELETE FROM archive_jobs WHERE id = ?`
)

const (
	InsertPrintCounter = `
		INSERT INTO print_counters (printer_id, date, count)
		VALUES (?, ?, ?)
		ON CONFLICT(printer_id, date) DO UPDATE SET count = count + ?
	`

	GetPrintCounter = `
		SELECT id, printer_id, date, count
		FROM print_counters WHERE printer_id = ? AND date = ?
	`

	GetPrintCountersByPrinter = `
		SELECT id, printer_id, date, count
		FROM print_counters WHERE printer_id = ? ORDER BY date DESC LIMIT ?
	`

	GetPrintCountersByDateRange = `
		SELECT id, printer_id, date, count
		FROM print_counters WHERE printer_id = ? AND date >= ? AND date <= ? ORDER BY date ASC
	`

	SumPrintCountersByDateRange = `
		SELECT COALESCE(SUM(count), 0) FROM print_counters WHERE printer_id = ? AND date >= ? AND date <= ?
	`
)

const (
	GetMigrationStatus = `
		SELECT version, applied_at FROM schema_migrations ORDER BY version ASC
	`

	GetAppliedMigrations = `
		SELECT version FROM schema_migrations
	`
)
