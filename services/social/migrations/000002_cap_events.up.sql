-- Follow/like actions need a history that survives retraction: the
-- rolling-24h cap counts entries here, not rows in follows/likes,
-- because Unfollow/Unlike hard-delete the edge (feeds must never show
-- a retracted action) and there is no tombstone left behind for the
-- cap to count instead - unlike comments, whose own live table
-- already keeps that history. No FK to users, matching every other
-- table in this schema (identities are validated over HTTP, never
-- joined in Postgres). Self-retaining: the store opportunistically
-- deletes rows older than 48h on every insert, so no background job
-- is needed to keep this bounded.
CREATE TABLE cap_events (
    user_id    uuid NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('follow', 'like')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cap_events_user_kind_idx ON cap_events (user_id, kind, created_at DESC);
