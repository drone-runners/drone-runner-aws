CREATE TABLE IF NOT EXISTS instance_utilization_history (
    id SERIAL PRIMARY KEY,
    pool_name VARCHAR(255) NOT NULL,
    variant_id VARCHAR(255) NOT NULL DEFAULT '',
    in_use_instances INTEGER NOT NULL DEFAULT 0,
    recorded_at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_utilization_history_pool_variant_time 
    ON instance_utilization_history (pool_name, variant_id, recorded_at);
