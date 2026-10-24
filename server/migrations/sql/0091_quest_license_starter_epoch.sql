-- Only the opt-in starter-aware save path reads or advances this generation.
-- Old clients and DB-derived blobs must retain the generation of the savedata
-- they actually loaded, rather than borrowing a newer generation at save time.
ALTER TABLE characters
    ADD COLUMN starter_inventory_epoch bigint NOT NULL DEFAULT 0
        CHECK (starter_inventory_epoch >= 0);

-- Historical/feature-off backups have unknown generation and must never be
-- presented as current inventory by the opt-in starter-aware recovery path.
ALTER TABLE savedata_backups
    ADD COLUMN starter_inventory_epoch bigint
        CHECK (starter_inventory_epoch >= 0);
