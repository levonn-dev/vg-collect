-- id: stable API handle for list/unlink so provider subjects never
-- appear in URLs. email: informational display value, never used for
-- resolution; backfills on the identity's next login.
ALTER TABLE identities
    ADD COLUMN id uuid UNIQUE NOT NULL DEFAULT gen_random_uuid(),
    ADD COLUMN email text;

-- A state row with link_user_id is a link flow for that user, not a login.
ALTER TABLE auth_states
    ADD COLUMN link_user_id uuid;
