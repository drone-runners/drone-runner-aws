ALTER TABLE instance_utilization_history ADD COLUMN IF NOT EXISTS image_name VARCHAR(512) NOT NULL DEFAULT '';

DROP INDEX IF EXISTS idx_utilization_history_pool_variant_time;

CREATE INDEX IF NOT EXISTS idx_utilization_history_pool_variant_image_time
    ON instance_utilization_history (pool_name, variant_id, image_name, recorded_at);
