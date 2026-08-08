-- When the owner dismissed the region-mismatch banner for the
-- entry's CURRENT (region, product) choice; any change to either
-- clears it so a new choice notifies exactly once again.
ALTER TABLE entries ADD COLUMN region_mismatch_ack_at timestamptz;
