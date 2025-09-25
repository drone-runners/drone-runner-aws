CREATE TABLE IF NOT EXISTS outbox_jobs (
    id SERIAL PRIMARY KEY,
    pool_name VARCHAR(255) NOT NULL,
    runner_name VARCHAR(255) NOT NULL,
    job_type VARCHAR(50) NOT NULL,
    job_params JSONB NULL,
    created_at INTEGER NOT NULL,
    processed_at INTEGER,
    status VARCHAR(20) DEFAULT 'pending',
    error_message TEXT,
    retry_count INTEGER DEFAULT 0
);

-- Create index for efficient job lookup by runner and type
CREATE INDEX outbox_runner_status_type_processed_at_idx ON outbox_jobs (runner_name, status, job_type, processed_at);
