-- Rolling-24h cap counts entries here, not follows/likes rows: Unfollow/
-- Unlike hard-delete the edge, leaving no tombstone to count (unlike
-- comments). No FK to users (identities validate over HTTP, never joined
-- in Postgres). Self-retaining: the store sweeps rows older than 48h on insert.
CREATE TABLE cap_events (
    user_id    uuid NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('follow', 'like')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cap_events_user_kind_idx ON cap_events (user_id, kind, created_at DESC);
