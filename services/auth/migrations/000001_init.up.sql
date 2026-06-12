CREATE TABLE identities (
    provider         text NOT NULL,
    provider_subject text NOT NULL,
    user_id          uuid NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_subject)
);

CREATE TABLE signing_keys (
    kid        text PRIMARY KEY,
    -- base64url raw Ed25519 public key, exactly the JWKS "x" field.
    public_key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    retired_at timestamptz
);

CREATE TABLE auth_states (
    state         text PRIMARY KEY,
    pkce_verifier text NOT NULL,
    nonce         text NOT NULL,
    provider      text NOT NULL,
    expires_at    timestamptz NOT NULL
);

CREATE TABLE refresh_tokens (
    -- hex SHA-256 of the opaque token; the raw token is never stored.
    token_hash      text PRIMARY KEY,
    -- rotation audit trail: which token this one replaced.
    parent_hash     text REFERENCES refresh_tokens (token_hash),
    -- login session id, inherited on rotation; reuse detection revokes
    -- the whole family with one indexed UPDATE.
    family_id       uuid NOT NULL,
    user_id         uuid NOT NULL,
    -- jti of the access token minted alongside this refresh token.
    last_access_jti text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    used_at         timestamptz,
    revoked_at      timestamptz
);

CREATE INDEX refresh_tokens_family_idx ON refresh_tokens (family_id);
