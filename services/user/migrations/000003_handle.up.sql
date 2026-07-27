-- Handles replace display_name as the single user identity. Case and
-- underscores are decoration: uniqueness and lookup fold via handle_key.
-- Backfill derives from display_name (underscore transform), falls back
-- to the email local part, dedupes on the folded key with a numeric
-- suffix. Dev-tier data volumes; the dedupe pass walks rows oldest
-- first and probes each candidate against a running claimed-key set,
-- so a bumped value can never collide with another row's key.
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

-- Walk rows oldest-first, probing each row's own derived value (then
-- numeric suffixes) against a running set of already-claimed fold
-- keys. A candidate is only accepted once confirmed collision-free
-- against every row decided so far, so a suffixed value can never
-- land on a key another row already holds - unlike a single-pass
-- partition dedupe, which fixes suffixes from stale sibling counts
-- and can walk two different partitions onto the same folded key.
-- claimed starts pre-loaded with the reserved handle folds (mirrors
-- services/user/internal/store/handle.go's ReservedHandles) so a
-- display_name that derives to one of them, e.g. "Search", probes
-- straight past it to a suffixed value - the app's mint path already
-- refuses these, and the backfill must not be the one way around it.
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
            -- rtrim mirrors services/user/internal/store/store.go's Upsert
            -- probe loop exactly: a clamp that lands mid-underscore-run
            -- must drop the trailing underscore before the suffix digits
            -- land, or the backfill and the app mint the same collision
            -- into two different (though same-folding) handles.
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
