CREATE TABLE IF NOT EXISTS instances (
     instance_id        VARCHAR(250) PRIMARY KEY
    ,instance_name      VARCHAR(50)
    ,instance_address   VARCHAR(250)
    ,instance_provider  VARCHAR(50)
    ,instance_state     VARCHAR(50)
    ,instance_pool      VARCHAR(250)
    ,instance_image     VARCHAR(50)
    ,instance_region    VARCHAR(50)
    ,instance_zone      VARCHAR(50)
    ,instance_size      VARCHAR(50)
    ,instance_platform  VARCHAR(50)
    ,instance_capacity  INTEGER
    ,instance_error     TEXT
    ,instance_ca_key    TEXT
    ,instance_ca_cert   TEXT
    ,instance_tls_key   TEXT
    ,instance_tls_cert  TEXT
    ,instance_created   INTEGER
    ,instance_updated   INTEGER
    ,instance_started   INTEGER
    ,instance_stopped   INTEGER
,UNIQUE(instance_name)
);
