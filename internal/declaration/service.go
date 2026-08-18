package declaration

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"portcoord/internal/apperr"
	"portcoord/internal/audit"
	"portcoord/internal/domain"
	"portcoord/internal/store"
)

// Service orchestrates arrival-declaration submission, retrieval, and lifecycle.
type Service struct {
	deps       store.DeclarationRepo
	quota      store.QuotaRepo
	idem       store.IdempotencyRepo
	handover   store.HandoverRepo
	audit      *audit.Recorder
	clock      apperr.Clock
	logger     *apperr.Logger
	sm         *domain.StateMachine
	cabinLimit int
	yardLimit  int
}

// Deps bundles the dependencies for Service.
type Deps struct {
	Declarations store.DeclarationRepo
	Quotas       store.QuotaRepo
	Idempotency  store.IdempotencyRepo
	Handovers    store.HandoverRepo
	Audit        *audit.Recorder
	Clock        apperr.Clock
	Logger       *apperr.Logger
	CabinLimit   int
	YardLimit    int
}

// New creates a declaration Service.
func New(deps Deps) *Service {
	return &Service{
		deps:       deps.Declarations,
		quota:      deps.Quotas,
		idem:       deps.Idempotency,
		handover:   deps.Handovers,
		audit:      deps.Audit,
		clock:      deps.Clock,
		logger:     deps.Logger,
		sm:         domain.DeclarationStateMachine(),
		cabinLimit: deps.CabinLimit,
		yardLimit:  deps.YardLimit,
	}
}

// SubmitResult is the synchronous reply to a declaration submission.
type SubmitResult struct {
	Status        domain.DeclarationStatus `json:"status"`
	DeclarationID string                   `json:"declaration_id"`
	QueuePosition int                      `json:"queue_position"`
	WaitEstimate  time.Duration            `json:"wait_estimate"`
	QuotaStatus   string                   `json:"quota_status"`
	Message       string                   `json:"message"`
}

// SubmitRequest carries the data for a new arrival declaration.
type SubmitRequest struct {
	ShipName        string
	IMONumber       string
	VoyageNumber    string
	ETA             time.Time
	BerthPreference string
	CargoType       string
	CargoVolume     int
	CargoUnit       string
	DeclaredBy      string
	DeclaringParty  domain.PartyRole
	Priority        int
	IdempotencyKey  string
	RequestID       string
}

// Submit creates or returns an existing declaration, enforcing idempotency,
// quota backpressure, and state-machine initialization.
func (s *Service) Submit(ctx context.Context, req SubmitRequest) (*SubmitResult, error) {
	if err := validateSubmit(req); err != nil {
		return nil, err
	}

	if req.IdempotencyKey != "" {
		cached, err := s.tryIdempotency(ctx, req.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if cached != nil {
			return cached, nil
		}
	}

	now := s.clock.Now()
	dateStr := now.Format("2006-01-02")

	quotaType := domain.QuotaTypeCabin
	limit := s.cabinLimit
	if req.CargoUnit == "TEU" || req.CargoType == "container" {
		quotaType = domain.QuotaTypeYard
		limit = s.yardLimit
	}

	reserved, quotaID, err := s.reserveQuotaWithRetry(ctx, quotaType, dateStr, limit, req.CargoVolume, 25)
	if err != nil {
		return nil, err
	}
	if !reserved {
		s.logger.Warn("quota exceeded, rejecting declaration",
			apperr.F("ship", req.ShipName),
			apperr.F("requested", req.CargoVolume))
		result := &SubmitResult{
			Status:      domain.DeclStatusRejected,
			QuotaStatus: "exhausted",
			Message:     fmt.Sprintf("daily %s quota exhausted, requested %d", quotaType, req.CargoVolume),
		}
		s.cacheIdempotency(ctx, req.IdempotencyKey, result)
		return result, nil
	}

	q, err := s.quota.GetOrCreateQuota(ctx, quotaType, dateStr, limit)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "quota re-read failed", err)
	}
	utilization := q.Utilization()
	status := domain.DeclStatusAccepted
	queuePos := 0
	waitEstimate := time.Duration(0)
	if utilization > 0.8 {
		status = domain.DeclStatusQueued
		count, _ := s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusQueued)
		queuePos = count + 1
		waitEstimate = time.Duration(queuePos) * 10 * time.Minute
	}

	decl := &domain.ArrivalDeclaration{
		ID:              newUUID(),
		ShipName:        req.ShipName,
		IMONumber:       req.IMONumber,
		VoyageNumber:    req.VoyageNumber,
		ETA:             req.ETA,
		BerthPreference: req.BerthPreference,
		CargoType:       req.CargoType,
		CargoVolume:     req.CargoVolume,
		CargoUnit:       req.CargoUnit,
		DeclaredBy:      req.DeclaredBy,
		DeclaringParty:  req.DeclaringParty,
		Status:          status,
		Priority:        req.Priority,
		QueuePosition:   queuePos,
		IdempotencyKey:  req.IdempotencyKey,
		Version:         1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.deps.CreateDeclaration(ctx, decl); err != nil {
		_, _ = s.quota.ReleaseQuota(ctx, quotaID, req.CargoVolume, q.Version+1)
		return nil, apperr.Wrap(apperr.CodeInternal, "create declaration failed", err)
	}

	handover := &domain.HandoverDocument{
		ID:         newUUID(),
		EntityType: domain.EntityDeclaration,
		EntityID:   decl.ID,
		FromParty:  domain.PartyShipOwner,
		ToParty:    domain.PartyTerminal,
		Action:     "submit_declaration",
		Status:     domain.HandoverStatusPending,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.handover.CreateHandover(ctx, handover); err != nil {
		s.logger.Error("failed to create handover document", err)
	}

	_ = s.audit.Record(ctx, audit.Entry{
		Actor:      req.DeclaredBy,
		Action:     "submit_declaration",
		EntityType: domain.EntityDeclaration,
		EntityID:   decl.ID,
		After:      decl,
		RequestID:  req.RequestID,
	})

	result := &SubmitResult{
		Status:        status,
		DeclarationID: decl.ID,
		QueuePosition: queuePos,
		WaitEstimate:  waitEstimate,
		QuotaStatus:   string(quotaStatus(utilization)),
		Message:       messageForStatus(status, queuePos),
	}
	s.cacheIdempotency(ctx, req.IdempotencyKey, result)
	return result, nil
}

// Get retrieves a single declaration by ID.
func (s *Service) Get(ctx context.Context, id string) (*domain.ArrivalDeclaration, error) {
	decl, err := s.deps.GetDeclaration(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return nil, apperr.NotFound("declaration", id)
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "get declaration failed", err)
	}
	return decl, nil
}

// List returns a paginated list of declarations.
func (s *Service) List(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.ArrivalDeclaration], error) {
	if err := q.Validate(100); err != nil {
		return domain.PageResult[*domain.ArrivalDeclaration]{}, apperr.ValidationFailed(err.Error())
	}
	return s.deps.ListDeclarations(ctx, q)
}

// Cancel transitions a declaration to cancelled if the state machine allows it.
func (s *Service) Cancel(ctx context.Context, id, actor, requestID string) error {
	decl, err := s.deps.GetDeclaration(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("declaration", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get declaration failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(decl.Status), string(domain.DeclStatusCancelled))
	if err != nil {
		return apperr.InvalidTransition("declaration", string(decl.Status), string(domain.DeclStatusCancelled))
	}
	affected, err := s.deps.UpdateDeclarationStatus(ctx, id, domain.DeclarationStatus(newStatus), decl.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "update declaration failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("declaration", id, decl.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "cancel_declaration", id,
		domain.EntityDeclaration, string(decl.Status), newStatus, requestID)
	return nil
}

// UpdatePriority changes the priority of a declaration (forced intervention).
func (s *Service) UpdatePriority(ctx context.Context, id, actor string, priority int, requestID string) error {
	decl, err := s.deps.GetDeclaration(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("declaration", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get declaration failed", err)
	}
	if priority < 1 || priority > 10 {
		return apperr.ValidationFailed("priority must be 1-10")
	}
	affected, err := s.deps.UpdatePriority(ctx, id, priority, decl.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "update priority failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("declaration", id, decl.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "update_priority", id,
		domain.EntityDeclaration, fmt.Sprintf("p%d", decl.Priority), fmt.Sprintf("p%d", priority), requestID)
	return nil
}

// BacklogSummary reports counts by status for the backlog view.
type BacklogSummary struct {
	Submitted  int
	Reviewing  int
	Accepted   int
	Queued     int
	Scheduled  int
	Processing int
	Completed  int
	Rejected   int
	Cancelled  int
}

// Backlog returns a summary of declaration counts by status.
func (s *Service) Backlog(ctx context.Context) (*BacklogSummary, error) {
	b := &BacklogSummary{}
	var err error
	b.Submitted, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusSubmitted)
	if err != nil {
		return nil, err
	}
	b.Reviewing, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusReviewing)
	if err != nil {
		return nil, err
	}
	b.Accepted, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusAccepted)
	if err != nil {
		return nil, err
	}
	b.Queued, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusQueued)
	if err != nil {
		return nil, err
	}
	b.Scheduled, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusScheduled)
	if err != nil {
		return nil, err
	}
	b.Processing, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusProcessing)
	if err != nil {
		return nil, err
	}
	b.Completed, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusCompleted)
	if err != nil {
		return nil, err
	}
	b.Rejected, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusRejected)
	if err != nil {
		return nil, err
	}
	b.Cancelled, err = s.deps.CountDeclarationsByStatus(ctx, domain.DeclStatusCancelled)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ListQueued returns all declarations currently in the queue.
func (s *Service) ListQueued(ctx context.Context) ([]*domain.ArrivalDeclaration, error) {
	return s.deps.ListDeclarationsByStatus(ctx, domain.DeclStatusQueued)
}

// AdvanceQueued promotes the highest-priority queued declaration to scheduled.
func (s *Service) AdvanceQueued(ctx context.Context) (*domain.ArrivalDeclaration, error) {
	queued, err := s.deps.ListDeclarationsByStatus(ctx, domain.DeclStatusQueued)
	if err != nil {
		return nil, err
	}
	if len(queued) == 0 {
		return nil, nil
	}
	top := queued[0]
	newStatus, err := s.sm.MustTransition(string(top.Status), string(domain.DeclStatusScheduled))
	if err != nil {
		return nil, apperr.InvalidTransition("declaration", string(top.Status), string(domain.DeclStatusScheduled))
	}
	affected, err := s.deps.UpdateDeclarationStatus(ctx, top.ID, domain.DeclarationStatus(newStatus), top.Version)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "advance queued failed", err)
	}
	if affected == 0 {
		return nil, apperr.Conflict("declaration", top.ID, top.Version)
	}
	top.Status = domain.DeclStatusScheduled
	top.Version++
	return top, nil
}

func validateSubmit(req SubmitRequest) error {
	if req.ShipName == "" {
		return apperr.ValidationFailed("ship_name is required")
	}
	if req.IMONumber == "" {
		return apperr.ValidationFailed("imo_number is required")
	}
	if req.VoyageNumber == "" {
		return apperr.ValidationFailed("voyage_number is required")
	}
	if req.CargoVolume < 0 {
		return apperr.ValidationFailed("cargo_volume must be non-negative")
	}
	if req.DeclaredBy == "" {
		return apperr.ValidationFailed("declared_by is required")
	}
	if req.Priority < 1 || req.Priority > 10 {
		req.Priority = 5
	}
	return nil
}

func (s *Service) tryIdempotency(ctx context.Context, key string) (*SubmitResult, error) {
	rec, err := s.idem.GetIdempotency(ctx, key)
	if err != nil {
		if domain.IsNotFound(err) {
			return nil, nil
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "idempotency lookup failed", err)
	}
	if s.clock.Now().After(rec.ExpiresAt) {
		return nil, nil
	}
	var result SubmitResult
	if err := json.Unmarshal([]byte(rec.ResponseBody), &result); err != nil {
		return nil, nil
	}
	return &result, nil
}

func (s *Service) cacheIdempotency(ctx context.Context, key string, result *SubmitResult) {
	if key == "" {
		return
	}
	body, _ := json.Marshal(result)
	now := s.clock.Now()
	_ = s.idem.InsertIdempotency(ctx, &domain.IdempotencyRecord{
		Key:            key,
		ResponseBody:   string(body),
		ResponseStatus: 200,
		CreatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour),
	})
}

func quotaStatus(utilization float64) domain.QuotaStatus {
	if utilization >= 1.0 {
		return domain.QuotaStatusExhausted
	}
	if utilization > 0.8 {
		return domain.QuotaStatusWarning
	}
	return domain.QuotaStatusAvailable
}

func messageForStatus(status domain.DeclarationStatus, queuePos int) string {
	switch status {
	case domain.DeclStatusAccepted:
		return "declaration accepted, processing will proceed at scheduled time"
	case domain.DeclStatusQueued:
		return fmt.Sprintf("declaration queued at position %d due to high utilization", queuePos)
	case domain.DeclStatusRejected:
		return "declaration rejected: quota exceeded"
	default:
		return string(status)
	}
}
