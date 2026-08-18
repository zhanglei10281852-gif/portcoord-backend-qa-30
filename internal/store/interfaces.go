package store

import (
	"context"

	"portcoord/internal/domain"
)

// TxRunner executes a function within a database transaction.
type TxRunner interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// DeclarationRepo manages arrival-declaration persistence.
type DeclarationRepo interface {
	CreateDeclaration(ctx context.Context, d *domain.ArrivalDeclaration) error
	GetDeclaration(ctx context.Context, id string) (*domain.ArrivalDeclaration, error)
	ListDeclarations(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.ArrivalDeclaration], error)
	UpdateDeclarationStatus(ctx context.Context, id string, status domain.DeclarationStatus, version int) (int, error)
	UpdateQueuePosition(ctx context.Context, id string, pos, version int) (int, error)
	UpdatePriority(ctx context.Context, id string, priority, version int) (int, error)
	CountDeclarationsByStatus(ctx context.Context, status domain.DeclarationStatus) (int, error)
	ListDeclarationsByStatus(ctx context.Context, status domain.DeclarationStatus) ([]*domain.ArrivalDeclaration, error)
	GetDeclarationByIdempotencyKey(ctx context.Context, key string) (*domain.ArrivalDeclaration, error)
}

// WindowRepo manages berthing-window persistence.
type WindowRepo interface {
	CreateWindow(ctx context.Context, w *domain.BerthingWindow) error
	CreateWindowsBatch(ctx context.Context, ws []*domain.BerthingWindow) error
	GetWindow(ctx context.Context, id string) (*domain.BerthingWindow, error)
	ListWindows(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.BerthingWindow], error)
	UpdateWindowStatus(ctx context.Context, id string, status domain.WindowStatus, version int) (int, error)
	UpdateWindowAssignedTo(ctx context.Context, id string, assignedTo string, level int, version int) (int, error)
	ListExpiredWindows(ctx context.Context, now string) ([]*domain.BerthingWindow, error)
	ListWindowsByStatus(ctx context.Context, status domain.WindowStatus) ([]*domain.BerthingWindow, error)
	CountWindowsByStatus(ctx context.Context, status domain.WindowStatus) (int, error)
}

// WorkOrderRepo manages work-order persistence.
type WorkOrderRepo interface {
	CreateWorkOrder(ctx context.Context, w *domain.WorkOrder) error
	GetWorkOrder(ctx context.Context, id string) (*domain.WorkOrder, error)
	ListWorkOrders(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.WorkOrder], error)
	UpdateWorkOrderStatus(ctx context.Context, id string, status domain.WorkOrderStatus, version int) (int, error)
	UpdateActualVolume(ctx context.Context, id string, volume, version int) (int, error)
	ListWorkOrdersByStatus(ctx context.Context, status domain.WorkOrderStatus) ([]*domain.WorkOrder, error)
	CountWorkOrdersByStatus(ctx context.Context, status domain.WorkOrderStatus) (int, error)
}

// PilotTaskRepo manages pilot/tug-task persistence.
type PilotTaskRepo interface {
	CreatePilotTask(ctx context.Context, t *domain.PilotTugTask) error
	GetPilotTask(ctx context.Context, id string) (*domain.PilotTugTask, error)
	ListPilotTasks(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.PilotTugTask], error)
	UpdatePilotTaskStatus(ctx context.Context, id string, status domain.PilotTaskStatus, version int) (int, error)
	UpdatePilotTaskClaim(ctx context.Context, id, claimedBy, leaseID string, expires string, version int) (int, error)
	ClearPilotTaskClaim(ctx context.Context, id string, version int) (int, error)
	ListClaimableTasks(ctx context.Context, limit int) ([]*domain.PilotTugTask, error)
	ListExpiredClaims(ctx context.Context, now string) ([]*domain.PilotTugTask, error)
	ListPilotTasksByStatus(ctx context.Context, status domain.PilotTaskStatus) ([]*domain.PilotTugTask, error)
	CountPilotTasksByStatus(ctx context.Context, status domain.PilotTaskStatus) (int, error)
}

// QuotaRepo manages quota persistence.
type QuotaRepo interface {
	GetOrCreateQuota(ctx context.Context, qt domain.QuotaType, date string, limit int) (*domain.Quota, error)
	GetQuota(ctx context.Context, id string) (*domain.Quota, error)
	GetQuotaByTypeDate(ctx context.Context, qt domain.QuotaType, date string) (*domain.Quota, error)
	ListQuotas(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.Quota], error)
	ReserveQuota(ctx context.Context, id string, amount, version int) (int, error)
	CommitQuota(ctx context.Context, id string, amount, version int) (int, error)
	ReleaseQuota(ctx context.Context, id string, amount, version int) (int, error)
	ListAllQuotas(ctx context.Context) ([]*domain.Quota, error)
}

// HandoverRepo manages handover-document persistence.
type HandoverRepo interface {
	CreateHandover(ctx context.Context, h *domain.HandoverDocument) error
	GetHandover(ctx context.Context, id string) (*domain.HandoverDocument, error)
	ListHandovers(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.HandoverDocument], error)
	UpdateHandoverStatus(ctx context.Context, id string, status domain.HandoverStatus, version int) (int, error)
	ListHandoversByEntity(ctx context.Context, entityType domain.EntityType, entityID string) ([]*domain.HandoverDocument, error)
}

// LeaseRepo manages task-lease persistence.
type LeaseRepo interface {
	CreateLease(ctx context.Context, l *domain.TaskLease) error
	GetLease(ctx context.Context, id string) (*domain.TaskLease, error)
	GetActiveLeaseByTask(ctx context.Context, taskType domain.EntityType, taskID string) (*domain.TaskLease, error)
	RevokeLease(ctx context.Context, id, reason string, version int) (int, error)
	ListExpiredLeases(ctx context.Context, now string) ([]*domain.TaskLease, error)
	ReleaseLease(ctx context.Context, id string, version int) (int, error)
}

// AuditRepo manages audit-log persistence and queries.
type AuditRepo interface {
	InsertAudit(ctx context.Context, entry *domain.AuditLog) error
	ListAuditLogs(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.AuditLog], error)
	ListAuditByEntity(ctx context.Context, entityType domain.EntityType, entityID string) ([]*domain.AuditLog, error)
}

// EscalationRepo manages escalation-record persistence.
type EscalationRepo interface {
	InsertEscalation(ctx context.Context, r *domain.EscalationRecord) error
	ListEscalations(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.EscalationRecord], error)
	ListEscalationsByEntity(ctx context.Context, entityType domain.EntityType, entityID string) ([]*domain.EscalationRecord, error)
}

// ExecutionRepo manages execution-record persistence.
type ExecutionRepo interface {
	InsertExecution(ctx context.Context, r *domain.ExecutionRecord) error
	ListExecutions(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.ExecutionRecord], error)
}

// IdempotencyRepo manages idempotency-key persistence.
type IdempotencyRepo interface {
	GetIdempotency(ctx context.Context, key string) (*domain.IdempotencyRecord, error)
	InsertIdempotency(ctx context.Context, r *domain.IdempotencyRecord) error
	CleanExpiredIdempotency(ctx context.Context, now string) (int, error)
}

// Store is the aggregate persistence interface combining all repositories.
type Store interface {
	TxRunner
	DeclarationRepo
	WindowRepo
	WorkOrderRepo
	PilotTaskRepo
	QuotaRepo
	HandoverRepo
	LeaseRepo
	AuditRepo
	EscalationRepo
	ExecutionRepo
	IdempotencyRepo
	Close() error
}
