ALTER TABLE instances ADD COLUMN IF NOT EXISTS instance_source VARCHAR(20) DEFAULT 'unknown';
