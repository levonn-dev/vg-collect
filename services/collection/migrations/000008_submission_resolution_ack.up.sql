-- The submitter's acknowledgement of an approved verdict. NULL until
-- the owner dismisses the approval banner (stamped by the ack op);
-- the banner persists across page opens until then.
ALTER TABLE catalog_submissions ADD COLUMN resolution_ack_at timestamptz;
