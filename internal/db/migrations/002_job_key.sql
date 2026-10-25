-- Print exactly-once: a client-provided job key is stored uniquely so a repeated
-- POST returns the first job instead of printing twice. NULL means "no key".
ALTER TABLE print_jobs ADD COLUMN job_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_job_key ON print_jobs(job_key) WHERE job_key IS NOT NULL;
