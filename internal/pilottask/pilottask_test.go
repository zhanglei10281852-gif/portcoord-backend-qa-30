package pilottask

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
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
	svc := New(Deps{
		Tasks: st, Leases: st, Executions: st,
		Audit: audit.New(st, clock), Clock: clock, Logger: apperr.NopLogger(),
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

func mustCreateAssignedTask(t *testing.T, svc *Service, st *store.SQLiteStore, id string) *domain.PilotTugTask {
	t.Helper()
	mustCreateDecl(t, st, "decl-"+id)
	task, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-" + id, TaskType: domain.PTTypePilot,
		Location: "Berth-1", Priority: 5,
	})
	svc.Assign(context.Background(), id, "dispatcher", "d", "r")
	return task
}

func TestPilotTask_Create(t *testing.T) {
	svc, st, _, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-pt1")

	task, err := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt1", TaskType: domain.PTTypePilot,
		Location: "Berth-1", Priority: 5,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if task.Status != domain.PTStatusCreated {
		t.Errorf("expected created, got %s", task.Status)
	}
}

func TestPilotTask_ClaimAndReport(t *testing.T) {
	svc, st, _, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-pt2")

	task, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt2", TaskType: domain.PTTypeTug,
		Location: "Berth-2", Priority: 5,
	})
	svc.Assign(context.Background(), task.ID, "assignee", "d", "r")

	claim, err := svc.Claim(context.Background(), ClaimRequest{
		TaskID: task.ID, ExecutorID: "executor-1",
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.LeaseID == "" {
		t.Error("expected non-empty lease ID")
	}

	err = svc.Report(context.Background(), ReportRequest{
		TaskID: task.ID, ExecutorID: "executor-1", Result: "completed",
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	got, _ := svc.Get(context.Background(), task.ID)
	if got.Status != domain.PTStatusCompleted {
		t.Errorf("expected completed, got %s", got.Status)
	}
}

func TestPilotTask_ClaimWrongExecutorReject(t *testing.T) {
	svc, st, _, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-pt3")

	task, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt3", TaskType: domain.PTTypePilot,
		Location: "Berth-1", Priority: 5,
	})
	svc.Assign(context.Background(), task.ID, "assignee", "d", "r")

	svc.Claim(context.Background(), ClaimRequest{
		TaskID: task.ID, ExecutorID: "executor-1",
	})

	// Different executor tries to report.
	err := svc.Report(context.Background(), ReportRequest{
		TaskID: task.ID, ExecutorID: "executor-2", Result: "completed",
	})
	if err == nil {
		t.Fatal("expected error reporting with wrong executor")
	}
}

func TestPilotTask_ClaimIllegalTransitionReject(t *testing.T) {
	svc, st, _, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-pt4")

	task, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt4", TaskType: domain.PTTypePilot,
		Location: "Berth-1", Priority: 5,
	})
	// Can't claim a created task — must be assigned first.
	_, err := svc.Claim(context.Background(), ClaimRequest{
		TaskID: task.ID, ExecutorID: "exec",
	})
	if err == nil {
		t.Fatal("expected error claiming created task")
	}
	if !apperr.IsInvalidTransition(err) {
		t.Errorf("expected invalid transition, got %v", err)
	}
}

func TestPilotTask_PreemptExpiredClaim(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-pt5")

	task, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt5", TaskType: domain.PTTypeTug,
		Location: "Berth-1", Priority: 5,
	})
	svc.Assign(context.Background(), task.ID, "assignee", "d", "r")
	svc.Claim(context.Background(), ClaimRequest{
		TaskID: task.ID, ExecutorID: "executor-1",
	})

	// Advance clock past lease expiry.
	clock.Advance(2 * time.Hour)

	results, err := svc.PreemptExpiredClaims(context.Background())
	if err != nil {
		t.Fatalf("preempt: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 preempted, got %d", len(results))
	}
	if results[0].PrevExecutor != "executor-1" {
		t.Errorf("expected executor-1, got %s", results[0].PrevExecutor)
	}

	got, _ := svc.Get(context.Background(), task.ID)
	if got.Status != domain.PTStatusPreempted {
		t.Errorf("expected preempted, got %s", got.Status)
	}
	if got.ClaimedBy != "" {
		t.Errorf("expected empty claimed_by, got %s", got.ClaimedBy)
	}
}

func TestPilotTask_ReassignAfterPreempt(t *testing.T) {
	svc, st, clock, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-pt6")

	task, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt6", TaskType: domain.PTTypePilot,
		Location: "Berth-1", Priority: 5,
	})
	svc.Assign(context.Background(), task.ID, "assignee", "d", "r")
	svc.Claim(context.Background(), ClaimRequest{TaskID: task.ID, ExecutorID: "exec-1"})
	clock.Advance(2 * time.Hour)
	svc.PreemptExpiredClaims(context.Background())

	err := svc.Reassign(context.Background(), task.ID, "scheduler", "r")
	if err != nil {
		t.Fatalf("reassign: %v", err)
	}
	got, _ := svc.Get(context.Background(), task.ID)
	if got.Status != domain.PTStatusAssigned {
		t.Errorf("expected assigned, got %s", got.Status)
	}
}

func TestPilotTask_ConcurrentClaimRace(t *testing.T) {
	svc, st, _, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-pt7")

	task, _ := svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt7", TaskType: domain.PTTypePilot,
		Location: "Berth-1", Priority: 5,
	})
	svc.Assign(context.Background(), task.ID, "assignee", "d", "r")

	var wg sync.WaitGroup
	var success int32
	workers := 10
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := svc.Claim(context.Background(), ClaimRequest{
				TaskID: task.ID, ExecutorID: "exec",
			})
			if err == nil {
				atomic.AddInt32(&success, 1)
			}
		}(i)
	}
	wg.Wait()

	if success != 1 {
		t.Errorf("expected exactly 1 successful claim, got %d", success)
	}
}

func TestPilotTask_GetNotFound(t *testing.T) {
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

func TestPilotTask_Backlog(t *testing.T) {
	svc, st, _, cleanup := setupTest(t)
	defer cleanup()
	mustCreateDecl(t, st, "decl-pt8")
	mustCreateDecl(t, st, "decl-pt9")
	svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt8", TaskType: domain.PTTypePilot,
		Location: "B1", Priority: 5,
	})
	svc.Create(context.Background(), CreateRequest{
		DeclarationID: "decl-pt9", TaskType: domain.PTTypeTug,
		Location: "B2", Priority: 5,
	})

	backlog, err := svc.Backlog(context.Background())
	if err != nil {
		t.Fatalf("backlog: %v", err)
	}
	if backlog.Created != 2 {
		t.Errorf("expected 2 created, got %d", backlog.Created)
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
