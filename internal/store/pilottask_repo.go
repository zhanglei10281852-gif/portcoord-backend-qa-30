package store

import (
	"context"
	"database/sql"
	"fmt"

	"portcoord/internal/domain"
)

func (s *SQLiteStore) CreatePilotTask(ctx context.Context, t *domain.PilotTugTask) error {
	ex := s.executor(ctx)
	_, err := ex.Exec(`
		INSERT INTO pilot_tug_tasks
			(id, declaration_id, berthing_window_id, task_type, location, assigned_to,
			 claimed_by, claim_expires_at, lease_id, status, priority, report_data,
			 version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.DeclarationID, nullString(t.BerthingWindowID),
		string(t.TaskType), t.Location, t.AssignedTo,
		t.ClaimedBy, nullTime(t.ClaimExpiresAt), nullString(t.LeaseID),
		string(t.Status), t.Priority, t.ReportData,
		t.Version, t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), t.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	)
	if err != nil {
		return fmt.Errorf("insert pilot task: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetPilotTask(ctx context.Context, id string) (*domain.PilotTugTask, error) {
	ex := s.executor(ctx)
	row := ex.QueryRow(`SELECT id, declaration_id, berthing_window_id, task_type, location, assigned_to,
		claimed_by, claim_expires_at, lease_id, status, priority, report_data, version, created_at, updated_at
		FROM pilot_tug_tasks WHERE id = ?`, id)
	return scanPilotTask(row)
}

func scanPilotTask(sc scanner) (*domain.PilotTugTask, error) {
	t := &domain.PilotTugTask{}
	var bwID, claimExpires, leaseID sql.NullString
	var createdAt, updatedAt, typeStr, statusStr string
	err := sc.Scan(
		&t.ID, &t.DeclarationID, &bwID, &typeStr, &t.Location, &t.AssignedTo,
		&t.ClaimedBy, &claimExpires, &leaseID, &statusStr, &t.Priority, &t.ReportData,
		&t.Version, &createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewNotFoundError("pilot_task", "")
		}
		return nil, err
	}
	t.BerthingWindowID = parseNullString(bwID)
	t.ClaimExpiresAt = parseNullTime(claimExpires)
	t.LeaseID = parseNullString(leaseID)
	t.TaskType = domain.PilotTaskType(typeStr)
	t.Status = domain.PilotTaskStatus(statusStr)
	t.CreatedAt = parseTime(createdAt)
	t.UpdatedAt = parseTime(updatedAt)
	return t, nil
}

func (s *SQLiteStore) ListPilotTasks(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.PilotTugTask], error) {
	ex := s.executor(ctx)
	params := PageParams{PageSize: q.PageSize, Offset: q.Offset(), Filters: q.Filter}
	query, args := buildListQuery(`SELECT id, declaration_id, berthing_window_id, task_type, location, assigned_to,
		claimed_by, claim_expires_at, lease_id, status, priority, report_data, version, created_at, updated_at
		FROM pilot_tug_tasks`, params)
	rows, err := ex.Query(query, args...)
	if err != nil {
		return domain.PageResult[*domain.PilotTugTask]{}, fmt.Errorf("list pilot tasks: %w", err)
	}
	defer rows.Close()
	var items []*domain.PilotTugTask
	for rows.Next() {
		t, err := scanPilotTask(rows)
		if err != nil {
			return domain.PageResult[*domain.PilotTugTask]{}, err
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return domain.PageResult[*domain.PilotTugTask]{}, err
	}
	total, err := s.countFiltered(ctx, "pilot_tug_tasks", q.Filter)
	if err != nil {
		return domain.PageResult[*domain.PilotTugTask]{}, err
	}
	return domain.NewPageResult(items, total, q.Page, q.PageSize), nil
}

func (s *SQLiteStore) UpdatePilotTaskStatus(ctx context.Context, id string, status domain.PilotTaskStatus, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE pilot_tug_tasks SET status = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, string(status), nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("update pilot task status: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) UpdatePilotTaskClaim(ctx context.Context, id, claimedBy, leaseID, expires string, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE pilot_tug_tasks SET claimed_by = ?, lease_id = ?, claim_expires_at = ?,
		status = 'claimed', version = version + 1, updated_at = ? WHERE id = ? AND version = ?`,
		claimedBy, leaseID, expires, nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("update pilot task claim: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) ClearPilotTaskClaim(ctx context.Context, id string, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE pilot_tug_tasks SET claimed_by = '', lease_id = '', claim_expires_at = NULL,
		status = 'preempted', version = version + 1, updated_at = ? WHERE id = ? AND version = ?`,
		nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("clear pilot task claim: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) ListClaimableTasks(ctx context.Context, limit int) ([]*domain.PilotTugTask, error) {
	ex := s.executor(ctx)
	rows, err := ex.Query(`SELECT id, declaration_id, berthing_window_id, task_type, location, assigned_to,
		claimed_by, claim_expires_at, lease_id, status, priority, report_data, version, created_at, updated_at
		FROM pilot_tug_tasks WHERE status = 'assigned' ORDER BY priority ASC, created_at ASC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list claimable tasks: %w", err)
	}
	defer rows.Close()
	var items []*domain.PilotTugTask
	for rows.Next() {
		t, err := scanPilotTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) ListExpiredClaims(ctx context.Context, now string) ([]*domain.PilotTugTask, error) {
	ex := s.executor(ctx)
	rows, err := ex.Query(`SELECT id, declaration_id, berthing_window_id, task_type, location, assigned_to,
		claimed_by, claim_expires_at, lease_id, status, priority, report_data, version, created_at, updated_at
		FROM pilot_tug_tasks WHERE claim_expires_at IS NOT NULL AND claim_expires_at < ?
		AND status IN ('claimed', 'in_progress') ORDER BY claim_expires_at ASC`, now)
	if err != nil {
		return nil, fmt.Errorf("list expired claims: %w", err)
	}
	defer rows.Close()
	var items []*domain.PilotTugTask
	for rows.Next() {
		t, err := scanPilotTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) ListPilotTasksByStatus(ctx context.Context, status domain.PilotTaskStatus) ([]*domain.PilotTugTask, error) {
	ex := s.executor(ctx)
	rows, err := ex.Query(`SELECT id, declaration_id, berthing_window_id, task_type, location, assigned_to,
		claimed_by, claim_expires_at, lease_id, status, priority, report_data, version, created_at, updated_at
		FROM pilot_tug_tasks WHERE status = ? ORDER BY priority ASC, created_at ASC`, string(status))
	if err != nil {
		return nil, fmt.Errorf("list pilot tasks by status: %w", err)
	}
	defer rows.Close()
	var items []*domain.PilotTugTask
	for rows.Next() {
		t, err := scanPilotTask(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) CountPilotTasksByStatus(ctx context.Context, status domain.PilotTaskStatus) (int, error) {
	ex := s.executor(ctx)
	var n int
	err := ex.QueryRow("SELECT COUNT(*) FROM pilot_tug_tasks WHERE status = ?", string(status)).Scan(&n)
	return n, err
}
