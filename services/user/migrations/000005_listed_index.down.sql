DROP INDEX users_listed_idx;
CREATE INDEX users_listed_idx ON users (id) WHERE profile_visibility = 'listed';
