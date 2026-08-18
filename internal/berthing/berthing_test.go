package berthing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"portcoord/internal/apperr"
	"portcoord/internal/audit"
	"portcoord/internal/domain"
	"portcoord/internal/store"
)

func setupTest(t *testing.T) (*Service, *store.SQLiteStore, *apperr.FakeClock, func()) {
	t.Helper()
	db, err := store.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(findMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := store.NewSQLiteStore(db)
	clock := apperr.NewFake(time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC))
	auditRec := audit.New(st, clock)
	svc := New(Deps{
		Windows: st, Declarations: st, Escalations: st, Handovers: st,
		Audit: auditRec, Clock: clock, Logger: apperr.NopLogger(),
		LeaseTimeout: 60 * time.Second,
	})
	return svc, st, clock, func() { st.Close() }
}

func mustCreateDecl(t *testing.T, st *store.SQLiteStore, id string) {
	t.Helper()
	now := time.Now().UTC()
	d := &domain.ArrivalDeclaration{
		ID: id, ShipName: "Ship", IMONumber: "IMO", VoyageNumber: "V",
		ETA: now.Add(24 * time.Hour), CargoType: "cargo", CargoVolume: 1,
		CargoUnit: "TEU", DeclaredBy: "agent", DeclaringParty: domain.PartyShipOwner,
		Status: domain.DeclStatusAccepted, Priority: 5, Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := st.CreateDeclaration(context.Background(), d); err != nil {
		t.Fatalf("create declaration: %v", err)
	}
}

func TestBerthing_CreateWindow(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-1")

	now := clock.Now()
	w, err := svc.Create(context.Background(), CreateRequest{
		DeclarationID:    "decl-1",
		BerthNumber:      "B1",
		ShipName:         "TestShip",
		EffectiveAt:      now,
		DeadlineAt:       now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.Status != domain.WindowStatusAllocated {
		t.Errorf("expected allocated, got %s", w.Status)
	}
}

func TestBerthing_BatchAllocate(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-b1")
	mustCreateDecl(t, st, "decl-b2")

	now := clock.Now()
	items := []BatchItem{
		{DeclarationID: "decl-b1", BerthNumber: "B1", ShipName: "Ship1",
			EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour), ResponsibleParty: domain.PartyTerminal},
		{DeclarationID: "decl-b2", BerthNumber: "B2", ShipName: "Ship2",
			EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour), ResponsibleParty: domain.PartyTerminal},
	}
	windows, err := svc.BatchAllocate(context.Background(), "dispatcher", items, "req-1")
	if err != nil {
		t.Fatalf("batch allocate: %v", err)
	}
	if len(windows) != 2 {
		t.Errorf("expected 2 windows, got %d", len(windows))
	}
}

func TestBerthing_BatchAllocateRejectEmpty(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()
	_, err := svc.BatchAllocate(context.Background(), "d", nil, "req")
	if err == nil {
		t.Fatal("expected error for empty batch")
	}
}

func TestBerthing_Release(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-r1")

	now := clock.Now()
	w, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-r1", BerthNumber: "B1", ShipName: "Ship",
		EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	// Activate the window first (allocated -> effective).
	st.UpdateWindowStatus(context.Background(), w.ID, domain.WindowStatusEffective, w.Version)
	w.Version++

	err := svc.Release(context.Background(), w.ID, "dispatcher", "req")
	if err != nil {
		t.Fatalf("release: %v", err)
	}

	got, _ := svc.Get(context.Background(), w.ID)
	if got.Status != domain.WindowStatusReleased {
		t.Errorf("expected released, got %s", got.Status)
	}
}

func TestBerthing_ReleaseIllegalTransition(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-il1")

	now := clock.Now()
	w, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-il1", BerthNumber: "B1", ShipName: "Ship",
		EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	_ = svc.Release(context.Background(), w.ID, "d", "r")
	// Releasing a released window should fail.
	err := svc.Release(context.Background(), w.ID, "d", "r")
	if err == nil {
		t.Fatal("expected error releasing already-released window")
	}
	if !apperr.IsInvalidTransition(err) {
		t.Errorf("expected invalid transition, got %v", err)
	}
}

func TestBerthing_EscalateOverdueDeadlineExceeded(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-esc1")

	now := clock.Now()
	w, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-esc1", BerthNumber: "B1", ShipName: "Ship",
		EffectiveAt: now, DeadlineAt: now.Add(1 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	// Activate the window.
	st.UpdateWindowStatus(context.Background(), w.ID, domain.WindowStatusEffective, w.Version)

	// Advance clock past deadline.
	clock.Advance(2 * time.Hour)

	results, err := svc.EscalateOverdue(context.Background())
	if err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 escalation, got %d", len(results))
	}
	if results[0].FromLevel != 0 || results[0].ToLevel != 1 {
		t.Errorf("expected escalation 0->1, got %d->%d", results[0].FromLevel, results[0].ToLevel)
	}

	got, _ := svc.Get(context.Background(), w.ID)
	if got.EscalationLevel != 1 {
		t.Errorf("expected escalation level 1, got %d", got.EscalationLevel)
	}
	if got.Status != domain.WindowStatusEscalated {
		t.Errorf("expected escalated status, got %s", got.Status)
	}
}

func TestBerthing_ActivateEffectiveWindow(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-act1")

	now := clock.Now()
	w, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-act1", BerthNumber: "B1", ShipName: "Ship",
		EffectiveAt: now.Add(1 * time.Hour), DeadlineAt: now.Add(25 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})

	// Before effective time: no activation.
	count, _ := svc.ActivateEffective(context.Background())
	if count != 0 {
		t.Errorf("expected 0 activations before effective, got %d", count)
	}

	// After effective time: should activate.
	clock.Advance(2 * time.Hour)
	count, _ = svc.ActivateEffective(context.Background())
	if count != 1 {
		t.Errorf("expected 1 activation, got %d", count)
	}

	got, _ := svc.Get(context.Background(), w.ID)
	if got.Status != domain.WindowStatusEffective {
		t.Errorf("expected effective, got %s", got.Status)
	}
}

func TestBerthing_ForceIntervene(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-int1")

	now := clock.Now()
	w, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-int1", BerthNumber: "B1", ShipName: "Ship",
		EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})

	err := svc.ForceIntervene(context.Background(), w.ID, "supervisor", domain.WindowStatusCancelled, "req")
	if err != nil {
		t.Fatalf("force intervene: %v", err)
	}

	got, _ := svc.Get(context.Background(), w.ID)
	if got.Status != domain.WindowStatusCancelled {
		t.Errorf("expected cancelled, got %s", got.Status)
	}
}

func TestBerthing_ForceInterveneInvalidTransitionReject(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-int2")

	now := clock.Now()
	w, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-int2", BerthNumber: "B1", ShipName: "Ship",
		EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	// Cancel it first.
	svc.ForceIntervene(context.Background(), w.ID, "s", domain.WindowStatusCancelled, "r")

	// Try to force an invalid transition: cancelled -> occupied.
	err := svc.ForceIntervene(context.Background(), w.ID, "s", domain.WindowStatusOccupied, "r")
	if err == nil {
		t.Fatal("expected error for invalid intervention")
	}
	if !apperr.IsInvalidTransition(err) {
		t.Errorf("expected invalid transition, got %v", err)
	}
}

func TestBerthing_GetNotFound(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()
	_, err := svc.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !apperr.IsNotFound(err) {
		t.Errorf("expected not found, got %v", err)
	}
}

func TestBerthing_PersistRestartRecover(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "restart.db")
	migrations := findMigrations()

	// First session: create a window.
	db1, err := store.Open(dbPath, migrations)
	if err != nil {
		t.Fatalf("open db1: %v", err)
	}
	st1 := store.NewSQLiteStore(db1)
	mustCreateDecl(t, st1, "decl-restart1")
	clock1 := apperr.NewFake(time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC))
	svc1 := New(Deps{
		Windows: st1, Declarations: st1, Escalations: st1, Handovers: st1,
		Audit: audit.New(st1, clock1), Clock: clock1, Logger: apperr.NopLogger(),
		LeaseTimeout: 60 * time.Second,
	})
	now := clock1.Now()
	w, err := svc1.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-restart1", BerthNumber: "B1", ShipName: "RestartShip",
		EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	st1.Close()

	// Second session: reopen and verify the window persisted.
	db2, err := store.Open(dbPath, migrations)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	st2 := store.NewSQLiteStore(db2)
	defer st2.Close()
	clock2 := apperr.NewFake(time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC))
	svc2 := New(Deps{
		Windows: st2, Declarations: st2, Escalations: st2, Handovers: st2,
		Audit: audit.New(st2, clock2), Clock: clock2, Logger: apperr.NopLogger(),
		LeaseTimeout: 60 * time.Second,
	})
	got, err := svc2.Get(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.ShipName != "RestartShip" {
		t.Errorf("ship name after restart: expected RestartShip, got %s", got.ShipName)
	}
	if got.Status != domain.WindowStatusAllocated {
		t.Errorf("status after restart: expected allocated, got %s", got.Status)
	}
}

func TestBerthing_Backlog(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-bl1")
	mustCreateDecl(t, st, "decl-bl2")

	now := clock.Now()
	svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-bl1", BerthNumber: "B1", ShipName: "S1",
		EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-bl2", BerthNumber: "B2", ShipName: "S2",
		EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})

	backlog, err := svc.Backlog(context.Background())
	if err != nil {
		t.Fatalf("backlog: %v", err)
	}
	if backlog.Allocated != 2 {
		t.Errorf("expected 2 allocated, got %d", backlog.Allocated)
	}
}

func findMigrations() string {
	for _, c := range []string{"./migrations", "../migrations", "../../migrations"} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "./migrations"
}
