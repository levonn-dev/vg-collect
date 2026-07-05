CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE tags (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL,
    name       citext NOT NULL CHECK (length(name) BETWEEN 1 AND 50),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE TABLE entry_tags (
    entry_id uuid NOT NULL REFERENCES entries (id) ON DELETE CASCADE,
    tag_id   uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
    PRIMARY KEY (entry_id, tag_id)
);

CREATE INDEX entry_tags_tag_idx ON entry_tags (tag_id);
