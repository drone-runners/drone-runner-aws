CREATE TABLE IF NOT EXISTS capability_utilization_history (
    id BIGSERIAL PRIMARY KEY,
    tier VARCHAR(50) NOT NULL,
    os VARCHAR(50) NOT NULL,
    arch VARCHAR(50) NOT NULL,
    variant_id VARCHAR(255) NOT NULL DEFAULT 'default',
    tenant_id VARCHAR(255) NOT NULL DEFAULT 'default',
    pool_name VARCHAR(255) NOT NULL,
    image_name VARCHAR(512),
    in_use_instances INTEGER NOT NULL DEFAULT 0,
    recorded_at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_capability_history_key_time
    ON capability_utilization_history (tier, os, arch, variant_id, tenant_id, recorded_at);

CREATE INDEX IF NOT EXISTS idx_capability_history_pool_time
    ON capability_utilization_history (pool_name, recorded_at);

CREATE INDEX IF NOT EXISTS idx_capability_history_time
    ON capability_utilization_history (recorded_at);
