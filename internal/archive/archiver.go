package archive

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Archiver struct {
	db          *sql.DB
	archivePath string
	archiveDays int
	passphrase  string
	stopCh      chan struct{}
	mu          sync.Mutex
}

type ArchiveFile struct {
	Filename  string    `json:"filename"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	JobCount  int       `json:"job_count"`
	DateRange string    `json:"date_range"`
}

type ArchiveConfig struct {
	ArchivePath string
	ArchiveDays int
	Passphrase  string
}

func NewArchiver(db *sql.DB, config ArchiveConfig) (*Archiver, error) {
	if config.ArchivePath == "" {
		config.ArchivePath = "./data/archives"
	}
	if config.ArchiveDays <= 0 {
		config.ArchiveDays = 30
	}

	if err := os.MkdirAll(config.ArchivePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create archive directory: %w", err)
	}

	return &Archiver{
		db:          db,
		archivePath: config.ArchivePath,
		archiveDays: config.ArchiveDays,
		passphrase:  config.Passphrase,
		stopCh:      make(chan struct{}),
	}, nil
}

func (a *Archiver) Start() {
	go a.runDailyArchive()
}

func (a *Archiver) Stop() {
	close(a.stopCh)
}

func (a *Archiver) runDailyArchive() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-a.stopCh:
			return
		case <-ticker.C:
			a.RunArchive()
		}
	}
}

func (a *Archiver) RunArchive() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.passphrase == "" {
		return fmt.Errorf("passphrase not set")
	}

	cutoff := time.Now().AddDate(0, 0, -a.archiveDays)

	jobs, err := a.getJobsForArchival(cutoff)
	if err != nil {
		return fmt.Errorf("failed to get jobs for archival: %w", err)
	}

	if len(jobs) == 0 {
		return nil
	}

	archiveDBPath := filepath.Join(a.archivePath, fmt.Sprintf("archive_%s.db", time.Now().Format("2006_01")))

	archiveDB, err := a.openOrCreateArchiveDB(archiveDBPath)
	if err != nil {
		return fmt.Errorf("failed to create archive database: %w", err)
	}
	defer archiveDB.Close()

	tx, err := archiveDB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin archive transaction: %w", err)
	}

	for _, job := range jobs {
		if err := a.insertJobToArchive(tx, job); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert job to archive: %w", err)
		}
	}

	if _, err := tx.Exec(`
		INSERT OR REPLACE INTO archive_metadata (id, archived_at, source_database)
		VALUES (1, ?, 'main')
	`, time.Now()); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to update archive metadata: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit archive transaction: %w", err)
	}

	archiveDB.Close()

	if err := a.encryptAndCleanup(archiveDBPath); err != nil {
		return fmt.Errorf("failed to encrypt archive: %w", err)
	}

	if err := a.deleteArchivedJobs(jobs); err != nil {
		return fmt.Errorf("failed to delete archived jobs: %w", err)
	}

	if err := a.recordArchiveJobs(jobs, filepath.Base(archiveDBPath)+".age"); err != nil {
		return fmt.Errorf("failed to record archive jobs: %w", err)
	}

	return nil
}

type archivedJob struct {
	ID            int64
	PrinterID     int64
	TemplateID    int64
	VariablesJSON string
	TSPLContent   string
	Status        string
	Priority      int
	RetryCount    int
	ErrorMessage  string
	Copies        int
	SubmittedBy   string
	CreatedAt     time.Time
	StartedAt     *time.Time
	CompletedAt   *time.Time
}

func (a *Archiver) getJobsForArchival(cutoff time.Time) ([]*archivedJob, error) {
	rows, err := a.db.Query(`
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs
		WHERE status IN ('completed', 'failed', 'cancelled')
		AND completed_at IS NOT NULL
		AND completed_at < ?
		ORDER BY completed_at ASC
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*archivedJob
	for rows.Next() {
		job := &archivedJob{}
		if err := rows.Scan(
			&job.ID, &job.PrinterID, &job.TemplateID, &job.VariablesJSON, &job.TSPLContent,
			&job.Status, &job.Priority, &job.RetryCount, &job.ErrorMessage, &job.Copies,
			&job.SubmittedBy, &job.CreatedAt, &job.StartedAt, &job.CompletedAt,
		); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (a *Archiver) openOrCreateArchiveDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS print_jobs (
			id INTEGER PRIMARY KEY,
			printer_id INTEGER NOT NULL,
			template_id INTEGER NOT NULL,
			variables_json TEXT,
			tspl_content TEXT,
			status TEXT NOT NULL,
			priority INTEGER DEFAULT 0,
			retry_count INTEGER DEFAULT 0,
			error_message TEXT,
			copies INTEGER DEFAULT 1,
			submitted_by TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			completed_at DATETIME
		);

		CREATE TABLE IF NOT EXISTS archive_metadata (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			archived_at DATETIME,
			source_database TEXT
		);

		CREATE INDEX IF NOT EXISTS idx_archive_jobs_completed_at ON print_jobs(completed_at);
		CREATE INDEX IF NOT EXISTS idx_archive_jobs_status ON print_jobs(status);
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func (a *Archiver) insertJobToArchive(tx *sql.Tx, job *archivedJob) error {
	_, err := tx.Exec(`
		INSERT OR REPLACE INTO print_jobs (id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.ID, job.PrinterID, job.TemplateID, job.VariablesJSON, job.TSPLContent,
		job.Status, job.Priority, job.RetryCount, job.ErrorMessage, job.Copies,
		job.SubmittedBy, job.CreatedAt, job.StartedAt, job.CompletedAt)
	return err
}

func (a *Archiver) encryptAndCleanup(archiveDBPath string) error {
	encryptedPath := archiveDBPath + ".age"

	if err := a.encryptFile(archiveDBPath, encryptedPath); err != nil {
		return err
	}

	return os.Remove(archiveDBPath)
}

func (a *Archiver) encryptFile(inputPath, outputPath string) error {
	cmd := exec.Command("age", "-a", "-p", "-o", outputPath, inputPath)
	cmd.Stdin = bytes.NewReader([]byte(a.passphrase + "\n" + a.passphrase + "\n"))
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("age encryption failed: %w, output: %s", err, string(output))
	}
	return nil
}

func (a *Archiver) decryptFile(inputPath, outputPath string) error {
	cmd := exec.Command("age", "-d", "-o", outputPath, inputPath)
	cmd.Stdin = bytes.NewReader([]byte(a.passphrase + "\n"))
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("age decryption failed: %w, output: %s", err, string(output))
	}
	return nil
}

func (a *Archiver) deleteArchivedJobs(jobs []*archivedJob) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}

	for _, job := range jobs {
		if _, err := tx.Exec("DELETE FROM print_jobs WHERE id = ?", job.ID); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

func (a *Archiver) recordArchiveJobs(jobs []*archivedJob, archiveFile string) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}

	for _, job := range jobs {
		if _, err := tx.Exec(`
			INSERT INTO archive_jobs (original_job_id, archive_file)
			VALUES (?, ?)
		`, job.ID, archiveFile); err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

func (a *Archiver) ListArchives() ([]*ArchiveFile, error) {
	files, err := os.ReadDir(a.archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read archive directory: %w", err)
	}

	var archives []*ArchiveFile
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".age") {
			continue
		}

		info, err := file.Info()
		if err != nil {
			continue
		}

		archiveFile := &ArchiveFile{
			Filename:  file.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		}

		if strings.HasPrefix(file.Name(), "archive_") {
			datePart := strings.TrimPrefix(file.Name(), "archive_")
			datePart = strings.TrimSuffix(datePart, ".age")
			archiveFile.DateRange = datePart
		}

		archives = append(archives, archiveFile)
	}

	return archives, nil
}

func (a *Archiver) GetArchiveInfo(filename string) (*ArchiveFile, error) {
	filePath := filepath.Join(a.archivePath, filename)

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("archive not found")
		}
		return nil, fmt.Errorf("failed to stat archive: %w", err)
	}

	archiveFile := &ArchiveFile{
		Filename:  filename,
		Size:      info.Size(),
		CreatedAt: info.ModTime(),
	}

	if strings.HasPrefix(filename, "archive_") {
		datePart := strings.TrimPrefix(filename, "archive_")
		datePart = strings.TrimSuffix(datePart, ".age")
		archiveFile.DateRange = datePart
	}

	jobCount, err := a.getArchiveJobCount(filename)
	if err == nil {
		archiveFile.JobCount = jobCount
	}

	return archiveFile, nil
}

func (a *Archiver) getArchiveJobCount(filename string) (int, error) {
	var count int
	err := a.db.QueryRow(`
		SELECT COUNT(*) FROM archive_jobs WHERE archive_file = ?
	`, filename).Scan(&count)
	return count, err
}

func (a *Archiver) DecryptArchive(filename string, outputPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.passphrase == "" {
		return fmt.Errorf("passphrase not set")
	}

	filePath := filepath.Join(a.archivePath, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("archive not found")
	}

	if err := a.decryptFile(filePath, outputPath); err != nil {
		return fmt.Errorf("failed to decrypt archive: %w", err)
	}

	return nil
}

func (a *Archiver) DeleteArchive(filename string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	filePath := filepath.Join(a.archivePath, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("archive not found")
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete archive: %w", err)
	}

	if _, err := a.db.Exec("DELETE FROM archive_jobs WHERE archive_file = ?", filename); err != nil {
		return fmt.Errorf("failed to delete archive job records: %w", err)
	}

	return nil
}

func (a *Archiver) SetPassphrase(passphrase string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.passphrase = passphrase
	return nil
}

func (a *Archiver) SetArchiveDays(days int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.archiveDays = days
}

func (a *Archiver) GetArchiveDays() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.archiveDays
}

func (a *Archiver) HasPassphrase() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.passphrase != ""
}

func (a *Archiver) GetArchivePath() string {
	return a.archivePath
}

func (a *Archiver) GetArchiveJobCountByMonth(ctx context.Context, year, month int) (int, error) {
	filename := fmt.Sprintf("archive_%04d_%02d.age", year, month)
	var count int
	err := a.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM archive_jobs WHERE archive_file = ?
	`, filename).Scan(&count)
	return count, err
}

func (a *Archiver) GetArchivedJobsByOriginalID(ctx context.Context, originalID int64) (*ArchiveJobInfo, error) {
	var archiveFile string
	var archivedAt time.Time
	err := a.db.QueryRowContext(ctx, `
		SELECT archive_file, archived_at FROM archive_jobs WHERE original_job_id = ?
	`, originalID).Scan(&archiveFile, &archivedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	return &ArchiveJobInfo{
		OriginalID: originalID,
		ArchiveFile: archiveFile,
		ArchivedAt:  archivedAt,
	}, nil
}

type ArchiveJobInfo struct {
	OriginalID  int64     `json:"original_id"`
	ArchiveFile string    `json:"archive_file"`
	ArchivedAt  time.Time `json:"archived_at"`
}

func (a *Archiver) RestoreJobFromArchive(ctx context.Context, originalID int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.passphrase == "" {
		return fmt.Errorf("passphrase not set")
	}

	info, err := a.GetArchivedJobsByOriginalID(ctx, originalID)
	if err != nil {
		return err
	}
	if info == nil {
		return fmt.Errorf("job not found in archives")
	}

	archivePath := filepath.Join(a.archivePath, info.ArchiveFile)
	tmpFile, err := os.CreateTemp("", "archive-restore-*.db")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	if err := a.decryptFile(archivePath, tmpPath); err != nil {
		return fmt.Errorf("failed to decrypt archive: %w", err)
	}

	archiveDB, err := sql.Open("sqlite3", tmpPath)
	if err != nil {
		return fmt.Errorf("failed to open archive database: %w", err)
	}
	defer archiveDB.Close()

	var job archivedJob
	err = archiveDB.QueryRow(`
		SELECT id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at
		FROM print_jobs WHERE id = ?
	`, originalID).Scan(
		&job.ID, &job.PrinterID, &job.TemplateID, &job.VariablesJSON, &job.TSPLContent,
		&job.Status, &job.Priority, &job.RetryCount, &job.ErrorMessage, &job.Copies,
		&job.SubmittedBy, &job.CreatedAt, &job.StartedAt, &job.CompletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("job not found in archive database")
		}
		return fmt.Errorf("failed to query archived job: %w", err)
	}

	_, err = a.db.ExecContext(ctx, `
		INSERT INTO print_jobs (id, printer_id, template_id, variables_json, tspl_content, status, priority, retry_count, error_message, copies, submitted_by, created_at, started_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.ID, job.PrinterID, job.TemplateID, job.VariablesJSON, job.TSPLContent,
		job.Status, job.Priority, job.RetryCount, job.ErrorMessage, job.Copies,
		job.SubmittedBy, job.CreatedAt, job.StartedAt, job.CompletedAt)
	if err != nil {
		return fmt.Errorf("failed to restore job: %w", err)
	}

	_, err = a.db.ExecContext(ctx, "DELETE FROM archive_jobs WHERE original_job_id = ?", originalID)
	if err != nil {
		return fmt.Errorf("failed to remove archive record: %w", err)
	}

	return nil
}