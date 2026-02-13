-- 001_initial.sql
-- Initial schema for TSC spool

-- Printers table: Stores printer configurations and status
CREATE TABLE IF NOT EXISTS printers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    ip_address TEXT NOT NULL UNIQUE,
    port INTEGER DEFAULT 9100,
    dpi INTEGER DEFAULT 203,
    label_width_mm REAL,
    label_height_mm REAL,
    gap_mm REAL DEFAULT 2,
    status TEXT DEFAULT 'unknown' CHECK(status IN ('online', 'offline', 'paused', 'error', 'unknown')),
    last_seen_at DATETIME,
    total_prints INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_printers_status ON printers(status);
CREATE INDEX IF NOT EXISTS idx_printers_ip ON printers(ip_address);

-- Label templates table: Stores label template definitions
CREATE TABLE IF NOT EXISTS label_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    schema_json TEXT NOT NULL,
    width_mm REAL NOT NULL,
    height_mm REAL NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_templates_name ON label_templates(name);

-- Print jobs table: Stores print job queue and history
CREATE TABLE IF NOT EXISTS print_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    printer_id INTEGER REFERENCES printers(id) ON DELETE SET NULL,
    template_id INTEGER REFERENCES label_templates(id) ON DELETE SET NULL,
    variables_json TEXT,
    tspl_content TEXT,
    status TEXT DEFAULT 'pending' CHECK(status IN ('pending', 'processing', 'completed', 'failed', 'paused', 'cancelled')),
    priority INTEGER DEFAULT 0,
    retry_count INTEGER DEFAULT 0,
    error_message TEXT,
    copies INTEGER DEFAULT 1,
    submitted_by TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    completed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_jobs_status ON print_jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_printer ON print_jobs(printer_id);
CREATE INDEX IF NOT EXISTS idx_jobs_created ON print_jobs(created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_priority ON print_jobs(priority DESC, created_at ASC);

-- Print counters table: Daily print count per printer
CREATE TABLE IF NOT EXISTS print_counters (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    printer_id INTEGER REFERENCES printers(id) ON DELETE CASCADE,
    date DATE,
    count INTEGER DEFAULT 0,
    UNIQUE(printer_id, date)
);

CREATE INDEX IF NOT EXISTS idx_counters_printer_date ON print_counters(printer_id, date);

-- Webhooks table: Webhook configurations
CREATE TABLE IF NOT EXISTS webhooks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    secret TEXT,
    events_json TEXT,
    enabled INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_webhooks_enabled ON webhooks(enabled);

-- Settings table: Application settings
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT,
    encrypted INTEGER DEFAULT 0,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Audit log table: Audit trail for all actions
CREATE TABLE IF NOT EXISTS audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    entity_type TEXT,
    entity_id INTEGER,
    details_json TEXT,
    ip_address TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);
CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_log(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at);

-- Archive jobs table: Archive records for old jobs
CREATE TABLE IF NOT EXISTS archive_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    original_job_id INTEGER,
    archive_file TEXT,
    archived_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_archive_original ON archive_jobs(original_job_id);

-- Triggers for updated_at
CREATE TRIGGER IF NOT EXISTS printers_updated_at
AFTER UPDATE ON printers
BEGIN
    UPDATE printers SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS label_templates_updated_at
AFTER UPDATE ON label_templates
BEGIN
    UPDATE label_templates SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
