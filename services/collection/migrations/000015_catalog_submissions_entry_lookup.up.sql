-- Serves LatestSubmissionForEntry: no existing index leads with entry_id,
-- so the entry page's read scanned the caller's whole submission history.
CREATE INDEX catalog_submissions_user_entry_created_idx
    ON catalog_submissions (user_id, entry_id, created_at DESC);
