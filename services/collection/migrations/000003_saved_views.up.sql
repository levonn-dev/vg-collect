CREATE TABLE saved_views (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL,
    name       citext NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    params     jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE INDEX saved_views_user_idx ON saved_views (user_id);
