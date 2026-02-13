package core

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"orrn-spool/internal/config"
)

type JobStatus string

const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusPaused     JobStatus = "paused"
	JobStatusCancelled  JobStatus = "cancelled"
)

type Job struct {
	ID            int64
	PrinterID     int64
	TemplateID    int64
	VariablesJSON string
	TSPLContent   string
	Status        JobStatus
	Priority      int
	RetryCount    int
	MaxRetries    int
	Copies        int
	ErrorMessage  string
	SubmittedBy   string
	CreatedAt     time.Time
	StartedAt     *time.Time
	CompletedAt   *time.Time
}

type QueueStats struct {
	Pending    int
	Processing int
	Completed  int
	Failed     int
	Paused     int
	Cancelled  int
	Total      int
}

type WebhookSender interface {
	SendJobEvent(event string, jobID int64, printerID int64, status JobStatus, errorMsg string) error
}

type PrinterManagerInterface interface {
	Print(printerID int64, tsplContent string, copies int) error
	GetPrinter(printerID int64) (*Printer, error)
	IncrementPrintCount(printerID int64, count int) error
}

type TSPL2GeneratorInterface interface {
	GenerateFromTemplate(templateID int64, variablesJSON string) (string, error)
}

type Queue struct {
	db             *sql.DB
	printerManager PrinterManagerInterface
	tsplGenerator  TSPL2GeneratorInterface
	webhookSender  WebhookSender
	config         *config.QueueConfig
	workers        int
	stopCh         chan struct{}
	jobCh          chan int64
	pausedPrinters map[int64]bool
	mu             sync.RWMutex
	running        bool
}

func NewQueue(db *sql.DB, pm PrinterManagerInterface, tg TSPL2GeneratorInterface, ws WebhookSender, cfg *config.QueueConfig) *Queue {
	if cfg == nil {
		cfg = &config.QueueConfig{
			MaxRetries:  3,
			RetryDelay:  10 * time.Second,
			WorkerCount: 2,
		}
	}
	if cfg.WorkerCount < 1 {
		cfg.WorkerCount = 2
	}

	return &Queue{
		db:             db,
		printerManager: pm,
		tsplGenerator:  tg,
		webhookSender:  ws,
		config:         cfg,
		workers:        cfg.WorkerCount,
		stopCh:         make(chan struct{}),
		jobCh:          make(chan int64, 1000),
		pausedPrinters: make(map[int64]bool),
	}
}

func (q *Queue) Start() error {
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return nil
	}
	q.running = true
	q.mu.Unlock()

	if err := q.recoverJobs(); err != nil {
		return fmt.Errorf("failed to recover jobs: %w", err)
	}

	for i := 0; i < q.workers; i++ {
		go q.worker(i)
	}

	go q.dispatcher()

	return nil
}

func (q *Queue) Stop() {
	q.mu.Lock()
	if !q.running {
		q.mu.Unlock()
		return
	}
	q.running = false
	q.mu.Unlock()

	close(q.stopCh)
}

func (q *Queue) recoverJobs() error {
	_, err := q.db.Exec("UPDATE print_jobs SET status = 'pending' WHERE status = 'processing'")
	if err != nil {
		return fmt.Errorf("failed to reset processing jobs: %w", err)
	}

	rows, err := q.db.Query(`
		SELECT id FROM print_jobs 
		WHERE status = 'pending' 
		ORDER BY priority DESC, created_at ASC
	`)
	if err != nil {
		return fmt.Errorf("failed to query pending jobs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var jobID int64
		if err := rows.Scan(&jobID); err != nil {
			return fmt.Errorf("failed to scan job id: %w", err)
		}
		select {
		case q.jobCh <- jobID:
		default:
		}
	}

	return nil
}

func (q *Queue) dispatcher() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-q.stopCh:
			return
		case <-ticker.C:
			q.enqueuePendingJobs()
		}
	}
}

func (q *Queue) enqueuePendingJobs() {
	rows, err := q.db.Query(`
		SELECT id FROM print_jobs 
		WHERE status = 'pending' 
		ORDER BY priority DESC, created_at ASC
		LIMIT 100
	`)
	if err != nil {
		log.Printf("failed to query pending jobs: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var jobID int64
		if err := rows.Scan(&jobID); err != nil {
			continue
		}
		select {
		case q.jobCh <- jobID:
		default:
			return
		}
	}
}

func (q *Queue) worker(id int) {
	for {
		select {
		case <-q.stopCh:
			return
		case jobID := <-q.jobCh:
			q.processJob(jobID)
		}
	}
}

func (q *Queue) processJob(jobID int64) {
	job, err := q.GetJob(jobID)
	if err != nil {
		log.Printf("worker: failed to get job %d: %v", jobID, err)
		return
	}

	if job.Status != JobStatusPending {
		return
	}

	q.mu.RLock()
	printerPaused := q.pausedPrinters[job.PrinterID]
	q.mu.RUnlock()

	if printerPaused {
		q.updateJobStatus(jobID, JobStatusPaused, "", nil, nil)
		return
	}

	if job.TSPLContent == "" && q.tsplGenerator != nil {
		tspl, err := q.tsplGenerator.GenerateFromTemplate(job.TemplateID, job.VariablesJSON)
		if err != nil {
			q.handleJobFailure(job, fmt.Sprintf("TSPL generation failed: %v", err))
			return
		}
		job.TSPLContent = tspl
		q.updateJobTSPL(jobID, tspl)
	}

	now := time.Now()
	q.updateJobStatus(jobID, JobStatusProcessing, "", &now, nil)

	if q.webhookSender != nil {
		q.webhookSender.SendJobEvent("job_started", jobID, job.PrinterID, JobStatusProcessing, "")
	}

	if q.printerManager == nil {
		q.handleJobFailure(job, "printer manager not configured")
		return
	}

	err = q.printerManager.Print(job.PrinterID, job.TSPLContent, job.Copies)
	if err != nil {
		q.handleJobFailure(job, err.Error())
		return
	}

	now = time.Now()
	q.updateJobStatus(jobID, JobStatusCompleted, "", nil, &now)

	if q.webhookSender != nil {
		q.webhookSender.SendJobEvent("job_completed", jobID, job.PrinterID, JobStatusCompleted, "")
	}

	q.printerManager.IncrementPrintCount(job.PrinterID, job.Copies)

	q.incrementPrintCounter(job.PrinterID, job.Copies)
}

func (q *Queue) handleJobFailure(job *Job, errMsg string) {
	if job.RetryCount < job.MaxRetries {
		delay := q.calculateBackoff(job.RetryCount)
		time.AfterFunc(delay, func() {
			q.retryJob(job.ID)
		})
		q.incrementRetryCount(job.ID)
		return
	}

	now := time.Now()
	q.updateJobStatus(job.ID, JobStatusFailed, errMsg, nil, &now)

	if q.webhookSender != nil {
		q.webhookSender.SendJobEvent("job_failed", job.ID, job.PrinterID, JobStatusFailed, errMsg)
	}
}

func (q *Queue) calculateBackoff(retryCount int) time.Duration {
	baseDelay := q.config.RetryDelay
	if baseDelay == 0 {
		baseDelay = 10 * time.Second
	}
	backoff := baseDelay * time.Duration(1<<uint(retryCount))
	maxBackoff := 5 * time.Minute
	if backoff > maxBackoff {
		backoff = maxBackoff
	}
	return backoff
}

func (q *Queue) retryJob(jobID int64) {
	q.updateJobStatus(jobID, JobStatusPending, "", nil, nil)
	select {
	case q.jobCh <- jobID:
	default:
	}
}

func (q *Queue) incrementRetryCount(jobID int64) {
	q.db.Exec("UPDATE print_jobs SET retry_count = retry_count + 1 WHERE id = ?", jobID)
}

func (q *Queue) updateJobStatus(jobID int64, status JobStatus, errMsg string, startedAt, completedAt *time.Time) {
	var startedAtVal, completedAtVal interface{}
	if startedAt != nil {
		startedAtVal = startedAt
	}
	if completedAt != nil {
		completedAtVal = completedAt
	}

	q.db.Exec(`
		UPDATE print_jobs 
		SET status = ?, error_message = ?, started_at = ?, completed_at = ? 
		WHERE id = ?
	`, status, errMsg, startedAtVal, completedAtVal, jobID)
}

func (q *Queue) updateJobTSPL(jobID int64, tspl string) {
	q.db.Exec("UPDATE print_jobs SET tspl_content = ? WHERE id = ?", tspl, jobID)
}

func (q *Queue) incrementPrintCounter(printerID int64, count int) {
	today := time.Now().Format("2006-01-02")
	q.db.Exec(`
		INSERT INTO print_counters (printer_id, date, count)
		VALUES (?, ?, ?)
		ON CONFLICT(printer_id, date) DO UPDATE SET count = count + ?
	`, printerID, today, count, count)
}

func (q *Queue) Enqueue(job *Job) (int64, error) {
	if job.MaxRetries == 0 {
		job.MaxRetries = q.config.MaxRetries
	}
	if job.Status == "" {
		job.Status = JobStatusPending
	}

	result, err := q.db.Exec(`
		INSERT INTO print_jobs (printer_id, template_id, variables_json, tspl_content, status, priority, copies, submitted_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, job.PrinterID, job.TemplateID, job.VariablesJSON, job.TSPLContent, job.Status, job.Priority, job.Copies, job.SubmittedBy)
	if err != nil {
		return 0, fmt.Errorf("failed to insert job: %w", err)
	}

	jobID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get job id: %w", err)
	}

	select {
	case q.jobCh <- jobID:
	default:
	}

	return jobID, nil
}

func (q *Queue) Dequeue() (*Job, error) {
	tx, err := q.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var job Job
	err = tx.QueryRow(`
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs 
		WHERE status = 'pending' 
		ORDER BY priority DESC, created_at ASC 
		LIMIT 1
	`).Scan(
		&job.ID, &job.PrinterID, &job.TemplateID, &job.VariablesJSON, &job.TSPLContent,
		&job.Status, &job.Priority, &job.RetryCount, &job.ErrorMessage,
		&job.Copies, &job.SubmittedBy, &job.CreatedAt, &job.StartedAt, &job.CompletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query job: %w", err)
	}

	now := time.Now()
	_, err = tx.Exec(`
		UPDATE print_jobs SET status = 'processing', started_at = ? WHERE id = ?
	`, now, job.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update job status: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	job.Status = JobStatusProcessing
	job.StartedAt = &now

	return &job, nil
}

func (q *Queue) GetJob(id int64) (*Job, error) {
	var job Job
	var startedAt, completedAt sql.NullTime
	err := q.db.QueryRow(`
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs WHERE id = ?
	`, id).Scan(
		&job.ID, &job.PrinterID, &job.TemplateID, &job.VariablesJSON, &job.TSPLContent,
		&job.Status, &job.Priority, &job.RetryCount, &job.ErrorMessage,
		&job.Copies, &job.SubmittedBy, &job.CreatedAt, &startedAt, &completedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found: %d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query job: %w", err)
	}

	if startedAt.Valid {
		job.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}

	return &job, nil
}

func (q *Queue) ListJobs(status JobStatus, limit, offset int) ([]*Job, error) {
	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = q.db.Query(`
			SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
			FROM print_jobs WHERE status = ?
			ORDER BY priority DESC, created_at DESC
			LIMIT ? OFFSET ?
		`, status, limit, offset)
	} else {
		rows, err = q.db.Query(`
			SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
			FROM print_jobs
			ORDER BY priority DESC, created_at DESC
			LIMIT ? OFFSET ?
		`, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		job := &Job{}
		var startedAt, completedAt sql.NullTime
		err := rows.Scan(
			&job.ID, &job.PrinterID, &job.TemplateID, &job.VariablesJSON, &job.TSPLContent,
			&job.Status, &job.Priority, &job.RetryCount, &job.ErrorMessage,
			&job.Copies, &job.SubmittedBy, &job.CreatedAt, &startedAt, &completedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		if startedAt.Valid {
			job.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			job.CompletedAt = &completedAt.Time
		}
		jobs = append(jobs, job)
	}

	return jobs, nil
}

func (q *Queue) CountByStatus(status JobStatus) (int, error) {
	var count int
	err := q.db.QueryRow("SELECT COUNT(*) FROM print_jobs WHERE status = ?", status).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count jobs: %w", err)
	}
	return count, nil
}

func (q *Queue) CancelJob(id int64) error {
	result, err := q.db.Exec(`
		UPDATE print_jobs SET status = 'cancelled', completed_at = CURRENT_TIMESTAMP 
		WHERE id = ? AND status IN ('pending', 'paused')
	`, id)
	if err != nil {
		return fmt.Errorf("failed to cancel job: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("job cannot be cancelled (not in pending/paused state)")
	}

	return nil
}

func (q *Queue) RetryJob(id int64) error {
	job, err := q.GetJob(id)
	if err != nil {
		return err
	}

	if job.Status != JobStatusFailed {
		return fmt.Errorf("only failed jobs can be retried")
	}

	_, err = q.db.Exec(`
		UPDATE print_jobs 
		SET status = 'pending', retry_count = 0, error_message = '', started_at = NULL, completed_at = NULL 
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("failed to retry job: %w", err)
	}

	select {
	case q.jobCh <- id:
	default:
	}

	return nil
}

func (q *Queue) ReprintJob(id int64) (int64, error) {
	job, err := q.GetJob(id)
	if err != nil {
		return 0, err
	}

	newJob := &Job{
		PrinterID:     job.PrinterID,
		TemplateID:    job.TemplateID,
		VariablesJSON: job.VariablesJSON,
		TSPLContent:   job.TSPLContent,
		Priority:      job.Priority,
		MaxRetries:    job.MaxRetries,
		Copies:        job.Copies,
		SubmittedBy:   job.SubmittedBy,
		Status:        JobStatusPending,
	}

	return q.Enqueue(newJob)
}

func (q *Queue) PausePrinter(printerID int64) error {
	q.mu.Lock()
	q.pausedPrinters[printerID] = true
	q.mu.Unlock()

	_, err := q.db.Exec(`
		UPDATE print_jobs SET status = 'paused' 
		WHERE printer_id = ? AND status = 'pending'
	`, printerID)
	if err != nil {
		return fmt.Errorf("failed to pause printer jobs: %w", err)
	}

	return nil
}

func (q *Queue) ResumePrinter(printerID int64) error {
	q.mu.Lock()
	delete(q.pausedPrinters, printerID)
	q.mu.Unlock()

	rows, err := q.db.Query(`
		SELECT id FROM print_jobs 
		WHERE printer_id = ? AND status = 'paused'
	`, printerID)
	if err != nil {
		return fmt.Errorf("failed to query paused jobs: %w", err)
	}
	defer rows.Close()

	var jobIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		jobIDs = append(jobIDs, id)
	}

	for _, id := range jobIDs {
		q.updateJobStatus(id, JobStatusPending, "", nil, nil)
		select {
		case q.jobCh <- id:
		default:
		}
	}

	return nil
}

func (q *Queue) PauseJob(id int64) error {
	result, err := q.db.Exec(`
		UPDATE print_jobs SET status = 'paused' 
		WHERE id = ? AND status IN ('pending', 'processing')
	`, id)
	if err != nil {
		return fmt.Errorf("failed to pause job: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("job cannot be paused")
	}

	return nil
}

func (q *Queue) ResumeJob(id int64) error {
	job, err := q.GetJob(id)
	if err != nil {
		return err
	}

	if job.Status != JobStatusPaused {
		return fmt.Errorf("only paused jobs can be resumed")
	}

	q.mu.RLock()
	printerPaused := q.pausedPrinters[job.PrinterID]
	q.mu.RUnlock()

	if printerPaused {
		return fmt.Errorf("printer is paused, resume printer first")
	}

	q.updateJobStatus(id, JobStatusPending, "", nil, nil)

	select {
	case q.jobCh <- id:
	default:
	}

	return nil
}

func (q *Queue) GetStats() *QueueStats {
	stats := &QueueStats{}

	rows, err := q.db.Query("SELECT status, COUNT(*) FROM print_jobs GROUP BY status")
	if err != nil {
		return stats
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		stats.Total += count
		switch JobStatus(status) {
		case JobStatusPending:
			stats.Pending = count
		case JobStatusProcessing:
			stats.Processing = count
		case JobStatusCompleted:
			stats.Completed = count
		case JobStatusFailed:
			stats.Failed = count
		case JobStatusPaused:
			stats.Paused = count
		case JobStatusCancelled:
			stats.Cancelled = count
		}
	}

	return stats
}

func (q *Queue) IsPrinterPaused(printerID int64) bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.pausedPrinters[printerID]
}

func (q *Queue) GetPausedPrinters() []int64 {
	q.mu.RLock()
	defer q.mu.RUnlock()

	printers := make([]int64, 0, len(q.pausedPrinters))
	for id := range q.pausedPrinters {
		printers = append(printers, id)
	}
	return printers
}
