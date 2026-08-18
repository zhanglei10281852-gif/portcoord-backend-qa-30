# PortCoord Backend

PortCoord is a pure Go backend for coordinating arrival declarations, berthing windows, cargo work orders, pilot and tug tasks, capacity quotas, and responsibility handovers between port operators. It is designed as a production-style baseline for later Go coding-agent tasks. This baseline contains no intentionally seeded defect or private answer material.

## Architecture

The service uses three processes over a shared SQLite database:

- `cmd/scheduler`: HTTP API and deadline-driven scheduling engine.
- `cmd/executor`: persistent task claimant and execution reporter.
- `cmd/migrate`: explicit database migration command.

Business code is split by responsibility:

```text
cmd/                   process entry points
internal/domain/       entities, state machines, pagination, domain errors
internal/declaration/  arrival declaration and idempotency workflow
internal/berthing/     berthing allocation, deadlines, escalation
internal/workorder/    cargo work-order lifecycle
internal/pilottask/    task assignment, leasing, claiming, reporting
internal/quota/        daily cabin and yard quota reservation
internal/handover/     responsibility transfer documents
internal/engine/       scheduling loop and overdue recovery
internal/worker/       executor polling and task processing
internal/audit/        durable audit events
internal/store/        repository contracts, SQLite SQL, transactions
internal/server/       HTTP routing, handlers, middleware, JSON contracts
internal/config/       environment configuration and validation
migrations/            versioned SQLite schema
```

The domain layer does not depend on HTTP or SQLite. Handlers invoke business services, and services access persistence through repository interfaces. Cross-entity updates use the transaction context supplied by `Store.InTx`.

## Data Model

The initial migration creates the following related tables:

- `arrival_declarations`: vessel arrival intent, priority, queue position, and optimistic version.
- `berthing_windows`: assigned berth and effective/deadline window for a declaration.
- `work_orders`: cargo operations linked to a declaration and optional berthing window.
- `pilot_tug_tasks`: leased pilot/tug execution tasks for a declaration or window.
- `quotas`: unique daily cabin and yard capacity limits with reserved/used amounts.
- `handover_documents`: responsibility transfers for declaration workflows.
- `task_leases`: executor ownership and expiry for claimable tasks.
- `execution_records`: durable task execution outcomes.
- `audit_logs`: actor, action, object, request, and before/after state.
- `escalation_records`: deadline escalation history.
- `idempotency_records`: replayable HTTP results with expiry.
- `schema_migrations`: applied migration versions.

Foreign keys, unique constraints, business indexes, timestamps, and optimistic version columns are defined in `migrations/001_initial_schema.sql`. SQLite runs with foreign keys, WAL mode, a busy timeout, and bounded connection pooling.

## Configuration

Use `.env.example` as the environment-variable reference:

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORTCOORD_PORT` | `58552` | HTTP listen port |
| `PORTCOORD_DATA_DIR` | `./data` | SQLite data directory |
| `PORTCOORD_DB_NAME` | `portcoord.db` | Database filename |
| `PORTCOORD_REQUEST_TIMEOUT` | `30` | HTTP timeout in seconds |
| `PORTCOORD_SCHEDULER_INTERVAL` | `5` | Scheduler interval in seconds |
| `PORTCOORD_LEASE_TIMEOUT` | `60` | Task lease duration in seconds |
| `PORTCOORD_EXECUTOR_INTERVAL` | `3` | Executor poll interval in seconds |
| `PORTCOORD_EXECUTOR_ID` | `executor-1` | Stable executor identity |
| `PORTCOORD_LOG_LEVEL` | `info` | Structured log level |

No credentials or external online service are required.

## Run Locally

Go 1.26 or later is required.

```bash
go mod download
go run ./cmd/migrate
go run ./cmd/scheduler
```

Run the executor in another terminal after the database has been migrated:

```bash
go run ./cmd/executor
```

The scheduler applies migrations on startup as well, so the explicit migration command is useful for deployment checks but is not mandatory.

Health and readiness endpoints:

```bash
curl -s http://localhost:58552/health
curl -s http://localhost:58552/ready
```

## Main API

All business endpoints are under `/api/v1`:

- `POST /declarations`, `GET /declarations`, `PUT /declarations/{id}/cancel`
- `POST /berthing-windows`, `POST /berthing-windows/batch`, `PUT /berthing-windows/{id}/release`
- `POST /work-orders`, then `/assign`, `/start`, `/complete`, or `/cancel`
- `POST /pilot-tasks`, then `/assign`, `/claim`, or `/report`
- `POST /quotas/reserve`, then `/quotas/{id}/commit` or `/release`
- `POST /handover-documents`, then `/confirm` or `/reject`
- `GET /audit-logs`, `/escalations`, `/backlog`, `/executions`
- `GET /export/reconciliation` and `POST /intervene`

Responses use JSON. Middleware supplies request IDs, structured access logs, panic recovery, CORS policy, and request timeouts. Domain errors are mapped to stable HTTP error codes.

Example declaration request:

```bash
curl -sS -X POST http://localhost:58552/api/v1/declarations \
  -H 'Content-Type: application/json' \
  -d '{
    "ship_name":"Hai Xing",
    "imo_number":"IMO9387421",
    "voyage_number":"HX-2026-08",
    "eta":"2026-08-20T08:00:00Z",
    "cargo_type":"container",
    "cargo_volume":240,
    "cargo_unit":"TEU",
    "declared_by":"agent-17",
    "declaring_party":"ship_agent",
    "idempotency_key":"arrival-demo-001"
  }'
```

## Tests

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./...
```

Tests cover state transitions, invalid transitions, idempotency, quota boundaries, transaction rollback, optimistic conflicts, concurrent claims, worker cancellation, deadline escalation, pagination, HTTP error contracts, migrations, and reopening a persisted database.

## Docker

Build and run the scheduler:

```bash
docker build -t portcoord-backend .
docker run --rm -p 58552:58552 -v portcoord-data:/app/data portcoord-backend
```

The image includes the scheduler, executor, and migration binaries under `/app/bin`. Override the default command with `executor` or `migrate` when running those process roles.
