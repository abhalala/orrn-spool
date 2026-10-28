package jobs

import (
	"context"
	"database/sql"
	"errors"
)

// ApplyConfig stores one complete desired-config version and its
// config_applied event in the same transaction. Older versions are ignored.
func (s *Store) ApplyConfig(ctx context.Context, version int, configJSON []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	deploymentID, err := currentDeploymentTx(ctx, tx)
	if err != nil {
		return err
	}
	var current int
	err = tx.QueryRowContext(ctx, `SELECT applied_config_version FROM edge_config_state WHERE id = 1 AND deployment_id = ?`, deploymentID).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if version <= current {
		return tx.Commit()
	}

	now := nowISO()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO edge_config_state (id, deployment_id, applied_config_version, config_json, updated_at)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			deployment_id = excluded.deployment_id,
			applied_config_version = excluded.applied_config_version,
			config_json = excluded.config_json,
			updated_at = excluded.updated_at
	`, deploymentID, version, string(configJSON), now); err != nil {
		return err
	}
	if err := insertJobEvent(ctx, tx, "config", version, "config_applied", now, map[string]any{
		"configVersion": version,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// LoadAppliedConfig returns the active deployment's last applied version and
// its JSON, or 0 and nil before the first config download.
func (s *Store) LoadAppliedConfig(ctx context.Context) (int, []byte, error) {
	deploymentID, err := currentDeployment(ctx, s.db)
	if err != nil {
		return 0, nil, err
	}
	var version int
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT applied_config_version, config_json FROM edge_config_state WHERE id = 1 AND deployment_id = ?`, deploymentID).Scan(&version, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	return version, []byte(raw), nil
}
