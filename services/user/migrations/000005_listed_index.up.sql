-- users_listed_idx was scoped to (id), which can't serve SearchListed's
-- query (filters profile_visibility='listed', LIKE-matches handle_key,
-- orders by handle_key). Rebuilding on handle_key avoids an explicit Sort.
DROP INDEX users_listed_idx;
CREATE INDEX users_listed_idx ON users (handle_key) WHERE profile_visibility = 'listed';
