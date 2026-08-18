package domain

import (
	"fmt"
	"sync"
)

// Transition defines a single allowed state change.
type Transition struct {
	From string
	To   string
}

// StateMachine is a generic state machine driven by an explicit transition table.
// It enforces that only declared transitions are legal and rejects all others.
type StateMachine struct {
	mu           sync.RWMutex
	name         string
	transitions  map[string]map[string]bool
	initialState string
}

// NewStateMachine creates a state machine with the given name and initial state.
func NewStateMachine(name, initial string) *StateMachine {
	return &StateMachine{
		name:         name,
		transitions:  make(map[string]map[string]bool),
		initialState: initial,
	}
}

// Allow registers a transition from src to dst.
func (sm *StateMachine) Allow(src, dst string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.transitions[src] == nil {
		sm.transitions[src] = make(map[string]bool)
	}
	sm.transitions[src][dst] = true
}

// AllowMany registers multiple transitions from the same source.
func (sm *StateMachine) AllowMany(src string, dsts ...string) {
	for _, dst := range dsts {
		sm.Allow(src, dst)
	}
}

// CanTransition returns true if the transition from src to dst is legal.
func (sm *StateMachine) CanTransition(src, dst string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if sm.transitions[src] == nil {
		return false
	}
	return sm.transitions[src][dst]
}

// MustTransition validates and returns the new state, or an error describing
// the illegal transition. This is the single enforcement point for state rules.
func (sm *StateMachine) MustTransition(src, dst string) (string, error) {
	if !sm.CanTransition(src, dst) {
		return "", fmt.Errorf("illegal transition for %s: %s -> %s", sm.name, src, dst)
	}
	return dst, nil
}

// InitialState returns the configured initial state.
func (sm *StateMachine) InitialState() string {
	return sm.initialState
}

// AllowedTargets returns all states reachable from src.
func (sm *StateMachine) AllowedTargets(src string) []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	var out []string
	if sm.transitions[src] == nil {
		return out
	}
	for dst := range sm.transitions[src] {
		out = append(out, dst)
	}
	return out
}

// AllTransitions returns every registered transition for inspection/testing.
func (sm *StateMachine) AllTransitions() []Transition {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	var out []Transition
	for src, dsts := range sm.transitions {
		for dst := range dsts {
			out = append(out, Transition{From: src, To: dst})
		}
	}
	return out
}

// DeclarationStateMachine returns the state machine for arrival declarations.
func DeclarationStateMachine() *StateMachine {
	sm := NewStateMachine("declaration", string(DeclStatusSubmitted))
	sm.AllowMany(string(DeclStatusSubmitted),
		string(DeclStatusReviewing), string(DeclStatusAccepted), string(DeclStatusRejected), string(DeclStatusQueued), string(DeclStatusCancelled))
	sm.AllowMany(string(DeclStatusReviewing),
		string(DeclStatusAccepted), string(DeclStatusRejected), string(DeclStatusQueued))
	sm.AllowMany(string(DeclStatusAccepted),
		string(DeclStatusQueued), string(DeclStatusScheduled), string(DeclStatusCancelled))
	sm.AllowMany(string(DeclStatusQueued),
		string(DeclStatusScheduled), string(DeclStatusCancelled))
	sm.AllowMany(string(DeclStatusScheduled),
		string(DeclStatusProcessing), string(DeclStatusCancelled))
	sm.AllowMany(string(DeclStatusProcessing),
		string(DeclStatusCompleted), string(DeclStatusCancelled))
	return sm
}

// WindowStateMachine returns the state machine for berthing windows.
func WindowStateMachine() *StateMachine {
	sm := NewStateMachine("berthing_window", string(WindowStatusAllocated))
	sm.AllowMany(string(WindowStatusAllocated),
		string(WindowStatusEffective), string(WindowStatusCancelled), string(WindowStatusEscalated))
	sm.AllowMany(string(WindowStatusEffective),
		string(WindowStatusOccupied), string(WindowStatusExpired), string(WindowStatusEscalated), string(WindowStatusReleased))
	sm.AllowMany(string(WindowStatusOccupied),
		string(WindowStatusReleased), string(WindowStatusEscalated))
	sm.AllowMany(string(WindowStatusEscalated),
		string(WindowStatusEffective), string(WindowStatusReleased), string(WindowStatusCancelled))
	return sm
}

// WorkOrderStateMachine returns the state machine for work orders.
func WorkOrderStateMachine() *StateMachine {
	sm := NewStateMachine("work_order", string(WOStatusCreated))
	sm.AllowMany(string(WOStatusCreated),
		string(WOStatusAssigned), string(WOStatusCancelled))
	sm.AllowMany(string(WOStatusAssigned),
		string(WOStatusInProgress), string(WOStatusCancelled))
	sm.AllowMany(string(WOStatusInProgress),
		string(WOStatusCompleted), string(WOStatusFailed), string(WOStatusCancelled))
	return sm
}

// PilotTaskStateMachine returns the state machine for pilot/tug tasks.
func PilotTaskStateMachine() *StateMachine {
	sm := NewStateMachine("pilot_task", string(PTStatusCreated))
	sm.AllowMany(string(PTStatusCreated),
		string(PTStatusAssigned), string(PTStatusCancelled))
	sm.AllowMany(string(PTStatusAssigned),
		string(PTStatusClaimed), string(PTStatusPreempted), string(PTStatusCancelled))
	sm.AllowMany(string(PTStatusClaimed), string(PTStatusCompleted),
		string(PTStatusInProgress), string(PTStatusPreempted))
	sm.AllowMany(string(PTStatusInProgress),
		string(PTStatusCompleted), string(PTStatusPreempted))
	sm.AllowMany(string(PTStatusPreempted),
		string(PTStatusAssigned), string(PTStatusCancelled))
	return sm
}

// QuotaStateMachine returns the state machine for quotas.
func QuotaStateMachine() *StateMachine {
	sm := NewStateMachine("quota", string(QuotaStatusAvailable))
	sm.AllowMany(string(QuotaStatusAvailable),
		string(QuotaStatusWarning), string(QuotaStatusReserved), string(QuotaStatusExhausted), string(QuotaStatusCompleted))
	sm.AllowMany(string(QuotaStatusWarning),
		string(QuotaStatusReserved), string(QuotaStatusExhausted), string(QuotaStatusAvailable), string(QuotaStatusCompleted))
	sm.AllowMany(string(QuotaStatusReserved),
		string(QuotaStatusAvailable), string(QuotaStatusExhausted), string(QuotaStatusWarning))
	sm.AllowMany(string(QuotaStatusExhausted),
		string(QuotaStatusAvailable), string(QuotaStatusWarning), string(QuotaStatusCompleted))
	return sm
}
