package store

import (
	"context"
	"database/sql"
	"fmt"

	"portcoord/internal/domain"
)

func (s *SQLiteStore) CreateDeclaration(ctx context.Context, d *domain.ArrivalDeclaration) error {
	ex := s.executor(ctx)
	_, err := ex.Exec(`
		INSERT INTO arrival_declarations
			(id, ship_name, imo_number, voyage_number, eta, berth_preference,
			 cargo_type, cargo_volume, cargo_unit, declared_by, declaring_party,
			 status, priority, queue_position, idempotency_key, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.ShipName, d.IMONumber, d.VoyageNumber,
		d.ETA.Format("2006-01-02T15:04:05Z07:00"), d.BerthPreference,
		d.CargoType, d.CargoVolume, d.CargoUnit, d.DeclaredBy, string(d.DeclaringParty),
		string(d.Status), d.Priority, d.QueuePosition, d.IdempotencyKey,
		d.Version, d.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), d.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	)
	if err != nil {
		return fmt.Errorf("insert declaration: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetDeclaration(ctx context.Context, id string) (*domain.ArrivalDeclaration, error) {
	ex := s.executor(ctx)
	row := ex.QueryRow(`SELECT id, ship_name, imo_number, voyage_number, eta, berth_preference,
		cargo_type, cargo_volume, cargo_unit, declared_by, declaring_party, status, priority,
		queue_position, idempotency_key, version, created_at, updated_at
		FROM arrival_declarations WHERE id = ?`, id)
	d, err := scanDeclaration(row)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *SQLiteStore) ListDeclarations(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.ArrivalDeclaration], error) {
	ex := s.executor(ctx)
	params := PageParams{
		PageSize: q.PageSize,
		Offset:   q.Offset(),
		Filters:  q.Filter,
	}
	query, args := buildListQuery(`SELECT id, ship_name, imo_number, voyage_number, eta, berth_preference,
		cargo_type, cargo_volume, cargo_unit, declared_by, declaring_party, status, priority,
		queue_position, idempotency_key, version, created_at, updated_at
		FROM arrival_declarations`, params)
	rows, err := ex.Query(query, args...)
	if err != nil {
		return domain.PageResult[*domain.ArrivalDeclaration]{}, fmt.Errorf("list declarations: %w", err)
	}
	defer rows.Close()
	var items []*domain.ArrivalDeclaration
	for rows.Next() {
		d, err := scanDeclaration(rows)
		if err != nil {
			return domain.PageResult[*domain.ArrivalDeclaration]{}, err
		}
		items = append(items, d)
	}
	if err := rows.Err(); err != nil {
		return domain.PageResult[*domain.ArrivalDeclaration]{}, err
	}
	total, err := s.countFiltered(ctx, "arrival_declarations", q.Filter)
	if err != nil {
		return domain.PageResult[*domain.ArrivalDeclaration]{}, err
	}
	return domain.NewPageResult(items, total, q.Page, q.PageSize), nil
}

func (s *SQLiteStore) UpdateDeclarationStatus(ctx context.Context, id string, status domain.DeclarationStatus, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE arrival_declarations SET status = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`,
		string(status), nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("update declaration status: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) UpdateQueuePosition(ctx context.Context, id string, pos, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE arrival_declarations SET queue_position = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, pos, nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("update queue position: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) UpdatePriority(ctx context.Context, id string, priority, version int) (int, error) {
	ex := s.executor(ctx)
	res, err := ex.Exec(`UPDATE arrival_declarations SET priority = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND version = ?`, priority, nowStamp(), id, version)
	if err != nil {
		return 0, fmt.Errorf("update priority: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *SQLiteStore) CountDeclarationsByStatus(ctx context.Context, status domain.DeclarationStatus) (int, error) {
	ex := s.executor(ctx)
	var n int
	err := ex.QueryRow("SELECT COUNT(*) FROM arrival_declarations WHERE status = ?", string(status)).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (s *SQLiteStore) ListDeclarationsByStatus(ctx context.Context, status domain.DeclarationStatus) ([]*domain.ArrivalDeclaration, error) {
	ex := s.executor(ctx)
	rows, err := ex.Query(`SELECT id, ship_name, imo_number, voyage_number, eta, berth_preference,
		cargo_type, cargo_volume, cargo_unit, declared_by, declaring_party, status, priority,
		queue_position, idempotency_key, version, created_at, updated_at
		FROM arrival_declarations WHERE status = ? ORDER BY priority ASC, created_at ASC`, string(status))
	if err != nil {
		return nil, fmt.Errorf("list declarations by status: %w", err)
	}
	defer rows.Close()
	var items []*domain.ArrivalDeclaration
	for rows.Next() {
		d, err := scanDeclaration(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

func (s *SQLiteStore) GetDeclarationByIdempotencyKey(ctx context.Context, key string) (*domain.ArrivalDeclaration, error) {
	if key == "" {
		return nil, domain.NewNotFoundError("declaration", "")
	}
	ex := s.executor(ctx)
	row := ex.QueryRow(`SELECT id, ship_name, imo_number, voyage_number, eta, berth_preference,
		cargo_type, cargo_volume, cargo_unit, declared_by, declaring_party, status, priority,
		queue_position, idempotency_key, version, created_at, updated_at
		FROM arrival_declarations WHERE idempotency_key = ?`, key)
	d, err := scanDeclaration(row)
	if err != nil {
		return nil, err
	}
	return d, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDeclaration(sc scanner) (*domain.ArrivalDeclaration, error) {
	d := &domain.ArrivalDeclaration{}
	var eta, createdAt, updatedAt string
	var partyStr string
	var statusStr string
	err := sc.Scan(
		&d.ID, &d.ShipName, &d.IMONumber, &d.VoyageNumber, &eta, &d.BerthPreference,
		&d.CargoType, &d.CargoVolume, &d.CargoUnit, &d.DeclaredBy, &partyStr,
		&statusStr, &d.Priority, &d.QueuePosition, &d.IdempotencyKey,
		&d.Version, &createdAt, &updatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewNotFoundError("declaration", "")
		}
		return nil, err
	}
	d.ETA = parseTime(eta)
	d.DeclaringParty = domain.PartyRole(partyStr)
	d.Status = domain.DeclarationStatus(statusStr)
	d.CreatedAt = parseTime(createdAt)
	d.UpdatedAt = parseTime(updatedAt)
	return d, nil
}

func (s *SQLiteStore) countFiltered(ctx context.Context, table string, filters map[string]string) (int, error) {
	ex := s.executor(ctx)
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	var args []any
	if len(filters) > 0 {
		query += " WHERE "
		clauses := make([]string, 0, len(filters))
		for col, val := range filters {
			clauses = append(clauses, fmt.Sprintf("%s = ?", col))
			args = append(args, val)
		}
		query += joinAnd(clauses)
	}
	var n int
	err := ex.QueryRow(query, args...).Scan(&n)
	return n, err
}
