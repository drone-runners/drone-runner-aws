CREATE TABLE IF NOT EXISTS capacity_reservation (
     stage_id          VARCHAR(250) PRIMARY KEY
    ,pool_name         VARCHAR(250)
    ,instance_id       VARCHAR(250)
    ,reservation_id    VARCHAR(250)
    ,created_at INTEGER NOT NULL,
    );

CREATE INDEX IF NOT EXISTS idx_capacity_reservation_pool_name ON capacity_reservation (pool_name);