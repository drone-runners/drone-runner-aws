ALTER TABLE instance_utilization_history ADD COLUMN IF NOT EXISTS tenant_id VARCHAR(255) NOT NULL DEFAULT 'default';

DROP INDEX IF EXISTS idx_utilization_history_pool_variant_image_time;

CREATE INDEX IF NOT EXISTS idx_utilization_history_pool_tenant_variant_image_time
    ON instance_utilization_history (pool_name, tenant_id, variant_id, image_name, recorded_at);
