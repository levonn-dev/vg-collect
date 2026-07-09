ALTER TABLE auth_states DROP COLUMN link_user_id;

ALTER TABLE identities
    DROP COLUMN email,
    DROP COLUMN id;
