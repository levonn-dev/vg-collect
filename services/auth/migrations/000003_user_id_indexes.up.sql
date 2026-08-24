-- Every per-user query (list/delete identities, revoke-on-account-
-- deletion) filters on user_id; neither table had a supporting index.
CREATE INDEX identities_user_id_idx ON identities (user_id);
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
