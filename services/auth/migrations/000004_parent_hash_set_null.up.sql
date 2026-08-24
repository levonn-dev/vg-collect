-- parent_hash is an audit-trail link only (reuse detection revokes a
-- family by family_id, never by walking the chain). The opportunistic
-- retention sweep in CreateSession deletes dead rows past their
-- window, and a swept row can still be a live descendant's
-- parent_hash; ON DELETE SET NULL lets the sweep proceed and simply
-- truncates the audit link instead of the delete failing on the FK.
ALTER TABLE refresh_tokens DROP CONSTRAINT refresh_tokens_parent_hash_fkey;
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_parent_hash_fkey
    FOREIGN KEY (parent_hash) REFERENCES refresh_tokens (token_hash) ON DELETE SET NULL;
