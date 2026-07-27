-- The social graph. shelf_id is a collection saved-view id; owner ids
-- are denormalized at write time so owner-scoped reads and the
-- account purge never need cross-service lookups. Visibility is NEVER
-- stored here: it is evaluated at read time against collection/user.
CREATE TABLE follows (
    follower_id uuid NOT NULL,
    followee_id uuid NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (follower_id, followee_id),
    CHECK (follower_id <> followee_id)
);
CREATE INDEX follows_followee_idx ON follows (followee_id);

CREATE TABLE likes (
    user_id        uuid NOT NULL,
    shelf_id       uuid NOT NULL,
    shelf_owner_id uuid NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, shelf_id)
);
CREATE INDEX likes_shelf_idx ON likes (shelf_id);
CREATE INDEX likes_owner_idx ON likes (shelf_owner_id);

-- Comment lifecycle: live -> self-deleted (deleted_by = author, body
-- NULL) / removed (deleted_by = owner, body RETAINED for a later
-- undelete) / purge-anonymized (author_id NULL, body NULL). The CHECK
-- pins live-row invariants; tombstone body rules are handler-enforced.
CREATE TABLE comments (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    shelf_id       uuid NOT NULL,
    shelf_owner_id uuid NOT NULL,
    author_id      uuid,
    body           text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    deleted_at     timestamptz,
    deleted_by     uuid,
    CHECK (
        (deleted_at IS NULL AND author_id IS NOT NULL AND body IS NOT NULL
            AND length(body) BETWEEN 1 AND 2000)
        OR deleted_at IS NOT NULL
    )
);
CREATE INDEX comments_shelf_live_idx ON comments (shelf_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX comments_author_idx ON comments (author_id);
CREATE INDEX comments_owner_idx ON comments (shelf_owner_id);

-- Append-except-undo: retracting an action (unfollow, unlike, comment
-- delete) deletes its event so feeds never show retracted actions.
-- published_shelf keeps exactly one live row per shelf (the partial
-- unique index); republish refreshes it, throttled hourly in the
-- upsert.
CREATE TABLE activity (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id          uuid NOT NULL,
    verb              text NOT NULL CHECK (verb IN
        ('followed_user', 'liked_shelf', 'commented_shelf', 'published_shelf')),
    object_shelf_id   uuid,
    object_comment_id uuid,
    target_user_id    uuid NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX activity_target_idx ON activity (target_user_id, created_at DESC, id DESC);
CREATE INDEX activity_actor_idx ON activity (actor_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX activity_publish_one_idx ON activity (object_shelf_id)
    WHERE verb = 'published_shelf';
