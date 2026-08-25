-- Handles replace display_name as the identity; case/underscores are
-- decoration, folding via handle_key. Backfill derives from display_name,
-- falls back to the email local part, then dedupes via a numeric suffix,
-- walking rows oldest-first against a running claimed-key set so no two rows collide.
ALTER TABLE users ADD COLUMN handle text;
ALTER TABLE users ADD COLUMN handle_changed_at timestamptz;
ALTER TABLE users ADD COLUMN profile_visibility text NOT NULL DEFAULT 'private'
    CHECK (profile_visibility IN ('private', 'unlisted', 'listed'));

UPDATE users SET handle = left(
    regexp_replace(regexp_replace(display_name, '[^a-zA-Z0-9]+', '_', 'g'), '^_+|_+$', '', 'g'),
    30);

UPDATE users SET handle = left(
    regexp_replace(regexp_replace(split_part(email, '@', 1), '[^a-zA-Z0-9]+', '_', 'g'), '^_+|_+$', '', 'g'),
    30)
WHERE handle IS NULL OR length(replace(handle, '_', '')) < 2;

UPDATE users SET handle = 'collector'
WHERE handle IS NULL OR length(replace(handle, '_', '')) < 2;

-- Walks rows oldest-first, accepting a candidate only once confirmed
-- collision-free against every row decided so far (unlike a single-pass
-- partition dedupe, which can walk two partitions onto the same folded
-- key from stale sibling counts). claimed starts pre-loaded with the
-- reserved handle folds (handle.go's ReservedHandles), so a reserved-word
-- derivation probes past it like any other collision.
DO $$
DECLARE
    r RECORD;
    claimed text[] := ARRAY[
        'admin', 'api', 'explore', 'feed', 'login', 'me',
        'search', 'settings', 'shelves', 'u', 'vg'
    ]::text[];
    base text;
    candidate text;
    fold text;
    attempt int;
BEGIN
    FOR r IN SELECT id, handle FROM users ORDER BY created_at, id LOOP
        base := r.handle;
        candidate := base;
        attempt := 2;
        LOOP
            fold := lower(replace(candidate, '_', ''));
            EXIT WHEN NOT (fold = ANY(claimed));
            -- rtrim mirrors store.go's Upsert probe loop: a clamp landing
            -- mid-underscore-run must drop the trailing underscore before the suffix
            -- digits land, or backfill and app-mint the same collision differently.
            candidate := rtrim(left(base, 30 - length(attempt::text)), '_') || attempt::text;
            attempt := attempt + 1;
        END LOOP;
        claimed := claimed || fold;
        IF candidate <> r.handle THEN
            UPDATE users SET handle = candidate WHERE id = r.id;
        END IF;
    END LOOP;
END $$;

ALTER TABLE users ALTER COLUMN handle SET NOT NULL;
ALTER TABLE users ADD COLUMN handle_key text
    GENERATED ALWAYS AS (lower(replace(handle, '_', ''))) STORED;
CREATE UNIQUE INDEX users_handle_key_idx ON users (handle_key);
CREATE INDEX users_listed_idx ON users (id) WHERE profile_visibility = 'listed';

ALTER TABLE users DROP COLUMN display_name;
