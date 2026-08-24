-- users_listed_idx was scoped to (id), which cannot serve the one
-- query it exists for: SearchListed filters on profile_visibility =
-- 'listed', matches handle_key against a LIKE pattern, and orders by
-- handle_key. Rebuilding the index on handle_key lets the planner
-- read listed rows in handle_key order for free instead of adding an
-- explicit Sort step.
DROP INDEX users_listed_idx;
CREATE INDEX users_listed_idx ON users (handle_key) WHERE profile_visibility = 'listed';
