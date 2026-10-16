CREATE TABLE IF NOT EXISTS capability_utilization_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tier TEXT NOT NULL,
    os TEXT NOT NULL,
    arch TEXT NOT NULL,
    variant_id TEXT NOT NULL DEFAULT 'default',
    tenant_id TEXT NOT NULL DEFAULT 'default',
    pool_name TEXT NOT NULL,
    image_name TEXT,
    in_use_instances INTEGER NOT NULL DEFAULT 0,
    recorded_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_capability_history_key_time
    ON capability_utilization_history (tier, os, arch, variant_id, tenant_id, recorded_at);

CREATE INDEX IF NOT EXISTS idx_capability_history_pool_time
    ON capability_utilization_history (pool_name, recorded_at);

CREATE INDEX IF NOT EXISTS idx_capability_history_time
    ON capability_utilization_history (recorded_at);
