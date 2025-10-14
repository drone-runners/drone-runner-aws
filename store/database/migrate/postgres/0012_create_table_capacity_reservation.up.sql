CREATE TABLE IF NOT EXISTS capacity_reservation (
     stage_id          VARCHAR(250) PRIMARY KEY
    ,pool_name         VARCHAR(250)
    ,instance_id       VARCHAR(250)
    ,reservation_id    VARCHAR(250)
    );