CREATE TABLE IF NOT EXISTS firewall_rules (
     id              SERIAL PRIMARY KEY
    ,stage_id        VARCHAR(250) NOT NULL
    ,instance_id     VARCHAR(250) NOT NULL
    ,resource_id     VARCHAR(500) NOT NULL
    ,cloud_provider  VARCHAR(50)  NOT NULL
    ,state           VARCHAR(20)  NOT NULL
    ,created_at      INTEGER      NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_firewall_rules_stage_id
    ON firewall_rules (stage_id);
