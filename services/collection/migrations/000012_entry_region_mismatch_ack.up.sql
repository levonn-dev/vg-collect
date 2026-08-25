-- When the owner dismissed the region-mismatch banner for the entry's CURRENT
-- (region, product) choice; any change to either clears it (notifies once).
ALTER TABLE entries ADD COLUMN region_mismatch_ack_at timestamptz;
