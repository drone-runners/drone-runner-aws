ALTER TABLE capacity_reservation ADD COLUMN IF NOT EXISTS marked_for_deletion BOOLEAN NOT NULL DEFAULT false;
