package engine

import (
	"context"
	"os"
	"testing"
	"time"

	"portcoord/internal/apperr"
	"portcoord/internal/audit"
	"portcoord/internal/berthing"
	"portcoord/internal/declaration"
	"portcoord/internal/domain"
	"portcoord/internal/pilottask"
	"portcoord/internal/store"
)

func setupTest(t *testing.T) (*Engine, *store.SQLiteStore, *apperr.FakeClock, *declaration.Service, *berthing.Service, *pilottask.Service, func()) {
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
	declSvc := declaration.New(declaration.Deps{
		Declarations: st, Quotas: st, Idempotency: st, Handovers: st,
		Audit: auditRec, Clock: clock, Logger: apperr.NopLogger(),
		CabinLimit: 1000, YardLimit: 5000,
	})
	windowSvc := berthing.New(berthing.Deps{
		Windows: st, Declarations: st, Escalations: st, Handovers: st,
		Audit: auditRec, Clock: clock, Logger: apperr.NopLogger(),
		LeaseTimeout: 60 * time.Second,
	})
	taskSvc := pilottask.New(pilottask.Deps{
		Tasks: st, Leases: st, Executions: st,
		Audit: auditRec, Clock: clock, Logger: apperr.NopLogger(),
		LeaseTimeout: 60 * time.Second,
	})
	eng := New(Deps{
		DeclarationService: declSvc,
		BerthingService:    windowSvc,
		PilotTaskService:   taskSvc,
		Clock:              apperr.Default(),
		Logger:             apperr.NopLogger(),
		TickInterval:       50 * time.Millisecond,
	})
	return eng, st, clock, declSvc, windowSvc, taskSvc, func() { st.Close() }
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

func TestEngine_StartStop(t *testing.T) {
	eng, _, _, _, _, _, cleanup := setupTest(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eng.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	eng.Stop()

	stats := eng.Stats()
	if stats.Ticks == 0 {
		t.Error("expected at least 1 tick")
	}
}

func TestEngine_EscalateOverdueWindow(t *testing.T) {
	eng, st, clock, _, windowSvc, _, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-eng1")

	now := clock.Now()
	w, _ := windowSvc.Create(context.Background(), berthing.CreateRequest{
		DeclarationID: "decl-eng1", BerthNumber: "B1", ShipName: "Ship",
		EffectiveAt: now, DeadlineAt: now.Add(1 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	st.UpdateWindowStatus(context.Background(), w.ID, domain.WindowStatusEffective, w.Version)

	// Advance past deadline.
	clock.Advance(2 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	eng.Stop()

	stats := eng.Stats()
	if stats.WindowsEscalated == 0 {
		t.Error("expected at least 1 escalation")
	}
}

func TestEngine_PreemptExpiredClaim(t *testing.T) {
	eng, st, clock, _, _, taskSvc, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-eng2")

	task, _ := taskSvc.Create(context.Background(), pilottask.CreateRequest{
		DeclarationID: "decl-eng2", TaskType: domain.PTTypePilot,
		Location: "B1", Priority: 5,
	})
	taskSvc.Assign(context.Background(), task.ID, "assignee", "d", "r")
	taskSvc.Claim(context.Background(), pilottask.ClaimRequest{
		TaskID: task.ID, ExecutorID: "exec-1",
	})

	// Advance past lease expiry.
	clock.Advance(2 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	eng.Stop()

	stats := eng.Stats()
	if stats.TasksPreempted == 0 {
		t.Error("expected at least 1 preemption")
	}

	got, _ := taskSvc.Get(context.Background(), task.ID)
	// After preemption + reassignment, should be assigned.
	if got.Status != domain.PTStatusAssigned && got.Status != domain.PTStatusPreempted {
		t.Errorf("expected assigned or preempted, got %s", got.Status)
	}
}

func TestEngine_PersistRestartRecover(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/restart.db"
	migrations := findMigrations()

	db1, _ := store.Open(dbPath, migrations)
	st1 := store.NewSQLiteStore(db1)
	mustCreateDecl(t, st1, "decl-persist1")
	clock1 := apperr.NewFake(time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC))
	windowSvc1 := berthing.New(berthing.Deps{
		Windows: st1, Declarations: st1, Escalations: st1, Handovers: st1,
		Audit: audit.New(st1, clock1), Clock: clock1, Logger: apperr.NopLogger(),
		LeaseTimeout: 60 * time.Second,
	})
	now := clock1.Now()
	w, _ := windowSvc1.Create(context.Background(), berthing.CreateRequest{
		DeclarationID: "decl-persist1", BerthNumber: "B1", ShipName: "PersistShip",
		EffectiveAt: now, DeadlineAt: now.Add(24 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	st1.Close()

	// Reopen and verify.
	db2, _ := store.Open(dbPath, migrations)
	st2 := store.NewSQLiteStore(db2)
	defer st2.Close()
	got, err := st2.GetWindow(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.ShipName != "PersistShip" {
		t.Errorf("expected PersistShip, got %s", got.ShipName)
	}
}

func TestEngine_ActivateWindow(t *testing.T) {
	eng, st, clock, _, windowSvc, _, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-eng3")

	now := clock.Now()
	w, _ := windowSvc.Create(context.Background(), berthing.CreateRequest{
		DeclarationID: "decl-eng3", BerthNumber: "B1", ShipName: "Ship",
		EffectiveAt: now.Add(1 * time.Hour), DeadlineAt: now.Add(25 * time.Hour),
		ResponsibleParty: domain.PartyTerminal,
	})
	clock.Advance(2 * time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng.Start(ctx)
	time.Sleep(200 * time.Millisecond)
	eng.Stop()

	stats := eng.Stats()
	if stats.WindowsActivated == 0 {
		t.Error("expected at least 1 activation")
	}

	got, _ := windowSvc.Get(context.Background(), w.ID)
	if got.Status != domain.WindowStatusEffective {
		t.Errorf("expected effective, got %s", got.Status)
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
