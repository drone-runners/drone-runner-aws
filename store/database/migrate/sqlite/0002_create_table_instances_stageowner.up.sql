CREATE TABLE IF NOT EXISTS instances (
     instance_id        VARCHAR(250) PRIMARY KEY
    ,instance_name      VARCHAR(250)
    ,instance_address   VARCHAR(250)
    ,instance_provider  VARCHAR(50)
    ,instance_state     VARCHAR(50)
    ,instance_pool      VARCHAR(250)
    ,instance_image     VARCHAR(250)
    ,instance_region    VARCHAR(50)
    ,instance_zone      VARCHAR(50)
    ,instance_size      VARCHAR(50)
    ,instance_platform  VARCHAR(50)
    ,instance_arch      VARCHAR(50)
    ,instance_stage     INTEGER
    ,instance_ca_key    BLOB
    ,instance_ca_cert   BLOB
    ,instance_tls_key   BLOB
    ,instance_tls_cert  BLOB
    ,instance_updated   INTEGER
    ,instance_started   INTEGER
    ,is_hibernated      BOOLEAN
,UNIQUE(instance_name)
);

CREATE TABLE IF NOT EXISTS stage_owner (
     stage_id          VARCHAR(250) PRIMARY KEY
    ,pool_name         VARCHAR(250)
);