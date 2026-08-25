-- Credit snapshot: IGDB company credits, community curated lists as gap-fill,
-- or user facts on custom entries. NULL = unknown (display/filters skip).
ALTER TABLE entries
    ADD COLUMN developers text[],
    ADD COLUMN publishers text[];
