package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type JobState string

const (
	StateAccepted        JobState = "accepted"
	StateDeviceOpen      JobState = "device_open"
	StateTransmitting    JobState = "transmitting"
	StateSent            JobState = "sent"
	StateFailedRetryable JobState = "failed_retryable"
	StateFailedFinal     JobState = "failed_final"
	StateOutcomeUnknown  JobState = "outcome_unknown"
	StateCancelled       JobState = "cancelled"
)

var (
	ErrJobNotFound       = errors.New("edge job not found")
	ErrJobNotReprintable = errors.New("edge job is not eligible for offline reprint")
	ErrJobStateChanged   = errors.New("edge job state changed before transition")
	ErrIdentityRequired  = errors.New("active edge identity is required")
)

type PrintCommandInput struct {
	CommandID   string `json:"commandId"`
	Kind        string `json:"kind,omitempty"`
	JobKey      string `json:"jobKey"`
	PayloadHash string `json:"payloadHash"`
	PrinterID   string `json:"printerId"`
	TSPL        string `json:"tspl"`
	Copies      int    `json:"copies"`
	// InvalidReason rejects the command durably with this detail.
	InvalidReason string `json:"-"`
}

type CommandResolution struct {
	CommandID  string `json:"commandId"`
	Outcome    string `json:"outcome"`
	Resolution string `json:"resolution"`
	AgentJobID string `json:"agentJobId,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

type Job struct {
	ID            string   `json:"id"`
	CommandID     string   `json:"commandId,omitempty"`
	JobKey        string   `json:"jobKey"`
	PayloadHash   string   `json:"payloadHash"`
	PrinterID     string   `json:"printerId"`
	TSPL          string   `json:"tspl"`
	Copies        int      `json:"copies"`
	State         JobState `json:"state"`
	Attempts      int      `json:"attempts"`
	Sequence      int      `json:"sequence"`
	LastError     string   `json:"lastError,omitempty"`
	SourceJobID   string   `json:"sourceJobId,omitempty"`
	ReprintByUser string   `json:"reprintByUser,omitempty"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
}

type OutboxEvent struct {
	EventID       string         `json:"eventId"`
	DeploymentID  string         `json:"deploymentId"`
	AggregateID   string         `json:"aggregateId"`
	Sequence      int            `json:"sequence"`
	Kind          string         `json:"kind"`
	OccurredAt    string         `json:"occurredAt"`
	Payload       map[string]any `json:"payload"`
	CreatedAt     string         `json:"-"`
	NextAttemptAt string         `json:"-"`
	AttemptCount  int            `json:"-"`
}

type Store struct {
	db *sql.DB
}

func NewStore(ctx context.Context, sqlDB *sql.DB) (*Store, error) {
	if sqlDB == nil {
		return nil, errors.New("sql.DB is required")
	}
	s := &Store{db: sqlDB}
	if err := s.initSchema(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) initSchema(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS edge_commands (
	command_id TEXT PRIMARY KEY,
	deployment_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	job_key TEXT NOT NULL DEFAULT '',
	payload_hash TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL,
	resolution TEXT NOT NULL,
	agent_job_id TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	resolved_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS edge_jobs (
	id TEXT PRIMARY KEY,
	deployment_id TEXT NOT NULL DEFAULT '',
	command_id TEXT NOT NULL DEFAULT '',
	job_key TEXT NOT NULL UNIQUE,
	payload_hash TEXT NOT NULL,
	printer_id TEXT NOT NULL,
	tspl TEXT NOT NULL,
	copies INTEGER NOT NULL DEFAULT 1,
	state TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	sequence INTEGER NOT NULL DEFAULT 1,
	last_error TEXT NOT NULL DEFAULT '',
	next_attempt_at TEXT NOT NULL DEFAULT '',
	source_job_id TEXT NOT NULL DEFAULT '',
	reprint_by_user TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_edge_jobs_printer_state ON edge_jobs(printer_id, state, created_at);

CREATE TABLE IF NOT EXISTS edge_outbox (
	event_id TEXT PRIMARY KEY,
	deployment_id TEXT NOT NULL DEFAULT '',
	aggregate_id TEXT NOT NULL,
	sequence INTEGER NOT NULL,
	kind TEXT NOT NULL,
	occurred_at TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT '',
	next_attempt_at TEXT NOT NULL DEFAULT '',
	attempt_count INTEGER NOT NULL DEFAULT 0,
	acked_at TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_edge_outbox_unacked ON edge_outbox(acked_at, sequence);

CREATE TABLE IF NOT EXISTS edge_config_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	deployment_id TEXT NOT NULL DEFAULT '',
	applied_config_version INTEGER NOT NULL DEFAULT 0,
	config_json TEXT NOT NULL DEFAULT '{}',
	updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS edge_runtime_owner (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	deployment_id TEXT NOT NULL,
	instance_id TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return err
	}
	for _, table := range []string{"edge_commands", "edge_jobs", "edge_outbox", "edge_config_state"} {
		if err := ensureDeploymentColumn(ctx, s.db, table); err != nil {
			return err
		}
	}
	if err := ensureColumn(ctx, s.db, "edge_jobs", "next_attempt_at", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "created_at", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "next_attempt_at", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "attempt_count", definition: "INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := ensureColumn(ctx, s.db, "edge_outbox", column.name, column.definition); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
	return err
}

func ensureDeploymentColumn(ctx context.Context, db *sql.DB, table string) error {
	return ensureColumn(ctx, db, table, "deployment_id", "TEXT NOT NULL DEFAULT ''")
}

// BindIdentity atomically switches execution and event delivery to one enrolled
// identity. Historical rows remain owned by their original deployment.
func (s *Store) BindIdentity(ctx context.Context, deploymentID, instanceID string) (bool, error) {
	deploymentID = strings.TrimSpace(deploymentID)
	instanceID = strings.TrimSpace(instanceID)
	if deploymentID == "" || instanceID == "" {
		return false, ErrIdentityRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var current string
	err = tx.QueryRowContext(ctx, `SELECT deployment_id FROM edge_runtime_owner WHERE id = 1`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	changed := !errors.Is(err, sql.ErrNoRows) && current != deploymentID
	now := nowISO()
	if errors.Is(err, sql.ErrNoRows) {
		for _, table := range []string{"edge_commands", "edge_jobs", "edge_outbox", "edge_config_state"} {
			if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET deployment_id = ? WHERE deployment_id = ''`, deploymentID); err != nil {
				return false, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO edge_runtime_owner (id, deployment_id, instance_id, updated_at) VALUES (1, ?, ?, ?)`, deploymentID, instanceID, now); err != nil {
			return false, err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE edge_runtime_owner SET deployment_id = ?, instance_id = ?, updated_at = ? WHERE id = 1`, deploymentID, instanceID, now); err != nil {
			return false, err
		}
	}
	if changed {
		if _, err := tx.ExecContext(ctx, `UPDATE edge_config_state SET deployment_id = ?, applied_config_version = 0, config_json = '{}', updated_at = ? WHERE id = 1`, deploymentID, now); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

// PreserveUnownedHistory assigns pre-ownership rows to the previous identity
// before an enrollment change, without moving an already-bound runtime owner.
func (s *Store) PreserveUnownedHistory(ctx context.Context, deploymentID, instanceID string) error {
	deploymentID = strings.TrimSpace(deploymentID)
	instanceID = strings.TrimSpace(instanceID)
	if deploymentID == "" || instanceID == "" {
		return ErrIdentityRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT deployment_id FROM edge_runtime_owner WHERE id = 1`).Scan(&existing)
	if err == nil {
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	for _, table := range []string{"edge_commands", "edge_jobs", "edge_outbox", "edge_config_state"} {
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET deployment_id = ? WHERE deployment_id = ''`, deploymentID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO edge_runtime_owner (id, deployment_id, instance_id, updated_at) VALUES (1, ?, ?, ?)`, deploymentID, instanceID, nowISO()); err != nil {
		return err
	}
	return tx.Commit()
}

func currentDeploymentTx(ctx context.Context, tx *sql.Tx) (string, error) {
	var deploymentID string
	err := tx.QueryRowContext(ctx, `SELECT deployment_id FROM edge_runtime_owner WHERE id = 1`).Scan(&deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return deploymentID, err
}

func currentDeployment(ctx context.Context, db *sql.DB) (string, error) {
	var deploymentID string
	err := db.QueryRowContext(ctx, `SELECT deployment_id FROM edge_runtime_owner WHERE id = 1`).Scan(&deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return deploymentID, err
}

func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func nowISO() string {
	// Fixed width so stored timestamps sort lexically.
	return time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func (s *Store) ResolvePrintCommand(ctx context.Context, in PrintCommandInput) (CommandResolution, error) {
	in.CommandID = strings.TrimSpace(in.CommandID)
	in.JobKey = strings.TrimSpace(in.JobKey)
	in.PayloadHash = strings.TrimSpace(in.PayloadHash)
	in.PrinterID = strings.TrimSpace(in.PrinterID)
	if in.Copies <= 0 {
		in.Copies = 1
	}

	if in.CommandID == "" {
		return CommandResolution{}, errors.New("commandId is required")
	}
	if in.Kind == "" {
		in.Kind = "print_job"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandResolution{}, err
	}
	defer func() { _ = tx.Rollback() }()
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return CommandResolution{}, err
	}

	// 1. Check if commandId was already resolved.
	var existing CommandResolution
	err = tx.QueryRowContext(ctx, `
		SELECT command_id, outcome, resolution, agent_job_id, detail
		FROM edge_commands
		WHERE command_id = ? AND deployment_id = ?
	`, in.CommandID, deploymentID).Scan(&existing.CommandID, &existing.Outcome, &existing.Resolution, &existing.AgentJobID, &existing.Detail)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return CommandResolution{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandResolution{}, err
	}

	now := nowISO()
	if in.InvalidReason == "" && (in.JobKey == "" || in.PayloadHash == "" || in.PrinterID == "" || strings.TrimSpace(in.TSPL) == "") {
		in.InvalidReason = "Missing required print_job fields"
	}
	if in.InvalidReason != "" {
		res := CommandResolution{
			CommandID:  in.CommandID,
			Outcome:    "rejected",
			Resolution: "invalid_payload",
			Detail:     in.InvalidReason,
		}
		if err := insertCommandRow(ctx, tx, in.CommandID, in.Kind, in.JobKey, in.PayloadHash, res, now); err != nil {
			return CommandResolution{}, err
		}
		if err := tx.Commit(); err != nil {
			return CommandResolution{}, err
		}
		return res, nil
	}

	// 2. Check if job_key already exists.
	var existingJobID, existingHash string
	err = tx.QueryRowContext(ctx, `
		SELECT id, payload_hash
		FROM edge_jobs
		WHERE job_key = ? AND deployment_id = ?
	`, in.JobKey, deploymentID).Scan(&existingJobID, &existingHash)
	if err == nil {
		if existingHash != in.PayloadHash {
			res := CommandResolution{
				CommandID:  in.CommandID,
				Outcome:    "rejected",
				Resolution: "job_key_conflict",
				Detail:     "Payload hash mismatch for existing job_key",
			}
			if err := insertCommandRow(ctx, tx, in.CommandID, in.Kind, in.JobKey, in.PayloadHash, res, now); err != nil {
				return CommandResolution{}, err
			}
			if err := tx.Commit(); err != nil {
				return CommandResolution{}, err
			}
			return res, nil
		}

		res := CommandResolution{
			CommandID:  in.CommandID,
			Outcome:    "accepted",
			Resolution: "already_accepted",
			AgentJobID: existingJobID,
		}
		if err := insertCommandRow(ctx, tx, in.CommandID, in.Kind, in.JobKey, in.PayloadHash, res, now); err != nil {
			return CommandResolution{}, err
		}
		if err := tx.Commit(); err != nil {
			return CommandResolution{}, err
		}
		return res, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandResolution{}, err
	}

	// 3. Create new accepted job + outbox event.
	jobID := newUUIDv4()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO edge_jobs (
			id, deployment_id, command_id, job_key, payload_hash, printer_id, tspl, copies,
			state, attempts, sequence, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 1, '', ?, ?)
	`, jobID, deploymentID, in.CommandID, in.JobKey, in.PayloadHash, in.PrinterID, in.TSPL, in.Copies, string(StateAccepted), now, now)
	if err != nil {
		return CommandResolution{}, err
	}

	res := CommandResolution{
		CommandID:  in.CommandID,
		Outcome:    "accepted",
		Resolution: "accepted",
		AgentJobID: jobID,
	}
	if err := insertCommandRow(ctx, tx, in.CommandID, in.Kind, in.JobKey, in.PayloadHash, res, now); err != nil {
		return CommandResolution{}, err
	}

	if err := insertJobEvent(ctx, tx, jobID, 1, "job_state_changed", now, map[string]any{
		"state":      string(StateAccepted),
		"jobKey":     in.JobKey,
		"commandId":  in.CommandID,
		"printerId":  in.PrinterID,
		"agentJobId": jobID,
		"reason":     nil,
	}); err != nil {
		return CommandResolution{}, err
	}

	if err := tx.Commit(); err != nil {
		return CommandResolution{}, err
	}
	return res, nil
}

func (s *Store) ResolveInvalidCommand(ctx context.Context, commandID, kind, detail string) (CommandResolution, error) {
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return CommandResolution{}, errors.New("commandId is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandResolution{}, err
	}
	defer func() { _ = tx.Rollback() }()
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return CommandResolution{}, err
	}
	var existing CommandResolution
	err = tx.QueryRowContext(ctx, `
		SELECT command_id, outcome, resolution, agent_job_id, detail
		FROM edge_commands WHERE command_id = ? AND deployment_id = ?
	`, commandID, deploymentID).Scan(&existing.CommandID, &existing.Outcome, &existing.Resolution, &existing.AgentJobID, &existing.Detail)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return CommandResolution{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandResolution{}, err
	}
	res := CommandResolution{CommandID: commandID, Outcome: "rejected", Resolution: "invalid_payload", Detail: detail}
	if err := insertCommandRow(ctx, tx, commandID, kind, "", "", res, nowISO()); err != nil {
		return CommandResolution{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommandResolution{}, err
	}
	return res, nil
}

func (s *Store) ResolveCancelCommand(ctx context.Context, commandID string, targetJobIDOrKey string) (CommandResolution, error) {
	commandID = strings.TrimSpace(commandID)
	targetJobIDOrKey = strings.TrimSpace(targetJobIDOrKey)
	if commandID == "" {
		return CommandResolution{}, errors.New("commandId is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandResolution{}, err
	}
	defer func() { _ = tx.Rollback() }()
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return CommandResolution{}, err
	}

	var existing CommandResolution
	err = tx.QueryRowContext(ctx, `
		SELECT command_id, outcome, resolution, agent_job_id, detail
		FROM edge_commands
		WHERE command_id = ? AND deployment_id = ?
	`, commandID, deploymentID).Scan(&existing.CommandID, &existing.Outcome, &existing.Resolution, &existing.AgentJobID, &existing.Detail)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return CommandResolution{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandResolution{}, err
	}

	now := nowISO()
	var jobID, jobKey, printerID, stateStr string
	var seq int
	err = tx.QueryRowContext(ctx, `
		SELECT id, job_key, printer_id, state, sequence
		FROM edge_jobs
		WHERE (id = ? OR job_key = ?) AND deployment_id = ?
		LIMIT 1
	`, targetJobIDOrKey, targetJobIDOrKey, deploymentID).Scan(&jobID, &jobKey, &printerID, &stateStr, &seq)
	if errors.Is(err, sql.ErrNoRows) {
		res := CommandResolution{
			CommandID:  commandID,
			Outcome:    "rejected",
			Resolution: "job_not_found",
			Detail:     "Target job does not exist on agent",
		}
		if err := insertCommandRow(ctx, tx, commandID, "cancel_job", targetJobIDOrKey, "", res, now); err != nil {
			return CommandResolution{}, err
		}
		if err := tx.Commit(); err != nil {
			return CommandResolution{}, err
		}
		return res, nil
	}
	if err != nil {
		return CommandResolution{}, err
	}

	var res CommandResolution
	switch JobState(stateStr) {
	case StateCancelled:
		res = CommandResolution{
			CommandID:  commandID,
			Outcome:    "accepted",
			Resolution: "already_cancelled",
			AgentJobID: jobID,
		}
	case StateAccepted, StateDeviceOpen, StateFailedRetryable:
		nextSeq := seq + 1
		result, err := tx.ExecContext(ctx, `
			UPDATE edge_jobs
			SET state = ?, sequence = ?, updated_at = ?
			WHERE id = ? AND state = ?
		`, string(StateCancelled), nextSeq, now, jobID, stateStr)
		if err != nil {
			return CommandResolution{}, err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			return CommandResolution{}, ErrJobStateChanged
		}
		if err := insertJobEvent(ctx, tx, jobID, nextSeq, "job_state_changed", now, map[string]any{
			"state":      string(StateCancelled),
			"jobKey":     jobKey,
			"commandId":  commandID,
			"printerId":  printerID,
			"agentJobId": jobID,
			"reason":     "cancelled_by_command",
		}); err != nil {
			return CommandResolution{}, err
		}
		res = CommandResolution{
			CommandID:  commandID,
			Outcome:    "accepted",
			Resolution: "cancelled",
			AgentJobID: jobID,
		}
	case StateTransmitting:
		res = CommandResolution{
			CommandID:  commandID,
			Outcome:    "rejected",
			Resolution: "transmission_started",
			AgentJobID: jobID,
			Detail:     "Cannot cancel job while TCP transmission is in progress",
		}
	default:
		res = CommandResolution{
			CommandID:  commandID,
			Outcome:    "rejected",
			Resolution: "job_not_cancellable",
			AgentJobID: jobID,
			Detail:     fmt.Sprintf("Job in state %s cannot be cancelled", stateStr),
		}
	}

	if err := insertCommandRow(ctx, tx, commandID, "cancel_job", jobKey, "", res, now); err != nil {
		return CommandResolution{}, err
	}
	if err := tx.Commit(); err != nil {
		return CommandResolution{}, err
	}
	return res, nil
}

func (s *Store) ClaimNextJob(ctx context.Context, printerID string) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return nil, err
	}

	now := nowISO()
	row := tx.QueryRowContext(ctx, `
		SELECT id, command_id, job_key, payload_hash, printer_id, tspl, copies,
		       state, attempts, sequence, last_error, source_job_id, reprint_by_user, created_at, updated_at
		FROM edge_jobs
		WHERE printer_id = ? AND deployment_id = ? AND (
			state = ? OR (state = ? AND (next_attempt_at = '' OR next_attempt_at <= ?))
		)
		ORDER BY created_at ASC
		LIMIT 1
	`, printerID, deploymentID, string(StateAccepted), string(StateFailedRetryable), now)

	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Commit()
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	nextSeq := job.Sequence + 1
	nextAttempts := job.Attempts + 1
	result, err := tx.ExecContext(ctx, `
		UPDATE edge_jobs
		SET state = ?, attempts = ?, sequence = ?, updated_at = ?, next_attempt_at = ''
		WHERE id = ? AND (
			state = ? OR (state = ? AND (next_attempt_at = '' OR next_attempt_at <= ?))
		)
	`, string(StateDeviceOpen), nextAttempts, nextSeq, now, job.ID, string(StateAccepted), string(StateFailedRetryable), now)
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return nil, ErrJobStateChanged
	}

	if err := insertJobEvent(ctx, tx, job.ID, nextSeq, "job_state_changed", now, map[string]any{
		"state":      string(StateDeviceOpen),
		"jobKey":     job.JobKey,
		"printerId":  job.PrinterID,
		"agentJobId": job.ID,
		"attempt":    nextAttempts,
		"reason":     nil,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	job.State = StateDeviceOpen
	job.Attempts = nextAttempts
	job.Sequence = nextSeq
	job.UpdatedAt = now
	return job, nil
}

func (s *Store) MarkTransmitting(ctx context.Context, jobID string) error {
	return s.transitionJob(ctx, jobID, StateTransmitting, "", "")
}

func (s *Store) MarkSent(ctx context.Context, jobID string) error {
	return s.transitionJob(ctx, jobID, StateSent, "", "")
}

func (s *Store) MarkFailedRetryable(ctx context.Context, jobID string, reason string, nextAttemptAt time.Time) error {
	return s.transitionJob(ctx, jobID, StateFailedRetryable, reason, nextAttemptAt.UTC().Format(time.RFC3339Nano))
}

func (s *Store) MarkOutcomeUnknown(ctx context.Context, jobID string, reason string) error {
	return s.transitionJob(ctx, jobID, StateOutcomeUnknown, reason, "")
}

func (s *Store) transitionJob(ctx context.Context, jobID string, nextState JobState, reason string, nextAttemptAt string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	job, err := getJobTx(ctx, tx, jobID)
	if err != nil {
		return err
	}
	validFrom := job.State == StateTransmitting
	if nextState == StateTransmitting {
		validFrom = job.State == StateDeviceOpen
	} else if nextState == StateFailedRetryable {
		validFrom = job.State == StateDeviceOpen || job.State == StateTransmitting
	}
	if !validFrom {
		return fmt.Errorf("%w: %s -> %s", ErrJobStateChanged, job.State, nextState)
	}

	now := nowISO()
	nextSeq := job.Sequence + 1
	result, err := tx.ExecContext(ctx, `
		UPDATE edge_jobs
		SET state = ?, sequence = ?, last_error = ?, updated_at = ?, next_attempt_at = ?
		WHERE id = ? AND state = ?
	`, string(nextState), nextSeq, reason, now, nextAttemptAt, job.ID, string(job.State))
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrJobStateChanged
	}
	var reasonVal any
	if reason != "" {
		reasonVal = reason
	}
	var nextAttemptVal any
	if nextAttemptAt != "" {
		nextAttemptVal = nextAttemptAt
	}
	if err := insertJobEvent(ctx, tx, job.ID, nextSeq, "job_state_changed", now, map[string]any{
		"state":         string(nextState),
		"jobKey":        job.JobKey,
		"printerId":     job.PrinterID,
		"agentJobId":    job.ID,
		"reason":        reasonVal,
		"nextAttemptAt": nextAttemptVal,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordTestPrintResult(ctx context.Context, jobID, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := getJobTx(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if job.State != StateSent && job.State != StateOutcomeUnknown && job.State != StateFailedFinal && job.State != StateCancelled {
		return tx.Commit()
	}
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return err
	}
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT kind FROM edge_commands WHERE command_id = ? AND deployment_id = ?`, job.CommandID, deploymentID).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	eventKind := ""
	switch kind {
	case "test_print":
		eventKind = "test_print_result"
	case "device_action":
		eventKind = "device_action_result"
	default:
		return tx.Commit()
	}
	now := nowISO()
	nextSeq := job.Sequence + 1
	if _, err := tx.ExecContext(ctx, `UPDATE edge_jobs SET sequence = ?, updated_at = ? WHERE id = ?`, nextSeq, now, job.ID); err != nil {
		return err
	}
	var reasonVal any
	if reason != "" {
		reasonVal = reason
	}
	if err := insertJobEvent(ctx, tx, job.ID, nextSeq, eventKind, now, map[string]any{
		"state":     string(job.State),
		"jobKey":    job.JobKey,
		"commandId": job.CommandID,
		"reason":    reasonVal,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

type PrinterEndpointEvent struct {
	PrinterID  string
	Host       string
	Port       int
	MACAddress string
	Model      string
	VerifiedAt time.Time
}

func (s *Store) RecordPrinterEndpointVerified(ctx context.Context, event PrinterEndpointEvent) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return err
	}
	aggregateID := "printer:" + event.PrinterID
	var sequence int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM edge_outbox WHERE deployment_id = ? AND aggregate_id = ?`, deploymentID, aggregateID).Scan(&sequence); err != nil {
		return err
	}
	occurredAt := event.VerifiedAt.UTC().Format(time.RFC3339Nano)
	if err := insertJobEvent(ctx, tx, aggregateID, sequence, "printer_endpoint_verified", occurredAt, map[string]any{
		"printerId":  event.PrinterID,
		"host":       event.Host,
		"port":       event.Port,
		"macAddress": event.MACAddress,
		"model":      event.Model,
		"verifiedAt": occurredAt,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecoverAfterCrash(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := nowISO()

	// 1. Move device_open -> accepted (zero bytes were sent to printer).
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return err
	}
	rowsOpen, err := tx.QueryContext(ctx, `
		SELECT id, job_key, printer_id, sequence
		FROM edge_jobs
		WHERE state = ? AND deployment_id = ?
	`, string(StateDeviceOpen), deploymentID)
	if err != nil {
		return err
	}
	type openItem struct {
		id, jobKey, printerID string
		seq                   int
	}
	var openItems []openItem
	for rowsOpen.Next() {
		var it openItem
		if err := rowsOpen.Scan(&it.id, &it.jobKey, &it.printerID, &it.seq); err != nil {
			_ = rowsOpen.Close()
			return err
		}
		openItems = append(openItems, it)
	}
	_ = rowsOpen.Close()

	for _, it := range openItems {
		nextSeq := it.seq + 1
		if _, err := tx.ExecContext(ctx, `
			UPDATE edge_jobs
			SET state = ?, sequence = ?, updated_at = ?
			WHERE id = ?
		`, string(StateAccepted), nextSeq, now, it.id); err != nil {
			return err
		}
		if err := insertJobEvent(ctx, tx, it.id, nextSeq, "job_state_changed", now, map[string]any{
			"state":      string(StateAccepted),
			"jobKey":     it.jobKey,
			"printerId":  it.printerID,
			"agentJobId": it.id,
			"reason":     "recovered_before_transmit",
		}); err != nil {
			return err
		}
	}

	// 2. Move transmitting -> outcome_unknown (bytes may have reached printer).
	rowsTx, err := tx.QueryContext(ctx, `
		SELECT id, job_key, printer_id, sequence
		FROM edge_jobs
		WHERE state = ? AND deployment_id = ?
	`, string(StateTransmitting), deploymentID)
	if err != nil {
		return err
	}
	var txItems []openItem
	for rowsTx.Next() {
		var it openItem
		if err := rowsTx.Scan(&it.id, &it.jobKey, &it.printerID, &it.seq); err != nil {
			_ = rowsTx.Close()
			return err
		}
		txItems = append(txItems, it)
	}
	_ = rowsTx.Close()

	for _, it := range txItems {
		nextSeq := it.seq + 1
		if _, err := tx.ExecContext(ctx, `
			UPDATE edge_jobs
			SET state = ?, sequence = ?, last_error = ?, updated_at = ?
			WHERE id = ?
		`, string(StateOutcomeUnknown), nextSeq, "process_interrupted", now, it.id); err != nil {
			return err
		}
		if err := insertJobEvent(ctx, tx, it.id, nextSeq, "job_state_changed", now, map[string]any{
			"state":      string(StateOutcomeUnknown),
			"jobKey":     it.jobKey,
			"printerId":  it.printerID,
			"agentJobId": it.id,
			"reason":     "process_interrupted",
		}); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) CreateOfflineReprint(ctx context.Context, sourceJobID string, username string) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	src, err := getJobTx(ctx, tx, sourceJobID)
	if err != nil {
		return nil, err
	}

	switch src.State {
	case StateSent, StateOutcomeUnknown, StateFailedFinal, StateCancelled:
	default:
		return nil, fmt.Errorf("%w: job state %s is active", ErrJobNotReprintable, src.State)
	}
	if strings.TrimSpace(src.TSPL) == "" {
		return nil, fmt.Errorf("%w: job has no frozen TSPL payload", ErrJobNotReprintable)
	}
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	var rawConfig string
	err = tx.QueryRowContext(ctx, `SELECT config_json FROM edge_config_state WHERE id = 1 AND deployment_id = ?`, deploymentID).Scan(&rawConfig)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: destination is not present in applied config", ErrJobNotReprintable)
	}
	if err != nil {
		return nil, err
	}
	var config struct {
		Printers []struct {
			PrinterID string `json:"printerId"`
		} `json:"printers"`
	}
	if err := json.Unmarshal([]byte(rawConfig), &config); err != nil {
		return nil, fmt.Errorf("%w: applied config is invalid", ErrJobNotReprintable)
	}
	destinationFound := false
	for _, target := range config.Printers {
		if target.PrinterID == src.PrinterID {
			destinationFound = true
			break
		}
	}
	if !destinationFound {
		return nil, fmt.Errorf("%w: destination is not present in applied config", ErrJobNotReprintable)
	}

	now := nowISO()
	newID := newUUIDv4()
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	newJobKey := fmt.Sprintf("%s:offline-reprint:%s", src.JobKey, hex.EncodeToString(suffix[:]))

	_, err = tx.ExecContext(ctx, `
		INSERT INTO edge_jobs (
			id, deployment_id, command_id, job_key, payload_hash, printer_id, tspl, copies,
			state, attempts, sequence, last_error, source_job_id, reprint_by_user, created_at, updated_at
		) VALUES (?, ?, '', ?, ?, ?, ?, ?, ?, 0, 1, '', ?, ?, ?, ?)
	`, newID, deploymentID, newJobKey, src.PayloadHash, src.PrinterID, src.TSPL, src.Copies, string(StateAccepted), src.ID, username, now, now)
	if err != nil {
		return nil, err
	}

	if err := insertJobEvent(ctx, tx, newID, 1, "offline_reprint", now, map[string]any{
		"state":        string(StateAccepted),
		"agentJobId":   newID,
		"jobKey":       newJobKey,
		"sourceJobKey": src.JobKey,
		"printerId":    src.PrinterID,
		"operator":     username,
		"reason":       "offline_reprint",
		"copies":       src.Copies,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetJob(ctx, newID)
}

func (s *Store) GetJob(ctx context.Context, jobID string) (*Job, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, command_id, job_key, payload_hash, printer_id, tspl, copies,
		       state, attempts, sequence, last_error, source_job_id, reprint_by_user, created_at, updated_at
		FROM edge_jobs
		WHERE id = ?
	`, jobID)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	return job, err
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items, _, err := s.listJobs(ctx, limit, 0, false)
	return items, err
}

// ListJobsPage returns only the active deployment's history for fallback use.
func (s *Store) ListJobsPage(ctx context.Context, limit, offset int) ([]Job, int, error) {
	return s.listJobs(ctx, limit, offset, true)
}

func (s *Store) listJobs(ctx context.Context, limit, offset int, activeOnly bool) ([]Job, int, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	deploymentID := ""
	var err error
	if activeOnly {
		deploymentID, err = currentDeployment(ctx, s.db)
		if err != nil {
			return nil, 0, err
		}
	}
	where := ""
	args := []any{}
	if activeOnly {
		where = " WHERE deployment_id = ?"
		args = append(args, deploymentID)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edge_jobs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `
		SELECT id, command_id, job_key, payload_hash, printer_id, tspl, copies,
		       state, attempts, sequence, last_error, source_job_id, reprint_by_user, created_at, updated_at
		FROM edge_jobs` + where + `
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		var j Job
		var stateStr string
		if err := rows.Scan(
			&j.ID, &j.CommandID, &j.JobKey, &j.PayloadHash, &j.PrinterID, &j.TSPL, &j.Copies,
			&stateStr, &j.Attempts, &j.Sequence, &j.LastError, &j.SourceJobID, &j.ReprintByUser, &j.CreatedAt, &j.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		j.State = JobState(stateStr)
		out = append(out, j)
	}
	return out, total, rows.Err()
}

func (s *Store) ListPendingOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	deploymentID, err := currentDeployment(ctx, s.db)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, deployment_id, aggregate_id, sequence, kind, occurred_at, payload_json,
		       created_at, next_attempt_at, attempt_count
		FROM edge_outbox
		WHERE acked_at = '' AND deployment_id = ? AND (next_attempt_at = '' OR next_attempt_at <= ?)
		ORDER BY rowid ASC
		LIMIT ?
	`, deploymentID, nowISO(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var ev OutboxEvent
		var rawPayload string
		if err := rows.Scan(&ev.EventID, &ev.DeploymentID, &ev.AggregateID, &ev.Sequence, &ev.Kind, &ev.OccurredAt, &rawPayload, &ev.CreatedAt, &ev.NextAttemptAt, &ev.AttemptCount); err != nil {
			return nil, err
		}
		ev.Payload = map[string]any{}
		_ = json.Unmarshal([]byte(rawPayload), &ev.Payload)
		events = append(events, ev)
	}
	return events, rows.Err()
}

func (s *Store) ScheduleOutboxRetry(ctx context.Context, eventIDs []string, nextAttemptAt time.Time) error {
	if len(eventIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return err
	}
	next := nextAttemptAt.UTC().Format(time.RFC3339Nano)
	for _, id := range eventIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE edge_outbox
			SET attempt_count = attempt_count + 1, next_attempt_at = ?
			WHERE event_id = ? AND deployment_id = ? AND acked_at = ''
		`, next, id, deploymentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AckOutboxEvents(ctx context.Context, eventIDs []string) error {
	if len(eventIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return err
	}

	now := nowISO()
	for _, id := range eventIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE edge_outbox
			SET acked_at = ?
			WHERE event_id = ? AND deployment_id = ?
		`, now, id, deploymentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertCommandRow(ctx context.Context, tx *sql.Tx, commandID, kind, jobKey, payloadHash string, res CommandResolution, now string) error {
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO edge_commands (
			command_id, deployment_id, kind, job_key, payload_hash, outcome, resolution, agent_job_id, detail, resolved_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, commandID, deploymentID, kind, jobKey, payloadHash, res.Outcome, res.Resolution, res.AgentJobID, res.Detail, now)
	if err != nil {
		return err
	}
	return insertJobEvent(ctx, tx, commandID, 1, "command_resolved", now, map[string]any{
		"commandId":  commandID,
		"kind":       kind,
		"jobKey":     jobKey,
		"outcome":    res.Outcome,
		"resolution": res.Resolution,
		"agentJobId": res.AgentJobID,
		"detail":     res.Detail,
	})
}

func insertJobEvent(ctx context.Context, tx *sql.Tx, aggregateID string, sequence int, kind string, occurredAt string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO edge_outbox (
			event_id, deployment_id, aggregate_id, sequence, kind, occurred_at, payload_json,
			created_at, next_attempt_at, attempt_count, acked_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '')
	`, newUUIDv4(), deploymentID, aggregateID, sequence, kind, occurredAt, string(raw), occurredAt, occurredAt)
	return err
}

func getJobTx(ctx context.Context, tx *sql.Tx, jobID string) (*Job, error) {
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx, `
		SELECT id, command_id, job_key, payload_hash, printer_id, tspl, copies,
		       state, attempts, sequence, last_error, source_job_id, reprint_by_user, created_at, updated_at
		FROM edge_jobs
		WHERE id = ? AND deployment_id = ?
	`, jobID, deploymentID)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	return job, err
}

func scanJob(row *sql.Row) (*Job, error) {
	var j Job
	var stateStr string
	if err := row.Scan(
		&j.ID, &j.CommandID, &j.JobKey, &j.PayloadHash, &j.PrinterID, &j.TSPL, &j.Copies,
		&stateStr, &j.Attempts, &j.Sequence, &j.LastError, &j.SourceJobID, &j.ReprintByUser, &j.CreatedAt, &j.UpdatedAt,
	); err != nil {
		return nil, err
	}
	j.State = JobState(stateStr)
	return &j, nil
}
