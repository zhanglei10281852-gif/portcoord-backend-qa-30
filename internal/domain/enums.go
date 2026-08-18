package domain

// PartyRole identifies one of the four collaborating parties.
type PartyRole string

const (
	PartyShipOwner PartyRole = "ship_owner"
	PartyTerminal  PartyRole = "terminal_dispatch"
	PartyPilotTug  PartyRole = "pilot_tug"
	PartyAuthority PartyRole = "port_authority"
)

// AllParties returns the four collaborating party roles.
func AllParties() []PartyRole {
	return []PartyRole{PartyShipOwner, PartyTerminal, PartyPilotTug, PartyAuthority}
}

// DeclarationStatus enumerates arrival-declaration lifecycle states.
type DeclarationStatus string

const (
	DeclStatusDraft      DeclarationStatus = "draft"
	DeclStatusSubmitted  DeclarationStatus = "submitted"
	DeclStatusReviewing  DeclarationStatus = "reviewing"
	DeclStatusAccepted   DeclarationStatus = "accepted"
	DeclStatusQueued     DeclarationStatus = "queued"
	DeclStatusScheduled  DeclarationStatus = "scheduled"
	DeclStatusProcessing DeclarationStatus = "processing"
	DeclStatusCompleted  DeclarationStatus = "completed"
	DeclStatusRejected   DeclarationStatus = "rejected"
	DeclStatusCancelled  DeclarationStatus = "cancelled"
)

// WindowStatus enumerates berthing-window lifecycle states.
type WindowStatus string

const (
	WindowStatusAllocated WindowStatus = "allocated"
	WindowStatusEffective WindowStatus = "effective"
	WindowStatusOccupied  WindowStatus = "occupied"
	WindowStatusReleased  WindowStatus = "released"
	WindowStatusExpired   WindowStatus = "expired"
	WindowStatusEscalated WindowStatus = "escalated"
	WindowStatusCancelled WindowStatus = "cancelled"
)

// WorkOrderStatus enumerates work-order lifecycle states.
type WorkOrderStatus string

const (
	WOStatusCreated    WorkOrderStatus = "created"
	WOStatusAssigned   WorkOrderStatus = "assigned"
	WOStatusInProgress WorkOrderStatus = "in_progress"
	WOStatusCompleted  WorkOrderStatus = "completed"
	WOStatusCancelled  WorkOrderStatus = "cancelled"
	WOStatusFailed     WorkOrderStatus = "failed"
)

// PilotTaskStatus enumerates pilot/tug task lifecycle states.
type PilotTaskStatus string

const (
	PTStatusCreated    PilotTaskStatus = "created"
	PTStatusAssigned   PilotTaskStatus = "assigned"
	PTStatusClaimed    PilotTaskStatus = "claimed"
	PTStatusInProgress PilotTaskStatus = "in_progress"
	PTStatusCompleted  PilotTaskStatus = "completed"
	PTStatusPreempted  PilotTaskStatus = "preempted"
	PTStatusCancelled  PilotTaskStatus = "cancelled"
)

// QuotaStatus enumerates quota states.
type QuotaStatus string

const (
	QuotaStatusAvailable QuotaStatus = "available"
	QuotaStatusWarning   QuotaStatus = "warning"
	QuotaStatusExhausted QuotaStatus = "exhausted"
	QuotaStatusReserved  QuotaStatus = "reserved"
	QuotaStatusCompleted QuotaStatus = "completed"
)

// QuotaType identifies a quota category.
type QuotaType string

const (
	QuotaTypeCabin QuotaType = "cabin"
	QuotaTypeYard  QuotaType = "yard"
)

// WorkOrderType identifies the operation type.
type WorkOrderType string

const (
	WOTypeLoading   WorkOrderType = "loading"
	WOTypeUnloading WorkOrderType = "unloading"
)

// PilotTaskType identifies pilot/tug task variant.
type PilotTaskType string

const (
	PTTypePilot  PilotTaskType = "pilot"
	PTTypeTug    PilotTaskType = "tug"
	PTTypeTowing PilotTaskType = "towing"
)

// EntityType identifies the kind of domain object in audit/handover records.
type EntityType string

const (
	EntityDeclaration    EntityType = "declaration"
	EntityBerthingWindow EntityType = "berthing_window"
	EntityWorkOrder      EntityType = "work_order"
	EntityPilotTask      EntityType = "pilot_task"
	EntityQuota          EntityType = "quota"
)

// HandoverStatus enumerates handover-document states.
type HandoverStatus string

const (
	HandoverStatusPending   HandoverStatus = "pending"
	HandoverStatusConfirmed HandoverStatus = "confirmed"
	HandoverStatusRejected  HandoverStatus = "rejected"
)

// LeaseStatus enumerates task-lease states.
type LeaseStatus string

const (
	LeaseStatusActive   LeaseStatus = "active"
	LeaseStatusReleased LeaseStatus = "released"
	LeaseStatusExpired  LeaseStatus = "expired"
	LeaseStatusRevoked  LeaseStatus = "revoked"
)

// SortOrder for list queries.
type SortOrder string

const (
	SortAsc  SortOrder = "asc"
	SortDesc SortOrder = "desc"
)
