package berthing

import (
	"context"
	"fmt"
	"time"

	"portcoord/internal/apperr"
	"portcoord/internal/audit"
	"portcoord/internal/domain"
	"portcoord/internal/store"
)

// Service orchestrates berthing-window allocation, release, and escalation.
type Service struct {
	windows      store.WindowRepo
	decls        store.DeclarationRepo
	escal        store.EscalationRepo
	handover     store.HandoverRepo
	audit        *audit.Recorder
	clock        apperr.Clock
	logger       *apperr.Logger
	sm           *domain.StateMachine
	leaseTimeout time.Duration
}

// Deps bundles the dependencies for the berthing Service.
type Deps struct {
	Windows      store.WindowRepo
	Declarations store.DeclarationRepo
	Escalations  store.EscalationRepo
	Handovers    store.HandoverRepo
	Audit        *audit.Recorder
	Clock        apperr.Clock
	Logger       *apperr.Logger
	LeaseTimeout time.Duration
}

// New creates a berthing Service.
func New(deps Deps) *Service {
	return &Service{
		windows:      deps.Windows,
		decls:        deps.Declarations,
		escal:        deps.Escalations,
		handover:     deps.Handovers,
		audit:        deps.Audit,
		clock:        deps.Clock,
		logger:       deps.Logger,
		sm:           domain.WindowStateMachine(),
		leaseTimeout: deps.LeaseTimeout,
	}
}

// CreateRequest defines the input for creating a single berthing window.
type CreateRequest struct {
	DeclarationID    string
	BerthNumber      string
	ShipName         string
	EffectiveAt      time.Time
	DeadlineAt       time.Time
	ResponsibleParty domain.PartyRole
	RequestID        string
}

// Create allocates a single berthing window with effective and deadline times.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*domain.BerthingWindow, error) {
	if err := validateCreate(req); err != nil {
		return nil, err
	}
	now := s.clock.Now()
	w := &domain.BerthingWindow{
		ID:               newUUID(),
		DeclarationID:    req.DeclarationID,
		BerthNumber:      req.BerthNumber,
		ShipName:         req.ShipName,
		EffectiveAt:      req.EffectiveAt,
		DeadlineAt:       req.DeadlineAt,
		ResponsibleParty: req.ResponsibleParty,
		Status:           domain.WindowStatusAllocated,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.windows.CreateWindow(ctx, w); err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "create window failed", err)
	}
	_ = s.audit.Record(ctx, audit.Entry{
		Actor:      "system",
		Action:     "allocate_window",
		EntityType: domain.EntityBerthingWindow,
		EntityID:   w.ID,
		After:      w,
		RequestID:  req.RequestID,
	})
	return w, nil
}

// BatchItem defines one window in a batch-allocation request.
type BatchItem struct {
	DeclarationID    string
	BerthNumber      string
	ShipName         string
	EffectiveAt      time.Time
	DeadlineAt       time.Time
	ResponsibleParty domain.PartyRole
}

// BatchAllocate creates multiple berthing windows in a single transaction.
// If any window fails validation, the entire batch is rolled back.
func (s *Service) BatchAllocate(ctx context.Context, actor string, items []BatchItem, requestID string) ([]*domain.BerthingWindow, error) {
	if len(items) == 0 {
		return nil, apperr.ValidationFailed("batch must contain at least one window")
	}
	if len(items) > 50 {
		return nil, apperr.ValidationFailed("batch must not exceed 50 windows")
	}
	for _, item := range items {
		if item.BerthNumber == "" || item.ShipName == "" {
			return nil, apperr.ValidationFailed("batch item missing required fields")
		}
		if !item.DeadlineAt.After(item.EffectiveAt) {
			return nil, apperr.ValidationFailed("deadline must be after effective time")
		}
	}
	var result []*domain.BerthingWindow
	now := s.clock.Now()
	for _, item := range items {
		w := &domain.BerthingWindow{
			ID:               newUUID(),
			DeclarationID:    item.DeclarationID,
			BerthNumber:      item.BerthNumber,
			ShipName:         item.ShipName,
			EffectiveAt:      item.EffectiveAt,
			DeadlineAt:       item.DeadlineAt,
			ResponsibleParty: item.ResponsibleParty,
			Status:           domain.WindowStatusAllocated,
			Version:          1,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		result = append(result, w)
	}
	if err := s.windows.CreateWindowsBatch(ctx, result); err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "batch create failed", err)
	}
	for _, w := range result {
		_ = s.audit.Record(ctx, audit.Entry{
			Actor:      actor,
			Action:     "batch_allocate_window",
			EntityType: domain.EntityBerthingWindow,
			EntityID:   w.ID,
			After:      w,
			RequestID:  requestID,
		})
	}
	return result, nil
}

// Get retrieves a single berthing window by ID.
func (s *Service) Get(ctx context.Context, id string) (*domain.BerthingWindow, error) {
	w, err := s.windows.GetWindow(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return nil, apperr.NotFound("berthing_window", id)
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "get window failed", err)
	}
	return w, nil
}

// List returns a paginated list of berthing windows.
func (s *Service) List(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.BerthingWindow], error) {
	if err := q.Validate(100); err != nil {
		return domain.PageResult[*domain.BerthingWindow]{}, apperr.ValidationFailed(err.Error())
	}
	return s.windows.ListWindows(ctx, q)
}

// Release transitions a window to released state.
func (s *Service) Release(ctx context.Context, id, actor, requestID string) error {
	w, err := s.windows.GetWindow(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("berthing_window", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get window failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(w.Status), string(domain.WindowStatusReleased))
	if err != nil {
		return apperr.InvalidTransition("berthing_window", string(w.Status), string(domain.WindowStatusReleased))
	}
	affected, err := s.windows.UpdateWindowStatus(ctx, id, domain.WindowStatus(newStatus), w.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "release window failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("berthing_window", id, w.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "release_window", id,
		domain.EntityBerthingWindow, string(w.Status), newStatus, requestID)
	return nil
}

// ForceIntervene allows a supervisor to force a window into any legal state.
func (s *Service) ForceIntervene(ctx context.Context, id, actor string, target domain.WindowStatus, requestID string) error {
	w, err := s.windows.GetWindow(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("berthing_window", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get window failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(w.Status), string(target))
	if err != nil {
		return apperr.InvalidTransition("berthing_window", string(w.Status), string(target))
	}
	affected, err := s.windows.UpdateWindowStatus(ctx, id, domain.WindowStatus(newStatus), w.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "force intervene failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("berthing_window", id, w.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "force_intervene", id,
		domain.EntityBerthingWindow, string(w.Status), newStatus, requestID)
	return nil
}

// EscalateOverdue finds windows past their deadline and escalates responsibility.
// This implements the deadline-window + escalation-level-up business rule:
// an overdue responsible party is automatically promoted one level and a new
// responsible party takes over.
func (s *Service) EscalateOverdue(ctx context.Context) ([]*domain.EscalationResult, error) {
	now := s.clock.Now()
	nowStr := now.Format("2006-01-02T15:04:05Z07:00")
	expired, err := s.windows.ListExpiredWindows(ctx, nowStr)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "list expired windows", err)
	}
	var results []*domain.EscalationResult
	for _, w := range expired {
		result, err := s.escalateOne(ctx, w, now)
		if err != nil {
			s.logger.Error("failed to escalate window", err,
				apperr.F("window_id", w.ID))
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// escalateOne escalates a single overdue window.
func (s *Service) escalateOne(ctx context.Context, w *domain.BerthingWindow, now time.Time) (*domain.EscalationResult, error) {
	newLevel := w.EscalationLevel + 1
	newParty := escalateParty(w.ResponsibleParty)
	newAssignee := fmt.Sprintf("level-%d-assignee", newLevel)
	affected, err := s.windows.UpdateWindowAssignedTo(ctx, w.ID, newAssignee, newLevel, w.Version)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, apperr.Conflict("berthing_window", w.ID, w.Version)
	}
	rec := &domain.EscalationRecord{
		ID:         newUUID(),
		EntityType: domain.EntityBerthingWindow,
		EntityID:   w.ID,
		FromLevel:  w.EscalationLevel,
		ToLevel:    newLevel,
		Reason:     fmt.Sprintf("deadline exceeded at %s", now.Format(time.RFC3339)),
		Timestamp:  now,
	}
	_ = s.escal.InsertEscalation(ctx, rec)
	_ = s.audit.Record(ctx, audit.Entry{
		Actor:      "scheduler",
		Action:     "escalate_window",
		EntityType: domain.EntityBerthingWindow,
		EntityID:   w.ID,
		Before:     map[string]any{"level": w.EscalationLevel, "party": string(w.ResponsibleParty)},
		After:      map[string]any{"level": newLevel, "party": string(newParty)},
	})
	return &domain.EscalationResult{
		WindowID:    w.ID,
		FromLevel:   w.EscalationLevel,
		ToLevel:     newLevel,
		NewParty:    newParty,
		NewAssignee: newAssignee,
	}, nil
}

// escalateParty promotes the responsible party one level up the chain.
func escalateParty(current domain.PartyRole) domain.PartyRole {
	switch current {
	case domain.PartyShipOwner:
		return domain.PartyTerminal
	case domain.PartyTerminal:
		return domain.PartyPilotTug
	case domain.PartyPilotTug:
		return domain.PartyAuthority
	default:
		return domain.PartyAuthority
	}
}

// ActivateEffective transitions windows that have entered their effective period.
func (s *Service) ActivateEffective(ctx context.Context) (int, error) {
	now := s.clock.Now()
	allocated, err := s.windows.ListWindowsByStatus(ctx, domain.WindowStatusAllocated)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, w := range allocated {
		if !now.Before(w.EffectiveAt) && now.Before(w.DeadlineAt) {
			affected, err := s.windows.UpdateWindowStatus(ctx, w.ID, domain.WindowStatusEffective, w.Version)
			if err != nil {
				continue
			}
			if affected > 0 {
				count++
			}
		}
	}
	return count, nil
}

// BacklogSummary reports window counts by status.
type BacklogSummary struct {
	Allocated int
	Effective int
	Occupied  int
	Released  int
	Expired   int
	Escalated int
	Cancelled int
}

// Backlog returns window counts by status.
func (s *Service) Backlog(ctx context.Context) (*BacklogSummary, error) {
	b := &BacklogSummary{}
	var err error
	b.Allocated, err = s.windows.CountWindowsByStatus(ctx, domain.WindowStatusAllocated)
	if err != nil {
		return nil, err
	}
	b.Effective, err = s.windows.CountWindowsByStatus(ctx, domain.WindowStatusEffective)
	if err != nil {
		return nil, err
	}
	b.Occupied, err = s.windows.CountWindowsByStatus(ctx, domain.WindowStatusOccupied)
	if err != nil {
		return nil, err
	}
	b.Released, err = s.windows.CountWindowsByStatus(ctx, domain.WindowStatusReleased)
	if err != nil {
		return nil, err
	}
	b.Expired, err = s.windows.CountWindowsByStatus(ctx, domain.WindowStatusExpired)
	if err != nil {
		return nil, err
	}
	b.Escalated, err = s.windows.CountWindowsByStatus(ctx, domain.WindowStatusEscalated)
	if err != nil {
		return nil, err
	}
	b.Cancelled, err = s.windows.CountWindowsByStatus(ctx, domain.WindowStatusCancelled)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ListExpired returns all currently expired windows.
func (s *Service) ListExpired(ctx context.Context) ([]*domain.BerthingWindow, error) {
	nowStr := s.clock.Now().Format("2006-01-02T15:04:05Z07:00")
	return s.windows.ListExpiredWindows(ctx, nowStr)
}

func validateCreate(req CreateRequest) error {
	if req.BerthNumber == "" {
		return apperr.ValidationFailed("berth_number is required")
	}
	if req.ShipName == "" {
		return apperr.ValidationFailed("ship_name is required")
	}
	if !req.DeadlineAt.After(req.EffectiveAt) {
		return apperr.ValidationFailed("deadline_at must be after effective_at")
	}
	return nil
}
