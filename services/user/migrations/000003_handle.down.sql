-- display_name values are unrecoverable; reconstruct readable names
-- from handles (underscores back to spaces).
ALTER TABLE users ADD COLUMN display_name text;
UPDATE users SET display_name = replace(handle, '_', ' ');
ALTER TABLE users ALTER COLUMN display_name SET NOT NULL;
DROP INDEX users_listed_idx;
DROP INDEX users_handle_key_idx;
ALTER TABLE users DROP COLUMN handle_key;
ALTER TABLE users DROP COLUMN profile_visibility;
ALTER TABLE users DROP COLUMN handle_changed_at;
ALTER TABLE users DROP COLUMN handle;
