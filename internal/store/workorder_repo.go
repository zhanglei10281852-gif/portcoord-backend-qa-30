package store

import (
	"context"
	"database/sql"
	"fmt"

	"portcoord/internal/domain"
)

func (s *SQLiteStore) CreateWorkOrder(ctx context.Context, w *domain.WorkOrder) error {
	ex := s.executor(ctx)
	_, err := ex.Exec(`
		INSERT INTO work_orders
			(id, declaration_id, berthing_window_id, order_type, cargo_type, planned_volume,
			 actual_volume, assigned_to, status, started_at, completed_at, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ID, w.DeclarationID, nullString(w.BerthingWindowID), string(w.OrderType),
		w.CargoType, w.PlannedVolume, w.ActualVolume, w.AssignedTo, string(w.Status),
		nullTime(w.StartedAt), nullTime(w.CompletedAt),
		w.Version, w.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), w.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	)
	if err != nil {
		return fmt.Errorf("insert work order: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetWorkOrder(ctx context.Context, id string) (*domain.WorkOrder, error) {
	ex := s.executor(ctx)
	row := ex.QueryRow(`SELECT id, declaration_id, berthing_window_id, order_type, cargo_type, planned_volume,
		actual_volume, assigned_to, status, started_at, completed_at, version, created_at, updated_at
		FROM work_orders WHERE id = ?`, id)
	return scanWorkOrder(row)
}

func scanWorkOrder(sc scanner) (*domain.WorkOrder, error) {
	w := &domain.WorkOrder{}
	var bwID sql.NullString
	var startedAt, completedAt sql.NullString
	var createdAt, updatedAt, typeStr, statusStr string
	err := sc.Scan(
		&w.ID, &w.DeclarationID, &bwID, &typeStr, &w.CargoType, &w.PlannedVolume,
		&w.ActualVolume, &w.AssignedTo, &statusStr, &startedAt, &completedAt,
		&w.Version, &createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewNotFoundError("work_order", "")
		}
		return nil, err
	}
	w.BerthingWindowID = parseNullString(bwID)
	w.StartedAt = parseNullTime(startedAt)
	w.CompletedAt = parseNullTime(completedAt)
	w.OrderType = domain.WorkOrderType(typeStr)
	w.Status = domain.WorkOrderStatus(statusStr)
	w.CreatedAt = parseTime(createdAt)
	w.UpdatedAt = parseTime(updatedAt)
	return w, nil
}

func (s *SQLiteStore) ListWorkOrders(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.WorkOrder], error) {
	ex := s.executor(ctx)
	params := PageParams{PageSize: q.PageSize, Offset: q.Offset(), Filters: q.Filter}
	query, args := buildListQuery(`SELECT id, declaration_id, berthing_window_id, order_type, cargo_type,
		planned_volume, actual_volume, assigned_to, status, started_at, completed_at, version, created_at, updated_at
		FROM work_orders`, params)
	rows, err := ex.Query(query, args...)
	if err != nil {
		return domain.PageResult[*domain.WorkOrder]{}, fmt.Errorf("list work orders: %w", err)
	}
	defer rows.Close()
	var items []*domain.WorkOrder
	for rows.Next() {
		w, err := scanWorkOrder(rows)
		if err != nil {
			return domain.PageResult[*domain.WorkOrder]{}, err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return domain.PageResult[*domain.WorkOrder]{}, err
	}
	total, err := s.countFiltered(ctx, "work_orders", q.Filter)
	if err != nil {
		return domain.PageResult[*domain.WorkOrder]{}, err
	}
	return domain.NewPageResult(items, total, q.Page, q.PageSize), nil
}

func (s *SQLiteStore) UpdateWorkOrderStatus(ctx context.Context, id string, status domain.WorkOrderStatus, version int) (int, error) {
	ex := s.executor(ctx)
	var startedAtExpr, completedAtExpr string
	var args []any
	args = append(args, string(status))
	if status == domain.WOStatusInProgress {
		startedAtExpr = ", started_at = ?"
		args = append(args, nowStamp())
	}
	if status == domain.WOStatusCompleted {
		completedAtExpr = ", completed_at = ?"
		args = append(args, nowStamp())
	}
	args = append(args, nowStamp(), id, version)
	query := fmt.Sprintf(`UPDATE work_orders SET status = ?%s%s, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, startedAtExpr, completedAtExpr)
	res, err := ex.Exec(query, args...)
	if err != nil {
		return 0, fmt.Errorf("update work order status: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) UpdateActualVolume(ctx context.Context, id string, volume, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE work_orders SET actual_volume = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, volume, nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("update actual volume: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) ListWorkOrdersByStatus(ctx context.Context, status domain.WorkOrderStatus) ([]*domain.WorkOrder, error) {
	ex := s.executor(ctx)
	rows, err := ex.Query(`SELECT id, declaration_id, berthing_window_id, order_type, cargo_type,
		planned_volume, actual_volume, assigned_to, status, started_at, completed_at, version, created_at, updated_at
		FROM work_orders WHERE status = ? ORDER BY created_at ASC`, string(status))
	if err != nil {
		return nil, fmt.Errorf("list work orders by status: %w", err)
	}
	defer rows.Close()
	var items []*domain.WorkOrder
	for rows.Next() {
		w, err := scanWorkOrder(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, w)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) CountWorkOrdersByStatus(ctx context.Context, status domain.WorkOrderStatus) (int, error) {
	ex := s.executor(ctx)
	var n int
	err := ex.QueryRow("SELECT COUNT(*) FROM work_orders WHERE status = ?", string(status)).Scan(&n)
	return n, err
}
