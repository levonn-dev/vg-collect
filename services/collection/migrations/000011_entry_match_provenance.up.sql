-- Whose choice the entry's current product/listing is. Automation
-- (the region-edit repoint and the entry rematch) only ever
-- touches rows whose match it made itself.
ALTER TABLE entries
    ADD COLUMN match_provenance text NOT NULL DEFAULT 'auto'
        CHECK (match_provenance IN ('auto', 'user'));
