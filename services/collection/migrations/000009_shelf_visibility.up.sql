-- Visibility ordered private < unlisted < listed; published_at stamps the
-- transition into listed and drives Explore's recent-first sort.
ALTER TABLE saved_views ADD COLUMN visibility text NOT NULL DEFAULT 'private'
    CHECK (visibility IN ('private', 'unlisted', 'listed'));
ALTER TABLE saved_views ADD COLUMN published_at timestamptz;
ALTER TABLE saved_views ADD COLUMN slug text;

UPDATE saved_views SET slug = left(
    regexp_replace(regexp_replace(name::text, '[^a-zA-Z0-9]+', '_', 'g'), '^_+|_+$', '', 'g'),
    30);
UPDATE saved_views SET slug = 'shelf'
WHERE slug IS NULL OR length(replace(slug, '_', '')) < 2;

-- Walks rows oldest-first per user, confirming each candidate against every
-- fold key already claimed for that user so suffixed values never collide.
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
            -- Mirrors store_views.go's CreateView/UpdateView clamp: trim the
            -- trailing underscore before suffix digits, or backfill and app diverge.
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
-- Folds so casing/punctuation variants of a slug collide in the unique index below.
ALTER TABLE saved_views ADD COLUMN slug_key text
    GENERATED ALWAYS AS (lower(replace(slug, '_', ''))) STORED;
CREATE UNIQUE INDEX saved_views_user_slug_key_idx ON saved_views (user_id, slug_key);
CREATE INDEX saved_views_listed_idx ON saved_views (published_at DESC) WHERE visibility = 'listed';
