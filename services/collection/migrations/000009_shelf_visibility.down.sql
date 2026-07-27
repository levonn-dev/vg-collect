DROP INDEX saved_views_listed_idx;
DROP INDEX saved_views_user_slug_key_idx;
ALTER TABLE saved_views DROP COLUMN slug_key;
ALTER TABLE saved_views DROP COLUMN slug;
ALTER TABLE saved_views DROP COLUMN published_at;
ALTER TABLE saved_views DROP COLUMN visibility;
