package domain

import "time"

// ArrivalDeclaration represents a ship's arrival declaration (到港申报).
type ArrivalDeclaration struct {
	ID              string
	ShipName        string
	IMONumber       string
	VoyageNumber    string
	ETA             time.Time
	BerthPreference string
	CargoType       string
	CargoVolume     int
	CargoUnit       string
	DeclaredBy      string
	DeclaringParty  PartyRole
	Status          DeclarationStatus
	Priority        int
	QueuePosition   int
	IdempotencyKey  string
	Version         int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// BerthingWindow represents a berthing window with effective and deadline times (靠泊窗口).
type BerthingWindow struct {
	ID               string
	DeclarationID    string
	BerthNumber      string
	ShipName         string
	EffectiveAt      time.Time
	DeadlineAt       time.Time
	AssignedTo       string
	ResponsibleParty PartyRole
	EscalationLevel  int
	Status           WindowStatus
	Version          int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IsExpired reports whether the window deadline has passed at the given time.
func (w *BerthingWindow) IsExpired(now time.Time) bool {
	return now.After(w.DeadlineAt)
}

// IsEffective reports whether the window is currently within its effective period.
func (w *BerthingWindow) IsEffective(now time.Time) bool {
	return !now.Before(w.EffectiveAt) && !now.After(w.DeadlineAt)
}

// WorkOrder represents a loading or unloading work order (装卸作业单).
type WorkOrder struct {
	ID               string
	DeclarationID    string
	BerthingWindowID string
	OrderType        WorkOrderType
	CargoType        string
	PlannedVolume    int
	ActualVolume     int
	AssignedTo       string
	Status           WorkOrderStatus
	StartedAt        *time.Time
	CompletedAt      *time.Time
	Version          int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// PilotTugTask represents a pilot or tug task (引航拖轮任务).
type PilotTugTask struct {
	ID               string
	DeclarationID    string
	BerthingWindowID string
	TaskType         PilotTaskType
	Location         string
	AssignedTo       string
	ClaimedBy        string
	ClaimExpiresAt   *time.Time
	LeaseID          string
	Status           PilotTaskStatus
	Priority         int
	ReportData       string
	Version          int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Quota represents a daily cabin-space or yard-quota allowance (舱位与堆场额度).
type Quota struct {
	ID             string
	QuotaType      QuotaType
	PeriodDate     string
	DailyLimit     int
	UsedAmount     int
	ReservedAmount int
	Status         QuotaStatus
	Version        int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Available returns the remaining quota.
func (q *Quota) Available() int {
	r := q.DailyLimit - q.UsedAmount - q.ReservedAmount
	if r < 0 {
		return 0
	}
	return r
}

// Utilization returns the fraction of quota consumed (0..1+).
func (q *Quota) Utilization() float64 {
	if q.DailyLimit <= 0 {
		return 0
	}
	return float64(q.UsedAmount+q.ReservedAmount) / float64(q.DailyLimit)
}

// HandoverDocument delineates responsibility boundaries between parties (交接单).
type HandoverDocument struct {
	ID          string
	EntityType  EntityType
	EntityID    string
	FromParty   PartyRole
	ToParty     PartyRole
	Action      string
	DocumentRef string
	Status      HandoverStatus
	Notes       string
	Version     int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TaskLease tracks an executor's claim on a task with an expiry (执行租约).
type TaskLease struct {
	ID            string
	TaskType      EntityType
	TaskID        string
	ExecutorID    string
	ClaimedAt     time.Time
	ExpiresAt     time.Time
	Status        LeaseStatus
	RevokedReason string
	Version       int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// IsExpired reports whether the lease has expired at the given time.
func (l *TaskLease) IsExpired(now time.Time) bool {
	return now.After(l.ExpiresAt)
}

// AuditLog records who did what when (审计记录).
type AuditLog struct {
	ID          string
	Actor       string
	Action      string
	EntityType  EntityType
	EntityID    string
	BeforeState string
	AfterState  string
	RequestID   string
	Timestamp   time.Time
}

// EscalationRecord records a responsibility escalation (升级记录).
type EscalationRecord struct {
	ID         string
	EntityType EntityType
	EntityID   string
	FromLevel  int
	ToLevel    int
	Reason     string
	ResolvedBy string
	ResolvedAt *time.Time
	Timestamp  time.Time
}

// ExecutionRecord records an executor's attempt result (执行记录).
type ExecutionRecord struct {
	ID           string
	TaskType     EntityType
	TaskID       string
	ExecutorID   string
	Result       string
	ErrorMessage string
	DurationMs   int64
	Timestamp    time.Time
}

// IdempotencyRecord stores a cached response for duplicate detection (幂等记录).
type IdempotencyRecord struct {
	Key            string
	ResponseBody   string
	ResponseStatus int
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// EscalationResult describes the outcome of escalating one entity.
type EscalationResult struct {
	WindowID    string
	FromLevel   int
	ToLevel     int
	NewParty    PartyRole
	NewAssignee string
}
