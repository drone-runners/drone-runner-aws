ALTER TABLE instances ADD COLUMN instance_os  TEXT NOT NULL DEFAULT '';

ALTER TABLE instances ADD COLUMN instance_variant  TEXT NOT NULL DEFAULT '';

ALTER TABLE instances ADD COLUMN instance_version  TEXT NOT NULL DEFAULT '';