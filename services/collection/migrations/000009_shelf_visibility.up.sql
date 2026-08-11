-- Shelf sharing: per-view visibility (private < unlisted < listed),
-- published_at (stamped on each transition into listed; drives
-- Explore recent-first), and a URL slug derived from the name.
-- Slugs address, UUIDs identify: slug_key folds case+underscores and
-- is unique per user. Backfill derives slugs from names with a
-- numeric-suffix dedupe (per-user), fallback 'shelf'.
ALTER TABLE saved_views ADD COLUMN visibility text NOT NULL DEFAULT 'private'
    CHECK (visibility IN ('private', 'unlisted', 'listed'));
ALTER TABLE saved_views ADD COLUMN published_at timestamptz;
ALTER TABLE saved_views ADD COLUMN slug text;

UPDATE saved_views SET slug = left(
    regexp_replace(regexp_replace(name::text, '[^a-zA-Z0-9]+', '_', 'g'), '^_+|_+$', '', 'g'),
    30);
UPDATE saved_views SET slug = 'shelf'
WHERE slug IS NULL OR length(replace(slug, '_', '')) < 2;

-- Walk rows oldest-first per user, probing each row's own derived
-- value (then numeric suffixes) against a running set of
-- already-claimed fold keys, reset at each user boundary. A candidate
-- is only accepted once confirmed collision-free against every row
-- decided so far for that user, so a suffixed value can never land on
-- a key another row already holds - unlike a single-pass partition
-- dedupe, which fixes suffixes from stale sibling counts and can walk
-- two different partitions onto the same folded key.
DO $$
DECLARE
    r RECORD;
    prev_user uuid;
    claimed text[] := ARRAY[]::text[];
    base text;
    candidate text;
    fold text;
    attempt int;
BEGIN
    FOR r IN SELECT id, user_id, slug FROM saved_views ORDER BY user_id, created_at, id LOOP
        IF prev_user IS DISTINCT FROM r.user_id THEN
            claimed := ARRAY[]::text[];
            prev_user := r.user_id;
        END IF;
        base := r.slug;
        candidate := base;
        attempt := 2;
        LOOP
            fold := lower(replace(candidate, '_', ''));
            EXIT WHEN NOT (fold = ANY(claimed));
            -- rtrim mirrors services/collection/internal/store/store_views.go's
            -- CreateView/UpdateView slug dedupe exactly: a clamp that
            -- lands mid-underscore-run must drop the trailing underscore
            -- before the suffix digits land, or the backfill and the app
            -- mint the same collision into two different (though
            -- same-folding) slugs.
            candidate := rtrim(left(base, 30 - length(attempt::text)), '_') || attempt::text;
            attempt := attempt + 1;
        END LOOP;
        claimed := claimed || fold;
        IF candidate <> r.slug THEN
            UPDATE saved_views SET slug = candidate WHERE id = r.id;
        END IF;
    END LOOP;
END $$;

ALTER TABLE saved_views ALTER COLUMN slug SET NOT NULL;
ALTER TABLE saved_views ADD COLUMN slug_key text
    GENERATED ALWAYS AS (lower(replace(slug, '_', ''))) STORED;
CREATE UNIQUE INDEX saved_views_user_slug_key_idx ON saved_views (user_id, slug_key);
CREATE INDEX saved_views_listed_idx ON saved_views (published_at DESC) WHERE visibility = 'listed';
