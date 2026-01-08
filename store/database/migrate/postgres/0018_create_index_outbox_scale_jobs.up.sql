-- Create partial unique index for efficient scale job deduplication per pool
-- This index only includes scale jobs and indexes on pool_name and window_start from job_params
CREATE UNIQUE INDEX IF NOT EXISTS outbox_scale_job_pool_window_idx 
    ON outbox_jobs (pool_name, (job_params->>'window_start'))
    WHERE job_type = 'scale';

