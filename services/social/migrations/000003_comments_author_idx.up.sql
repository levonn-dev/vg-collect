-- capCount's rolling-24h scan needs created_at trailing to bound it
-- (matching cap_events_user_kind_idx); the old single-column index walked
-- the author's entire history. Purge's equality lookup still uses the leading column.
DROP INDEX comments_author_idx;
CREATE INDEX comments_author_idx ON comments (author_id, created_at DESC);
