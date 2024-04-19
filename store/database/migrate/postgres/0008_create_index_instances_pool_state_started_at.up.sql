-- Uses btree to create index
-- Need this for purger
CREATE INDEX IF NOT EXISTS INSTANCE_POOL_STATE_STARTED_INDEX ON instances(instance_pool, instance_state, instance_started);