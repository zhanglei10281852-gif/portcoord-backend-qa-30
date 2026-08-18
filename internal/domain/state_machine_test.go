package domain

import (
	"testing"
)

func TestDeclarationStateMachine_AcceptsValidTransitions(t *testing.T) {
	sm := DeclarationStateMachine()
	tests := []struct {
		from, to DeclarationStatus
	}{
		{DeclStatusSubmitted, DeclStatusReviewing},
		{DeclStatusSubmitted, DeclStatusAccepted},
		{DeclStatusSubmitted, DeclStatusQueued},
		{DeclStatusSubmitted, DeclStatusCancelled},
		{DeclStatusReviewing, DeclStatusAccepted},
		{DeclStatusReviewing, DeclStatusRejected},
		{DeclStatusAccepted, DeclStatusScheduled},
		{DeclStatusQueued, DeclStatusScheduled},
		{DeclStatusScheduled, DeclStatusProcessing},
		{DeclStatusProcessing, DeclStatusCompleted},
	}
	for _, tt := range tests {
		if !sm.CanTransition(string(tt.from), string(tt.to)) {
			t.Errorf("expected transition %s -> %s to be allowed", tt.from, tt.to)
		}
	}
}

func TestDeclarationStateMachine_RejectsInvalidTransitions(t *testing.T) {
	sm := DeclarationStateMachine()
	tests := []struct {
		from, to DeclarationStatus
	}{
		{DeclStatusCompleted, DeclStatusSubmitted},
		{DeclStatusRejected, DeclStatusAccepted},
		{DeclStatusCancelled, DeclStatusProcessing},
		{DeclStatusCompleted, DeclStatusCancelled},
		{DeclStatusRejected, DeclStatusCancelled},
	}
	for _, tt := range tests {
		_, err := sm.MustTransition(string(tt.from), string(tt.to))
		if err == nil {
			t.Errorf("expected illegal transition %s -> %s to be rejected", tt.from, tt.to)
		}
	}
}

func TestWindowStateMachine_AllLegalTransitions(t *testing.T) {
	sm := WindowStateMachine()
	tests := []struct {
		from, to WindowStatus
	}{
		{WindowStatusAllocated, WindowStatusEffective},
		{WindowStatusAllocated, WindowStatusCancelled},
		{WindowStatusAllocated, WindowStatusEscalated},
		{WindowStatusEffective, WindowStatusOccupied},
		{WindowStatusEffective, WindowStatusReleased},
		{WindowStatusEffective, WindowStatusEscalated},
		{WindowStatusOccupied, WindowStatusReleased},
		{WindowStatusEscalated, WindowStatusEffective},
	}
	for _, tt := range tests {
		if !sm.CanTransition(string(tt.from), string(tt.to)) {
			t.Errorf("expected transition %s -> %s to be allowed", tt.from, tt.to)
		}
	}
}

func TestWindowStateMachine_IllegalTransitionsRejected(t *testing.T) {
	sm := WindowStateMachine()
	illegal := []struct {
		from, to WindowStatus
	}{
		{WindowStatusReleased, WindowStatusOccupied},
		{WindowStatusCancelled, WindowStatusEffective},
		{WindowStatusReleased, WindowStatusCancelled},
	}
	for _, tt := range illegal {
		_, err := sm.MustTransition(string(tt.from), string(tt.to))
		if err == nil {
			t.Errorf("expected illegal transition %s -> %s to be rejected", tt.from, tt.to)
		}
	}
}

func TestWorkOrderStateMachine_ValidAndInvalidTransitions(t *testing.T) {
	sm := WorkOrderStateMachine()
	if !sm.CanTransition(string(WOStatusCreated), string(WOStatusAssigned)) {
		t.Error("created -> assigned should be allowed")
	}
	if !sm.CanTransition(string(WOStatusInProgress), string(WOStatusCompleted)) {
		t.Error("in_progress -> completed should be allowed")
	}
	_, err := sm.MustTransition(string(WOStatusCreated), string(WOStatusCompleted))
	if err == nil {
		t.Error("created -> completed should be illegal")
	}
	_, err = sm.MustTransition(string(WOStatusCompleted), string(WOStatusCancelled))
	if err == nil {
		t.Error("completed -> cancelled should be illegal")
	}
}

func TestPilotTaskStateMachine_PreemptedCanReassign(t *testing.T) {
	sm := PilotTaskStateMachine()
	if !sm.CanTransition(string(PTStatusPreempted), string(PTStatusAssigned)) {
		t.Error("preempted -> assigned should be allowed for reassignment")
	}
	if !sm.CanTransition(string(PTStatusAssigned), string(PTStatusClaimed)) {
		t.Error("assigned -> claimed should be allowed")
	}
	_, err := sm.MustTransition(string(PTStatusCreated), string(PTStatusClaimed))
	if err == nil {
		t.Error("created -> claimed should be illegal (must assign first)")
	}
}

func TestQuotaStateMachine_Transitions(t *testing.T) {
	sm := QuotaStateMachine()
	if !sm.CanTransition(string(QuotaStatusAvailable), string(QuotaStatusWarning)) {
		t.Error("available -> warning should be allowed")
	}
	if !sm.CanTransition(string(QuotaStatusExhausted), string(QuotaStatusAvailable)) {
		t.Error("exhausted -> available should be allowed on new day")
	}
	_, err := sm.MustTransition(string(QuotaStatusReserved), string(QuotaStatusCompleted))
	if err == nil {
		t.Error("reserved -> completed should be illegal")
	}
}

func TestStateMachine_AllowedTargets(t *testing.T) {
	sm := DeclarationStateMachine()
	targets := sm.AllowedTargets(string(DeclStatusSubmitted))
	if len(targets) == 0 {
		t.Error("submitted should have allowed targets")
	}
	found := false
	for _, tgt := range targets {
		if tgt == string(DeclStatusAccepted) {
			found = true
		}
	}
	if !found {
		t.Error("submitted should allow transition to accepted")
	}
}

func TestStateMachine_InitialState(t *testing.T) {
	tests := []struct {
		name     string
		sm       *StateMachine
		expected string
	}{
		{"declaration", DeclarationStateMachine(), string(DeclStatusSubmitted)},
		{"window", WindowStateMachine(), string(WindowStatusAllocated)},
		{"work_order", WorkOrderStateMachine(), string(WOStatusCreated)},
		{"pilot_task", PilotTaskStateMachine(), string(PTStatusCreated)},
		{"quota", QuotaStateMachine(), string(QuotaStatusAvailable)},
	}
	for _, tt := range tests {
		if tt.sm.InitialState() != tt.expected {
			t.Errorf("%s initial state: expected %s, got %s", tt.name, tt.expected, tt.sm.InitialState())
		}
	}
}

func TestStateMachine_AllTransitionsNonEmpty(t *testing.T) {
	sms := []*StateMachine{
		DeclarationStateMachine(),
		WindowStateMachine(),
		WorkOrderStateMachine(),
		PilotTaskStateMachine(),
		QuotaStateMachine(),
	}
	for _, sm := range sms {
		transitions := sm.AllTransitions()
		if len(transitions) < 3 {
			t.Errorf("state machine %s has too few transitions: %d", sm.name, len(transitions))
		}
	}
}
