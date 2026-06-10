CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email        citext UNIQUE NOT NULL,
    display_name text NOT NULL,
    avatar_url   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role    text NOT NULL CHECK (role IN ('user', 'admin')),
    PRIMARY KEY (user_id, role)
);
