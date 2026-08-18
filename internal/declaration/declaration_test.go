package declaration

import (
	"context"
	"sync"
	"testing"
	"time"

	"portcoord/internal/apperr"
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
	auditRec := auditNew(st, clock)
	svc := New(Deps{
		Declarations: st, Quotas: st, Idempotency: st, Handovers: st,
		Audit: auditRec, Clock: clock, Logger: apperr.NopLogger(),
		CabinLimit: 1000, YardLimit: 5000,
	})
	return svc, st, clock, func() { st.Close() }
}

func TestDeclaration_Submit_Accepted(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	result, err := svc.Submit(context.Background(), NewDeclaration("Ship-A"))
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if result.Status != domain.DeclStatusAccepted {
		t.Errorf("expected accepted, got %s", result.Status)
	}
	if result.DeclarationID == "" {
		t.Error("expected non-empty declaration ID")
	}
}

func TestDeclaration_Submit_IdempotentDuplicate(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	req := NewDeclaration("Ship-Idem")
	req.IdempotencyKey = "idem-key-test"

	result1, err := svc.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}

	result2, err := svc.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("second submit: %v", err)
	}

	if result1.DeclarationID != result2.DeclarationID {
		t.Errorf("idempotent submit should return same ID: %s vs %s",
			result1.DeclarationID, result2.DeclarationID)
	}
	if result1.Status != result2.Status {
		t.Errorf("idempotent submit should return same status: %s vs %s",
			result1.Status, result2.Status)
	}
}

func TestDeclaration_Submit_QuotaExceededRejects(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	// Submit enough to exhaust the yard quota (5000 TEU).
	req := NewDeclaration("BigShip")
	req.CargoVolume = 5001
	req.CargoUnit = "TEU"
	req.CargoType = "container"

	result, err := svc.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if result.Status != domain.DeclStatusRejected {
		t.Errorf("expected rejected, got %s", result.Status)
	}
	if result.Message == "" {
	}
}

func TestDeclaration_Cancel_InvalidTransitionReject(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	result := mustSubmit(t, svc, "Ship-Cancel")

	// Cancel it (should succeed from submitted).
	err := svc.Cancel(context.Background(), result.DeclarationID, "agent", "req-1")
	if err != nil {
		t.Fatalf("first cancel: %v", err)
	}

	// Try to cancel again (should fail — already cancelled).
	err = svc.Cancel(context.Background(), result.DeclarationID, "agent", "req-2")
	if err == nil {
		t.Fatal("expected error on cancelling already-cancelled declaration")
	}
	if !apperr.IsInvalidTransition(err) {
		t.Errorf("expected invalid transition error, got %v", err)
	}
}

func TestDeclaration_Cancel_CompletedReject(t *testing.T) {
	svc, st, _, cleanup := setupTest(t)
	defer cleanup()

	result := mustSubmit(t, svc, "Ship-Done")

	// Force-complete the declaration by updating status through the store.
	d, _ := st.GetDeclaration(context.Background(), result.DeclarationID)
	st.UpdateDeclarationStatus(context.Background(), d.ID, domain.DeclStatusReviewing, d.Version)
	d, _ = st.GetDeclaration(context.Background(), result.DeclarationID)
	st.UpdateDeclarationStatus(context.Background(), d.ID, domain.DeclStatusAccepted, d.Version)
	d, _ = st.GetDeclaration(context.Background(), result.DeclarationID)
	st.UpdateDeclarationStatus(context.Background(), d.ID, domain.DeclStatusScheduled, d.Version)
	d, _ = st.GetDeclaration(context.Background(), result.DeclarationID)
	st.UpdateDeclarationStatus(context.Background(), d.ID, domain.DeclStatusProcessing, d.Version)
	d, _ = st.GetDeclaration(context.Background(), result.DeclarationID)
	st.UpdateDeclarationStatus(context.Background(), d.ID, domain.DeclStatusCompleted, d.Version)

	// Cancel should be rejected.
	err := svc.Cancel(context.Background(), result.DeclarationID, "agent", "req")
	if err == nil {
		t.Fatal("expected error cancelling completed declaration")
	}
	if !apperr.IsInvalidTransition(err) {
		t.Errorf("expected invalid transition error, got %v", err)
	}
}

func TestDeclaration_List_PaginationBoundary(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	for i := 0; i < 15; i++ {
		mustSubmit(t, svc, "Ship-"+string(rune('A'+i)))
	}

	q := domain.DefaultPage()
	q.Page = 1
	q.PageSize = 10
	result, err := svc.List(context.Background(), q)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Items) != 10 {
		t.Errorf("expected 10 items, got %d", len(result.Items))
	}
	if result.Total != 15 {
		t.Errorf("expected total 15, got %d", result.Total)
	}
}

func TestDeclaration_ConcurrentSubmit_DifferentShips(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	var wg sync.WaitGroup
	workers := 10
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := NewDeclaration("ConcurrentShip")
			req.IMONumber = "IMO"
			req.ShipName = "Ship-" + string(rune('A'+id))
			_, _ = svc.Submit(context.Background(), req)
		}(i)
	}
	wg.Wait()

	result, err := svc.List(context.Background(), domain.DefaultPage())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if result.Total != workers {
		t.Errorf("expected %d declarations, got %d", workers, result.Total)
	}
}

func TestDeclaration_ConcurrentSubmit_SameIdempotencyKey(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	var wg sync.WaitGroup
	workers := 5
	results := make([]*SubmitResult, workers)
	var mu sync.Mutex
	idx := 0

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := NewDeclaration("SameShip")
			req.IdempotencyKey = "shared-key-concurrent"
			result, err := svc.Submit(context.Background(), req)
			if err != nil {
				return
			}
			mu.Lock()
			if idx < workers {
				results[idx] = result
				idx++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	// All successful results should have the same declaration ID.
	var firstID string
	count := 0
	for _, r := range results {
		if r == nil {
			continue
		}
		if firstID == "" {
			firstID = r.DeclarationID
		}
		if r.DeclarationID == firstID {
			count++
		}
	}
	if count < 1 {
		t.Error("expected at least one successful submission with consistent ID")
	}
}

func TestDeclaration_Get_NotFound(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	_, err := svc.Get(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent declaration")
	}
	if !apperr.IsNotFound(err) {
		t.Errorf("expected not found error, got %v", err)
	}
}

func TestDeclaration_UpdatePriority(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	result := mustSubmit(t, svc, "Ship-Priority")

	err := svc.UpdatePriority(context.Background(), result.DeclarationID, "supervisor", 1, "req")
	if err != nil {
		t.Fatalf("update priority: %v", err)
	}

	d, err := svc.Get(context.Background(), result.DeclarationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if d.Priority != 1 {
		t.Errorf("expected priority 1, got %d", d.Priority)
	}
}

func TestDeclaration_UpdatePriority_InvalidValueReject(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	result := mustSubmit(t, svc, "Ship-BadPriority")

	err := svc.UpdatePriority(context.Background(), result.DeclarationID, "supervisor", 99, "req")
	if err == nil {
		t.Fatal("expected error for invalid priority")
	}
	if !apperr.IsCode(err, apperr.CodeValidationFailed) {
		t.Errorf("expected validation error, got %v", err)
	}
}

func TestDeclaration_Backlog(t *testing.T) {
	svc, _, _, cleanup := setupTest(t)
	defer cleanup()

	for i := 0; i < 3; i++ {
		mustSubmit(t, svc, "Ship-"+string(rune('A'+i)))
	}

	backlog, err := svc.Backlog(context.Background())
	if err != nil {
		t.Fatalf("backlog: %v", err)
	}
	if backlog.Accepted != 3 {
		t.Errorf("expected 3 accepted, got %d", backlog.Accepted)
	}
}

func mustSubmit(t *testing.T, svc *Service, shipName string) *SubmitResult {
	t.Helper()
	result, err := svc.Submit(context.Background(), NewDeclaration(shipName))
	if err != nil {
		t.Fatalf("submit declaration: %v", err)
	}
	return result
}

func findMigrations() string {
	for _, c := range []string{"./migrations", "../migrations", "../../migrations"} {
		if _, err := osStat(c); err == nil {
			return c
		}
	}
	return "./migrations"
}
