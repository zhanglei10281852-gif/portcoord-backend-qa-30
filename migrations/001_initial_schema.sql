-- Port Coordination Service - Initial Schema Migration
-- Version: 1

PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    description TEXT NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Arrival declarations (到港申报)
CREATE TABLE IF NOT EXISTS arrival_declarations (
    id TEXT PRIMARY KEY,
    ship_name TEXT NOT NULL,
    imo_number TEXT NOT NULL,
    voyage_number TEXT NOT NULL,
    eta TEXT NOT NULL,
    berth_preference TEXT NOT NULL DEFAULT '',
    cargo_type TEXT NOT NULL,
    cargo_volume INTEGER NOT NULL DEFAULT 0,
    cargo_unit TEXT NOT NULL DEFAULT 'TEU',
    declared_by TEXT NOT NULL,
    declaring_party TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'submitted',
    priority INTEGER NOT NULL DEFAULT 5,
    queue_position INTEGER NOT NULL DEFAULT 0,
    idempotency_key TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_declarations_status ON arrival_declarations(status);
CREATE INDEX IF NOT EXISTS idx_declarations_eta ON arrival_declarations(eta);
CREATE UNIQUE INDEX IF NOT EXISTS idx_declarations_idem ON arrival_declarations(idempotency_key) WHERE idempotency_key != '';
CREATE INDEX IF NOT EXISTS idx_declarations_party ON arrival_declarations(declaring_party);

-- Berthing windows (靠泊窗口)
CREATE TABLE IF NOT EXISTS berthing_windows (
    id TEXT PRIMARY KEY,
    declaration_id TEXT NOT NULL,
    berth_number TEXT NOT NULL,
    ship_name TEXT NOT NULL,
    effective_at TEXT NOT NULL,
    deadline_at TEXT NOT NULL,
    assigned_to TEXT NOT NULL DEFAULT '',
    responsible_party TEXT NOT NULL,
    escalation_level INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'allocated',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (declaration_id) REFERENCES arrival_declarations(id)
);

CREATE INDEX IF NOT EXISTS idx_windows_status ON berthing_windows(status);
CREATE INDEX IF NOT EXISTS idx_windows_effective ON berthing_windows(effective_at);
CREATE INDEX IF NOT EXISTS idx_windows_deadline ON berthing_windows(deadline_at);
CREATE INDEX IF NOT EXISTS idx_windows_berth ON berthing_windows(berth_number);

-- Work orders (装卸作业单)
CREATE TABLE IF NOT EXISTS work_orders (
    id TEXT PRIMARY KEY,
    declaration_id TEXT NOT NULL,
    berthing_window_id TEXT,
    order_type TEXT NOT NULL,
    cargo_type TEXT NOT NULL,
    planned_volume INTEGER NOT NULL,
    actual_volume INTEGER NOT NULL DEFAULT 0,
    assigned_to TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'created',
    started_at TEXT,
    completed_at TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (declaration_id) REFERENCES arrival_declarations(id),
    FOREIGN KEY (berthing_window_id) REFERENCES berthing_windows(id)
);

CREATE INDEX IF NOT EXISTS idx_workorders_status ON work_orders(status);
CREATE INDEX IF NOT EXISTS idx_workorders_decl ON work_orders(declaration_id);

-- Pilot tug tasks (引航拖轮任务)
CREATE TABLE IF NOT EXISTS pilot_tug_tasks (
    id TEXT PRIMARY KEY,
    declaration_id TEXT NOT NULL,
    berthing_window_id TEXT,
    task_type TEXT NOT NULL,
    location TEXT NOT NULL,
    assigned_to TEXT NOT NULL DEFAULT '',
    claimed_by TEXT NOT NULL DEFAULT '',
    claim_expires_at TEXT,
    lease_id TEXT,
    status TEXT NOT NULL DEFAULT 'created',
    priority INTEGER NOT NULL DEFAULT 5,
    report_data TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (declaration_id) REFERENCES arrival_declarations(id),
    FOREIGN KEY (berthing_window_id) REFERENCES berthing_windows(id)
);

CREATE INDEX IF NOT EXISTS idx_pilottasks_status ON pilot_tug_tasks(status);
CREATE INDEX IF NOT EXISTS idx_pilottasks_type ON pilot_tug_tasks(task_type);
CREATE INDEX IF NOT EXISTS idx_pilottasks_claim ON pilot_tug_tasks(claimed_by);

-- Quotas (舱位与堆场额度)
CREATE TABLE IF NOT EXISTS quotas (
    id TEXT PRIMARY KEY,
    quota_type TEXT NOT NULL,
    period_date TEXT NOT NULL,
    daily_limit INTEGER NOT NULL,
    used_amount INTEGER NOT NULL DEFAULT 0,
    reserved_amount INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'available',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(quota_type, period_date)
);

CREATE INDEX IF NOT EXISTS idx_quotas_type ON quotas(quota_type);
CREATE INDEX IF NOT EXISTS idx_quotas_period ON quotas(period_date);

-- Handover documents (交接单)
CREATE TABLE IF NOT EXISTS handover_documents (
    id TEXT PRIMARY KEY,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    from_party TEXT NOT NULL,
    to_party TEXT NOT NULL,
    action TEXT NOT NULL,
    document_ref TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    notes TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (entity_id) REFERENCES arrival_declarations(id)
);

CREATE INDEX IF NOT EXISTS idx_handover_entity ON handover_documents(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_handover_status ON handover_documents(status);

-- Task leases (执行租约)
CREATE TABLE IF NOT EXISTS task_leases (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    task_id TEXT NOT NULL,
    executor_id TEXT NOT NULL,
    claimed_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    revoked_reason TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_leases_task ON task_leases(task_type, task_id);
CREATE INDEX IF NOT EXISTS idx_leases_status ON task_leases(status);
CREATE INDEX IF NOT EXISTS idx_leases_expires ON task_leases(expires_at);

-- Audit logs (审计记录)
CREATE TABLE IF NOT EXISTS audit_logs (
    id TEXT PRIMARY KEY,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    before_state TEXT NOT NULL DEFAULT '',
    after_state TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    timestamp TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_entity ON audit_logs(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_audit_actor ON audit_logs(actor);
CREATE INDEX IF NOT EXISTS idx_audit_time ON audit_logs(timestamp);

-- Escalation records (升级记录)
CREATE TABLE IF NOT EXISTS escalation_records (
    id TEXT PRIMARY KEY,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    from_level INTEGER NOT NULL,
    to_level INTEGER NOT NULL,
    reason TEXT NOT NULL,
    resolved_by TEXT NOT NULL DEFAULT '',
    resolved_at TEXT,
    timestamp TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_escalation_entity ON escalation_records(entity_type, entity_id);

-- Idempotency records (幂等记录)
CREATE TABLE IF NOT EXISTS idempotency_records (
    key TEXT PRIMARY KEY,
    response_body TEXT NOT NULL,
    response_status INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_idempotency_expires ON idempotency_records(expires_at);

-- Execution records (执行记录)
CREATE TABLE IF NOT EXISTS execution_records (
    id TEXT PRIMARY KEY,
    task_type TEXT NOT NULL,
    task_id TEXT NOT NULL,
    executor_id TEXT NOT NULL,
    result TEXT NOT NULL,
    error_message TEXT NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    timestamp TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES pilot_tug_tasks(id)
);

CREATE INDEX IF NOT EXISTS idx_exec_task ON execution_records(task_type, task_id);
CREATE INDEX IF NOT EXISTS idx_exec_executor ON execution_records(executor_id);

