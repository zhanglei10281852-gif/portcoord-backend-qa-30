package store

import (
	"context"
	"database/sql"
	"fmt"

	"portcoord/internal/domain"
)

func (s *SQLiteStore) windowCreate(ctx context.Context, w *domain.BerthingWindow) error {
	ex := s.executor(ctx)
	_, err := ex.Exec(`
		INSERT INTO berthing_windows
			(id, declaration_id, berth_number, ship_name, effective_at, deadline_at,
			 assigned_to, responsible_party, escalation_level, status, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.DeclarationID, w.BerthNumber, w.ShipName,
		w.EffectiveAt.Format("2006-01-02T15:04:05Z07:00"), w.DeadlineAt.Format("2006-01-02T15:04:05Z07:00"),
		w.AssignedTo, string(w.ResponsibleParty), w.EscalationLevel, string(w.Status),
		w.Version, w.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), w.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	)
	if err != nil {
		return fmt.Errorf("insert window: %w", err)
	}
	return nil
}

func (s *SQLiteStore) CreateWindow(ctx context.Context, w *domain.BerthingWindow) error {
	return s.windowCreate(ctx, w)
}

func (s *SQLiteStore) CreateWindowsBatch(ctx context.Context, ws []*domain.BerthingWindow) error {
	for _, w := range ws {
		if err := s.windowCreate(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) GetWindow(ctx context.Context, id string) (*domain.BerthingWindow, error) {
	ex := s.executor(ctx)
	row := ex.QueryRow(`SELECT id, declaration_id, berth_number, ship_name, effective_at, deadline_at,
		assigned_to, responsible_party, escalation_level, status, version, created_at, updated_at
		FROM berthing_windows WHERE id = ?`, id)
	return scanWindow(row)
}

func scanWindow(sc scanner) (*domain.BerthingWindow, error) {
	w := &domain.BerthingWindow{}
	var effectiveAt, deadlineAt, createdAt, updatedAt, partyStr, statusStr string
	err := sc.Scan(
		&w.ID, &w.DeclarationID, &w.BerthNumber, &w.ShipName,
		&effectiveAt, &deadlineAt, &w.AssignedTo, &partyStr,
		&w.EscalationLevel, &statusStr, &w.Version, &createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewNotFoundError("berthing_window", "")
		}
		return nil, err
	}
	w.EffectiveAt = parseTime(effectiveAt)
	w.DeadlineAt = parseTime(deadlineAt)
	w.ResponsibleParty = domain.PartyRole(partyStr)
	w.Status = domain.WindowStatus(statusStr)
	w.CreatedAt = parseTime(createdAt)
	w.UpdatedAt = parseTime(updatedAt)
	return w, nil
}

func (s *SQLiteStore) ListWindows(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.BerthingWindow], error) {
	ex := s.executor(ctx)
	params := PageParams{PageSize: q.PageSize, Offset: q.Offset(), Filters: q.Filter}
	query, args := buildListQuery(`SELECT id, declaration_id, berth_number, ship_name, effective_at, deadline_at,
		assigned_to, responsible_party, escalation_level, status, version, created_at, updated_at
		FROM berthing_windows`, params)
	rows, err := ex.Query(query, args...)
	if err != nil {
		return domain.PageResult[*domain.BerthingWindow]{}, fmt.Errorf("list windows: %w", err)
	}
	defer rows.Close()
	var items []*domain.BerthingWindow
	for rows.Next() {
		w, err := scanWindow(rows)
		if err != nil {
			return domain.PageResult[*domain.BerthingWindow]{}, err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return domain.PageResult[*domain.BerthingWindow]{}, err
	}
	total, err := s.countFiltered(ctx, "berthing_windows", q.Filter)
	if err != nil {
		return domain.PageResult[*domain.BerthingWindow]{}, err
	}
	return domain.NewPageResult(items, total, q.Page, q.PageSize), nil
}

func (s *SQLiteStore) UpdateWindowStatus(ctx context.Context, id string, status domain.WindowStatus, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE berthing_windows SET status = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, string(status), nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("update window status: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) UpdateWindowAssignedTo(ctx context.Context, id string, assignedTo string, level int, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE berthing_windows SET assigned_to = ?, escalation_level = ?, status = 'escalated',
		version = version + 1, updated_at = ? WHERE id = ? AND version = ?`,
		assignedTo, level, nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("escalate window: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) ListExpiredWindows(ctx context.Context, now string) ([]*domain.BerthingWindow, error) {
	ex := s.executor(ctx)
	rows, err := ex.Query(`SELECT id, declaration_id, berth_number, ship_name, effective_at, deadline_at,
		assigned_to, responsible_party, escalation_level, status, version, created_at, updated_at
		FROM berthing_windows WHERE deadline_at < ? AND status IN ('effective', 'occupied', 'escalated')
		ORDER BY deadline_at ASC`, now)
	if err != nil {
		return nil, fmt.Errorf("list expired windows: %w", err)
	}
	defer rows.Close()
	var items []*domain.BerthingWindow
	for rows.Next() {
		w, err := scanWindow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) ListWindowsByStatus(ctx context.Context, status domain.WindowStatus) ([]*domain.BerthingWindow, error) {
	ex := s.executor(ctx)
	rows, err := ex.Query(`SELECT id, declaration_id, berth_number, ship_name, effective_at, deadline_at,
		assigned_to, responsible_party, escalation_level, status, version, created_at, updated_at
		FROM berthing_windows WHERE status = ? ORDER BY deadline_at ASC`, string(status))
	if err != nil {
		return nil, fmt.Errorf("list windows by status: %w", err)
	}
	defer rows.Close()
	var items []*domain.BerthingWindow
	for rows.Next() {
		w, err := scanWindow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) CountWindowsByStatus(ctx context.Context, status domain.WindowStatus) (int, error) {
	ex := s.executor(ctx)
	var n int
	err := ex.QueryRow("SELECT COUNT(*) FROM berthing_windows WHERE status = ?", string(status)).Scan(&n)
	return n, err
}
