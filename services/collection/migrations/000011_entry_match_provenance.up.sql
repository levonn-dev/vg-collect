-- Whose choice the entry's current product/listing is; automation (region-edit
-- repoint, entry rematch) only touches rows whose match it made itself.
ALTER TABLE entries
    ADD COLUMN match_provenance text NOT NULL DEFAULT 'auto'
        CHECK (match_provenance IN ('auto', 'user'));
