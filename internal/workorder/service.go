package workorder

import (
	"context"
	"portcoord/internal/apperr"
	"portcoord/internal/audit"
	"portcoord/internal/domain"
	"portcoord/internal/store"
)

// Service orchestrates work-order lifecycle and status transitions.
type Service struct {
	orders store.WorkOrderRepo
	audit  *audit.Recorder
	clock  apperr.Clock
	logger *apperr.Logger
	sm     *domain.StateMachine
}

// Deps bundles the dependencies for the work-order Service.
type Deps struct {
	Orders store.WorkOrderRepo
	Audit  *audit.Recorder
	Clock  apperr.Clock
	Logger *apperr.Logger
}

// New creates a work-order Service.
func New(deps Deps) *Service {
	return &Service{
		orders: deps.Orders,
		audit:  deps.Audit,
		clock:  deps.Clock,
		logger: deps.Logger,
		sm:     domain.WorkOrderStateMachine(),
	}
}

// CreateRequest defines the input for creating a work order.
type CreateRequest struct {
	DeclarationID    string
	BerthingWindowID string
	OrderType        domain.WorkOrderType
	CargoType        string
	PlannedVolume    int
	AssignedTo       string
	RequestID        string
}

// Create generates a new work order with the created state.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*domain.WorkOrder, error) {
	if err := validateCreate(req); err != nil {
		return nil, err
	}
	now := s.clock.Now()
	wo := &domain.WorkOrder{
		ID:               newUUID(),
		DeclarationID:    req.DeclarationID,
		BerthingWindowID: req.BerthingWindowID,
		OrderType:        req.OrderType,
		CargoType:        req.CargoType,
		PlannedVolume:    req.PlannedVolume,
		AssignedTo:       req.AssignedTo,
		Status:           domain.WOStatusCreated,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.orders.CreateWorkOrder(ctx, wo); err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "create work order failed", err)
	}
	_ = s.audit.Record(ctx, audit.Entry{
		Actor:      "system",
		Action:     "create_work_order",
		EntityType: domain.EntityWorkOrder,
		EntityID:   wo.ID,
		After:      wo,
		RequestID:  req.RequestID,
	})
	return wo, nil
}

// Get retrieves a single work order by ID.
func (s *Service) Get(ctx context.Context, id string) (*domain.WorkOrder, error) {
	wo, err := s.orders.GetWorkOrder(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return nil, apperr.NotFound("work_order", id)
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "get work order failed", err)
	}
	return wo, nil
}

// List returns a paginated list of work orders.
func (s *Service) List(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.WorkOrder], error) {
	if err := q.Validate(100); err != nil {
		return domain.PageResult[*domain.WorkOrder]{}, apperr.ValidationFailed(err.Error())
	}
	return s.orders.ListWorkOrders(ctx, q)
}

// Assign transitions a work order from created to assigned.
func (s *Service) Assign(ctx context.Context, id, assignee, actor, requestID string) error {
	wo, err := s.orders.GetWorkOrder(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("work_order", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get work order failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(wo.Status), string(domain.WOStatusAssigned))
	if err != nil {
		return apperr.InvalidTransition("work_order", string(wo.Status), string(domain.WOStatusAssigned))
	}
	affected, err := s.orders.UpdateWorkOrderStatus(ctx, id, domain.WorkOrderStatus(newStatus), wo.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "assign work order failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("work_order", id, wo.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "assign_work_order", id,
		domain.EntityWorkOrder, string(wo.Status), newStatus, requestID)
	return nil
}

// StartProgress transitions a work order from assigned to in_progress.
func (s *Service) StartProgress(ctx context.Context, id, actor, requestID string) error {
	wo, err := s.orders.GetWorkOrder(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("work_order", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get work order failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(wo.Status), string(domain.WOStatusInProgress))
	if err != nil {
		return apperr.InvalidTransition("work_order", string(wo.Status), string(domain.WOStatusInProgress))
	}
	affected, err := s.orders.UpdateWorkOrderStatus(ctx, id, domain.WorkOrderStatus(newStatus), wo.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "start progress failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("work_order", id, wo.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "start_work_order", id,
		domain.EntityWorkOrder, string(wo.Status), newStatus, requestID)
	return nil
}

// CompleteRequest carries the data for completing a work order.
type CompleteRequest struct {
	ID           string
	ActualVolume int
	Actor        string
	RequestID    string
}

// Complete performs a multi-step write: update actual volume, then transition
// status to completed. If the status update fails (e.g., version conflict),
// the caller can retry the whole operation.
func (s *Service) Complete(ctx context.Context, req CompleteRequest) error {
	wo, err := s.orders.GetWorkOrder(ctx, id(req.ID))
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("work_order", req.ID)
		}
		return apperr.Wrap(apperr.CodeInternal, "get work order failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(wo.Status), string(domain.WOStatusCompleted))
	if err != nil {
		return apperr.InvalidTransition("work_order", string(wo.Status), string(domain.WOStatusCompleted))
	}
	// Step 1: update actual volume (optimistic lock on current version).
	if req.ActualVolume > 0 {
		affected, err := s.orders.UpdateActualVolume(ctx, req.ID, req.ActualVolume, wo.Version)
		if err != nil {
			return apperr.Wrap(apperr.CodeInternal, "update volume failed", err)
		}
		if affected == 0 {
			return apperr.Conflict("work_order", req.ID, wo.Version)
		}
		wo.Version++
	}
	// Step 2: transition status (optimistic lock on incremented version).
	affected, err := s.orders.UpdateWorkOrderStatus(ctx, req.ID, domain.WorkOrderStatus(newStatus), wo.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "complete work order failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("work_order", req.ID, wo.Version)
	}
	_ = s.audit.RecordTransition(ctx, req.Actor, "complete_work_order", req.ID,
		domain.EntityWorkOrder, string(wo.Status), newStatus, req.RequestID)
	return nil
}

// Cancel transitions a work order to cancelled if the state machine allows it.
func (s *Service) Cancel(ctx context.Context, id, actor, requestID string) error {
	wo, err := s.orders.GetWorkOrder(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("work_order", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get work order failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(wo.Status), string(domain.WOStatusCancelled))
	if err != nil {
		return apperr.InvalidTransition("work_order", string(wo.Status), string(domain.WOStatusCancelled))
	}
	affected, err := s.orders.UpdateWorkOrderStatus(ctx, id, domain.WorkOrderStatus(newStatus), wo.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "cancel work order failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("work_order", id, wo.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "cancel_work_order", id,
		domain.EntityWorkOrder, string(wo.Status), newStatus, requestID)
	return nil
}

// BacklogSummary reports work-order counts by status.
type BacklogSummary struct {
	Created    int
	Assigned   int
	InProgress int
	Completed  int
	Cancelled  int
	Failed     int
}

// Backlog returns work-order counts by status.
func (s *Service) Backlog(ctx context.Context) (*BacklogSummary, error) {
	b := &BacklogSummary{}
	var err error
	b.Created, err = s.orders.CountWorkOrdersByStatus(ctx, domain.WOStatusCreated)
	if err != nil {
		return nil, err
	}
	b.Assigned, err = s.orders.CountWorkOrdersByStatus(ctx, domain.WOStatusAssigned)
	if err != nil {
		return nil, err
	}
	b.InProgress, err = s.orders.CountWorkOrdersByStatus(ctx, domain.WOStatusInProgress)
	if err != nil {
		return nil, err
	}
	b.Completed, err = s.orders.CountWorkOrdersByStatus(ctx, domain.WOStatusCompleted)
	if err != nil {
		return nil, err
	}
	b.Cancelled, err = s.orders.CountWorkOrdersByStatus(ctx, domain.WOStatusCancelled)
	if err != nil {
		return nil, err
	}
	b.Failed, err = s.orders.CountWorkOrdersByStatus(ctx, domain.WOStatusFailed)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ListByStatus returns all work orders in a given status.
func (s *Service) ListByStatus(ctx context.Context, status domain.WorkOrderStatus) ([]*domain.WorkOrder, error) {
	return s.orders.ListWorkOrdersByStatus(ctx, status)
}
func validateCreate(req CreateRequest) error {
	if req.DeclarationID == "" {
		return apperr.ValidationFailed("declaration_id is required")
	}
	if req.OrderType == "" {
		return apperr.ValidationFailed("order_type is required")
	}
	if req.PlannedVolume < 0 {
		return apperr.ValidationFailed("planned_volume must be non-negative")
	}
	return nil
}
