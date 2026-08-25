-- Ack of an approved verdict; NULL keeps the approval banner showing across
-- page opens until the owner dismisses it.
ALTER TABLE catalog_submissions ADD COLUMN resolution_ack_at timestamptz;
