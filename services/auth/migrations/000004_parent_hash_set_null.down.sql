ALTER TABLE refresh_tokens DROP CONSTRAINT refresh_tokens_parent_hash_fkey;
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_parent_hash_fkey
    FOREIGN KEY (parent_hash) REFERENCES refresh_tokens (token_hash);
