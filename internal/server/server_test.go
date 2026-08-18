package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"portcoord/internal/apperr"
	"portcoord/internal/config"
	"portcoord/internal/domain"
	"portcoord/internal/store"
)

func setupTestServer(t *testing.T) (*Server, *store.SQLiteStore, *apperr.FakeClock, func()) {
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
	cfg := config.Default()
	srv := New(Deps{Cfg: cfg, Store: st, Clock: clock, Logger: apperr.NopLogger()})
	return srv, st, clock, func() { st.Close() }
}

func createTestDecl(id string, now time.Time) *domain.ArrivalDeclaration {
	return &domain.ArrivalDeclaration{
		ID: id, ShipName: "TestShip", IMONumber: "IMO123", VoyageNumber: "V001",
		ETA: now.Add(24 * time.Hour), CargoType: "containers", CargoVolume: 10,
		CargoUnit: "TEU", DeclaredBy: "agent-1", DeclaringParty: domain.PartyShipOwner,
		Status: domain.DeclStatusAccepted, Priority: 5, Version: 1,
		CreatedAt: now, UpdatedAt: now, IdempotencyKey: "key-" + id,
	}
}

func TestServer_HealthEndpoint(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.HealthHandler()(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "healthy" {
		t.Errorf("expected healthy, got %s", resp["status"])
	}
}

func TestServer_SubmitDeclaration(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{
		"ship_name": "TestShip",
		"imo_number": "IMO123",
		"voyage_number": "V001",
		"eta": "2026-01-15T10:00:00Z",
		"cargo_type": "containers",
		"cargo_volume": 10,
		"cargo_unit": "TEU",
		"declared_by": "agent-1",
		"declaring_party": "ship_owner",
		"priority": 5,
		"idempotency_key": "idem-test-1"
	}`
	req := httptest.NewRequest("POST", "/api/v1/declarations", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_ErrorResponseIncludesRequestID(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("POST", "/api/v1/declarations", bytes.NewBufferString("{"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "req-contract-001")
	w := httptest.NewRecorder()
	srv.buildHandler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-Request-ID"); got != "req-contract-001" {
		t.Fatalf("expected response request ID, got %q", got)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.Code != string(apperr.CodeValidationFailed) {
		t.Errorf("expected validation code, got %q", resp.Code)
	}
	if resp.RequestID != "req-contract-001" {
		t.Errorf("expected request_id in body, got %q", resp.RequestID)
	}
}

func TestServer_ListDeclarationsPagination(t *testing.T) {
	srv, st, _, cleanup := setupTestServer(t)
	defer cleanup()

	for i := 0; i < 5; i++ {
		now := time.Now().UTC()
		d := createTestDecl("d-"+string(rune('A'+i)), now)
		st.CreateDeclaration(context.Background(), d)
	}

	req := httptest.NewRequest("GET", "/api/v1/declarations?page=1&page_size=2", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	meta := resp["meta"].(map[string]any)
	if meta["total"].(float64) != 5 {
		t.Errorf("expected total 5, got %v", meta["total"])
	}
}

func TestServer_GetDeclarationNotFound(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/v1/declarations/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestServer_CreateBerthingWindow(t *testing.T) {
	srv, st, clock, cleanup := setupTestServer(t)
	defer cleanup()
	now := time.Now().UTC()
	d := createTestDecl("decl-bw1", now)
	st.CreateDeclaration(context.Background(), d)

	body := `{
		"declaration_id": "decl-bw1",
		"berth_number": "B1",
		"ship_name": "TestShip",
		"effective_at": "` + clock.Now().Format(time.RFC3339) + `",
		"deadline_at": "` + clock.Now().Add(24*time.Hour).Format(time.RFC3339) + `",
		"responsible_party": "terminal_dispatch"
	}`
	req := httptest.NewRequest("POST", "/api/v1/berthing-windows", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_CancelDeclarationInvalidTransition(t *testing.T) {
	srv, st, _, cleanup := setupTestServer(t)
	defer cleanup()
	now := time.Now().UTC()
	d := createTestDecl("decl-cancel1", now)
	d.Status = domain.DeclStatusCancelled
	st.CreateDeclaration(context.Background(), d)

	req := httptest.NewRequest("PUT", "/api/v1/declarations/decl-cancel1/cancel", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", w.Code)
	}
}

func TestServer_QuotaReserve(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"quota_type": "cabin", "amount": 100}`
	req := httptest.NewRequest("POST", "/api/v1/quotas/reserve", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestServer_QuotaExceedBackpressure(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{"quota_type": "cabin", "amount": 99999}`
	req := httptest.NewRequest("POST", "/api/v1/quotas/reserve", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", w.Code)
	}
}

func TestServer_BacklogEndpoint(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/v1/backlog", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestServer_ExportReconciliation(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/v1/export/reconciliation", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "text/csv" {
		t.Errorf("expected text/csv, got %s", w.Header().Get("Content-Type"))
	}
}

func TestServer_AuditLogs(t *testing.T) {
	srv, st, _, cleanup := setupTestServer(t)
	defer cleanup()

	now := time.Now().UTC()
	entry := &domain.AuditLog{
		ID: "audit-1", Actor: "agent-1", Action: "submit",
		EntityType: domain.EntityDeclaration, EntityID: "decl-1",
		Timestamp: now,
	}
	st.InsertAudit(context.Background(), entry)

	req := httptest.NewRequest("GET", "/api/v1/audit-logs", nil)
	w := httptest.NewRecorder()
	srv.Router().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestServer_IdempotentDuplicateSubmit(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	body := `{
		"ship_name": "IdemShip",
		"imo_number": "IMO456",
		"voyage_number": "V002",
		"eta": "2026-01-15T10:00:00Z",
		"cargo_type": "containers",
		"cargo_volume": 10,
		"cargo_unit": "TEU",
		"declared_by": "agent-1",
		"declaring_party": "ship_owner",
		"priority": 5,
		"idempotency_key": "idem-dup-key"
	}`

	req1 := httptest.NewRequest("POST", "/api/v1/declarations", bytes.NewBufferString(body))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	srv.Router().ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first submit: expected 201, got %d", w1.Code)
	}

	var resp1 map[string]any
	json.NewDecoder(w1.Body).Decode(&resp1)
	id1 := resp1["declaration_id"].(string)

	req2 := httptest.NewRequest("POST", "/api/v1/declarations", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	srv.Router().ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("second submit: expected 201, got %d", w2.Code)
	}

	var resp2 map[string]any
	json.NewDecoder(w2.Body).Decode(&resp2)
	id2 := resp2["declaration_id"].(string)

	if id1 != id2 {
		t.Errorf("idempotent submit should return same ID: %s vs %s", id1, id2)
	}
}

func TestServer_PersistRestartRecover(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/restart.db"
	migrations := findMigrations()

	db1, _ := store.Open(dbPath, migrations)
	st1 := store.NewSQLiteStore(db1)
	now := time.Now().UTC()
	d := createTestDecl("decl-restart-srv", now)
	st1.CreateDeclaration(context.Background(), d)
	st1.Close()

	db2, _ := store.Open(dbPath, migrations)
	st2 := store.NewSQLiteStore(db2)
	defer st2.Close()
	got, err := st2.GetDeclaration(context.Background(), "decl-restart-srv")
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.ShipName != "TestShip" {
		t.Errorf("expected TestShip, got %s", got.ShipName)
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
