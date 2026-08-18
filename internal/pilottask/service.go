package pilottask

import (
	"context"
	"fmt"
	"time"

	"portcoord/internal/apperr"
	"portcoord/internal/audit"
	"portcoord/internal/domain"
	"portcoord/internal/store"
)

// Service orchestrates pilot/tug-task creation, claiming, reporting, and preemption.
type Service struct {
	tasks        store.PilotTaskRepo
	leases       store.LeaseRepo
	executions   store.ExecutionRepo
	audit        *audit.Recorder
	clock        apperr.Clock
	logger       *apperr.Logger
	sm           *domain.StateMachine
	leaseTimeout time.Duration
}

// Deps bundles the dependencies for the pilot-task Service.
type Deps struct {
	Tasks        store.PilotTaskRepo
	Leases       store.LeaseRepo
	Executions   store.ExecutionRepo
	Audit        *audit.Recorder
	Clock        apperr.Clock
	Logger       *apperr.Logger
	LeaseTimeout time.Duration
}

// New creates a pilot-task Service.
func New(deps Deps) *Service {
	return &Service{
		tasks:        deps.Tasks,
		leases:       deps.Leases,
		executions:   deps.Executions,
		audit:        deps.Audit,
		clock:        deps.Clock,
		logger:       deps.Logger,
		sm:           domain.PilotTaskStateMachine(),
		leaseTimeout: deps.LeaseTimeout,
	}
}

// CreateRequest defines the input for creating a pilot/tug task.
type CreateRequest struct {
	DeclarationID    string
	BerthingWindowID string
	TaskType         domain.PilotTaskType
	Location         string
	Priority         int
	RequestID        string
}

// Create generates a new pilot/tug task in the created state.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*domain.PilotTugTask, error) {
	if err := validateCreate(req); err != nil {
		return nil, err
	}
	now := s.clock.Now()
	t := &domain.PilotTugTask{
		ID:               newUUID(),
		DeclarationID:    req.DeclarationID,
		BerthingWindowID: req.BerthingWindowID,
		TaskType:         req.TaskType,
		Location:         req.Location,
		Priority:         req.Priority,
		Status:           domain.PTStatusCreated,
		Version:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.tasks.CreatePilotTask(ctx, t); err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "create pilot task failed", err)
	}
	_ = s.audit.Record(ctx, audit.Entry{
		Actor:      "system",
		Action:     "create_pilot_task",
		EntityType: domain.EntityPilotTask,
		EntityID:   t.ID,
		After:      t,
		RequestID:  req.RequestID,
	})
	return t, nil
}

// Assign transitions a task from created to assigned, making it claimable.
func (s *Service) Assign(ctx context.Context, id, assignee, actor, requestID string) error {
	t, err := s.tasks.GetPilotTask(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("pilot_task", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get pilot task failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(t.Status), string(domain.PTStatusAssigned))
	if err != nil {
		return apperr.InvalidTransition("pilot_task", string(t.Status), string(domain.PTStatusAssigned))
	}
	affected, err := s.tasks.UpdatePilotTaskStatus(ctx, id, domain.PilotTaskStatus(newStatus), t.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "assign pilot task failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("pilot_task", id, t.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "assign_pilot_task", id,
		domain.EntityPilotTask, string(t.Status), newStatus, requestID)
	return nil
}

// ClaimRequest carries the data for an executor claiming a task.
type ClaimRequest struct {
	TaskID     string
	ExecutorID string
	RequestID  string
}

// ClaimResult holds the result of a successful claim.
type ClaimResult struct {
	TaskID    string
	LeaseID   string
	ExpiresAt time.Time
}

// Claim allows an executor to claim an assigned task, creating a time-limited lease.
// This implements the execution-lease pattern: the claim is atomic via optimistic
// locking, and the lease expires after leaseTimeout if not reported.
func (s *Service) Claim(ctx context.Context, req ClaimRequest) (*ClaimResult, error) {
	if req.ExecutorID == "" {
		return nil, apperr.ValidationFailed("executor_id is required")
	}
	t, err := s.tasks.GetPilotTask(ctx, req.TaskID)
	if err != nil {
		if domain.IsNotFound(err) {
			return nil, apperr.NotFound("pilot_task", req.TaskID)
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "get pilot task failed", err)
	}
	if t.Status != domain.PTStatusAssigned {
		return nil, apperr.InvalidTransition("pilot_task", string(t.Status), string(domain.PTStatusClaimed))
	}
	now := s.clock.Now()
	expiresAt := now.Add(s.leaseTimeout)
	leaseID := newUUID()
	// Atomically claim the task (optimistic lock).
	affected, err := s.tasks.UpdatePilotTaskClaim(ctx, req.TaskID, req.ExecutorID, leaseID,
		expiresAt.Format("2006-01-02T15:04:05Z07:00"), t.Version)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "claim task failed", err)
	}
	if affected == 0 {
		return nil, apperr.Conflict("pilot_task", req.TaskID, t.Version)
	}
	// Create the lease record.
	lease := &domain.TaskLease{
		ID:         leaseID,
		TaskType:   domain.EntityPilotTask,
		TaskID:     req.TaskID,
		ExecutorID: req.ExecutorID,
		ClaimedAt:  now,
		ExpiresAt:  expiresAt,
		Status:     domain.LeaseStatusActive,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := s.leases.CreateLease(ctx, lease); err != nil {
		// Best-effort: clear the claim if lease creation fails.
		_, _ = s.tasks.ClearPilotTaskClaim(ctx, req.TaskID, t.Version+1)
		return nil, apperr.Wrap(apperr.CodeInternal, "create lease failed", err)
	}
	_ = s.audit.Record(ctx, audit.Entry{
		Actor:      req.ExecutorID,
		Action:     "claim_pilot_task",
		EntityType: domain.EntityPilotTask,
		EntityID:   req.TaskID,
		After:      map[string]string{"executor": req.ExecutorID, "lease": leaseID},
		RequestID:  req.RequestID,
	})
	return &ClaimResult{
		TaskID:    req.TaskID,
		LeaseID:   leaseID,
		ExpiresAt: expiresAt,
	}, nil
}

// ReportRequest carries the data for an executor reporting task completion.
type ReportRequest struct {
	TaskID     string
	ExecutorID string
	Result     string
	ReportData string
	RequestID  string
}

// Report records the completion of a claimed task and releases the lease.
func (s *Service) Report(ctx context.Context, req ReportRequest) error {
	if req.ExecutorID == "" {
		return apperr.ValidationFailed("executor_id is required")
	}
	t, err := s.tasks.GetPilotTask(ctx, req.TaskID)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("pilot_task", req.TaskID)
		}
		return apperr.Wrap(apperr.CodeInternal, "get pilot task failed", err)
	}
	if t.ClaimedBy != req.ExecutorID {
		return apperr.New(apperr.CodeForbidden,
			fmt.Sprintf("task %s is claimed by %s, not %s", req.TaskID, t.ClaimedBy, req.ExecutorID))
	}
	newStatus, err := s.sm.MustTransition(string(t.Status), string(domain.PTStatusCompleted))
	if err != nil {
		return apperr.InvalidTransition("pilot_task", string(t.Status), string(domain.PTStatusCompleted))
	}
	// Transition task to completed.
	affected, err := s.tasks.UpdatePilotTaskStatus(ctx, req.TaskID, domain.PilotTaskStatus(newStatus), t.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "report task failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("pilot_task", req.TaskID, t.Version)
	}
	// Release the lease.
	if t.LeaseID != "" {
		lease, err := s.leases.GetLease(ctx, t.LeaseID)
		if err == nil {
			_, _ = s.leases.ReleaseLease(ctx, lease.ID, lease.Version)
		}
	}
	// Record execution result.
	now := s.clock.Now()
	_ = s.executions.InsertExecution(ctx, &domain.ExecutionRecord{
		ID:           newUUID(),
		TaskType:     domain.EntityPilotTask,
		TaskID:       req.TaskID,
		ExecutorID:   req.ExecutorID,
		Result:       req.Result,
		ErrorMessage: "",
		DurationMs:   0,
		Timestamp:    now,
	})
	_ = s.audit.RecordTransition(ctx, req.ExecutorID, "report_pilot_task", req.TaskID,
		domain.EntityPilotTask, string(t.Status), newStatus, req.RequestID)
	return nil
}

// PreemptExpiredClaims finds tasks whose leases have expired and preempts them,
// making them available for re-assignment. This implements the preemption
// and re-distribution business rule.
func (s *Service) PreemptExpiredClaims(ctx context.Context) ([]*PreemptResult, error) {
	nowStr := s.clock.Now().Format("2006-01-02T15:04:05Z07:00")
	expired, err := s.tasks.ListExpiredClaims(ctx, nowStr)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInternal, "list expired claims", err)
	}
	var results []*PreemptResult
	for _, t := range expired {
		result, err := s.preemptOne(ctx, t)
		if err != nil {
			s.logger.Error("failed to preempt task", err, apperr.F("task_id", t.ID))
			continue
		}
		results = append(results, result)
	}
	return results, nil
}

// PreemptResult describes the outcome of preempting one task.
type PreemptResult struct {
	TaskID       string
	PrevExecutor string
	Reason       string
}

func (s *Service) preemptOne(ctx context.Context, t *domain.PilotTugTask) (*PreemptResult, error) {
	// Clear the claim and set status to preempted.
	affected, err := s.tasks.ClearPilotTaskClaim(ctx, t.ID, t.Version)
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, apperr.Conflict("pilot_task", t.ID, t.Version)
	}
	// Revoke the lease.
	if t.LeaseID != "" {
		lease, err := s.leases.GetLease(ctx, t.LeaseID)
		if err == nil {
			_, _ = s.leases.RevokeLease(ctx, lease.ID, "lease expired", lease.Version)
		}
	}
	_ = s.audit.Record(ctx, audit.Entry{
		Actor:      "scheduler",
		Action:     "preempt_pilot_task",
		EntityType: domain.EntityPilotTask,
		EntityID:   t.ID,
		Before:     map[string]string{"executor": t.ClaimedBy},
		After:      map[string]string{"status": "preempted"},
	})
	return &PreemptResult{
		TaskID:       t.ID,
		PrevExecutor: t.ClaimedBy,
		Reason:       "lease expired",
	}, nil
}

// Reassign transitions a preempted task back to assigned for a new executor.
func (s *Service) Reassign(ctx context.Context, id, actor, requestID string) error {
	t, err := s.tasks.GetPilotTask(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return apperr.NotFound("pilot_task", id)
		}
		return apperr.Wrap(apperr.CodeInternal, "get pilot task failed", err)
	}
	newStatus, err := s.sm.MustTransition(string(t.Status), string(domain.PTStatusAssigned))
	if err != nil {
		return apperr.InvalidTransition("pilot_task", string(t.Status), string(domain.PTStatusAssigned))
	}
	affected, err := s.tasks.UpdatePilotTaskStatus(ctx, id, domain.PilotTaskStatus(newStatus), t.Version)
	if err != nil {
		return apperr.Wrap(apperr.CodeInternal, "reassign failed", err)
	}
	if affected == 0 {
		return apperr.Conflict("pilot_task", id, t.Version)
	}
	_ = s.audit.RecordTransition(ctx, actor, "reassign_pilot_task", id,
		domain.EntityPilotTask, string(t.Status), newStatus, requestID)
	return nil
}

// Get retrieves a single pilot/tug task by ID.
func (s *Service) Get(ctx context.Context, id string) (*domain.PilotTugTask, error) {
	t, err := s.tasks.GetPilotTask(ctx, id)
	if err != nil {
		if domain.IsNotFound(err) {
			return nil, apperr.NotFound("pilot_task", id)
		}
		return nil, apperr.Wrap(apperr.CodeInternal, "get pilot task failed", err)
	}
	return t, nil
}

// List returns a paginated list of pilot/tug tasks.
func (s *Service) List(ctx context.Context, q domain.PageQuery) (domain.PageResult[*domain.PilotTugTask], error) {
	if err := q.Validate(100); err != nil {
		return domain.PageResult[*domain.PilotTugTask]{}, apperr.ValidationFailed(err.Error())
	}
	return s.tasks.ListPilotTasks(ctx, q)
}

// ListClaimable returns tasks available for an executor to claim.
func (s *Service) ListClaimable(ctx context.Context, limit int) ([]*domain.PilotTugTask, error) {
	if limit < 1 || limit > 100 {
		limit = 10
	}
	return s.tasks.ListClaimableTasks(ctx, limit)
}

// BacklogSummary reports pilot-task counts by status.
type BacklogSummary struct {
	Created    int
	Assigned   int
	Claimed    int
	InProgress int
	Completed  int
	Preempted  int
	Cancelled  int
}

// Backlog returns pilot-task counts by status.
func (s *Service) Backlog(ctx context.Context) (*BacklogSummary, error) {
	b := &BacklogSummary{}
	var err error
	b.Created, err = s.tasks.CountPilotTasksByStatus(ctx, domain.PTStatusCreated)
	if err != nil {
		return nil, err
	}
	b.Assigned, err = s.tasks.CountPilotTasksByStatus(ctx, domain.PTStatusAssigned)
	if err != nil {
		return nil, err
	}
	b.Claimed, err = s.tasks.CountPilotTasksByStatus(ctx, domain.PTStatusClaimed)
	if err != nil {
		return nil, err
	}
	b.InProgress, err = s.tasks.CountPilotTasksByStatus(ctx, domain.PTStatusInProgress)
	if err != nil {
		return nil, err
	}
	b.Completed, err = s.tasks.CountPilotTasksByStatus(ctx, domain.PTStatusCompleted)
	if err != nil {
		return nil, err
	}
	b.Preempted, err = s.tasks.CountPilotTasksByStatus(ctx, domain.PTStatusPreempted)
	if err != nil {
		return nil, err
	}
	b.Cancelled, err = s.tasks.CountPilotTasksByStatus(ctx, domain.PTStatusCancelled)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func validateCreate(req CreateRequest) error {
	if req.DeclarationID == "" {
		return apperr.ValidationFailed("declaration_id is required")
	}
	if req.TaskType == "" {
		return apperr.ValidationFailed("task_type is required")
	}
	if req.Location == "" {
		return apperr.ValidationFailed("location is required")
	}
	if req.Priority < 1 || req.Priority > 10 {
		req.Priority = 5
	}
	return nil
}
