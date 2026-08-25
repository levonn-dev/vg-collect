CREATE TABLE catalog_submissions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The submission's subject; cascade delete also cancels a pending
    -- submission when its entry is deleted.
    entry_id      uuid NOT NULL REFERENCES entries (id) ON DELETE CASCADE,
    user_id       uuid NOT NULL,
    status        text NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    reject_reason text,
    -- Verdict's resolved product, recorded before adoption so an approve_new retry never mints twice.
    product_id    uuid,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    reviewed_at   timestamptz
);

-- One open submission per entry.
CREATE UNIQUE INDEX catalog_submissions_pending_entry_idx
    ON catalog_submissions (entry_id) WHERE status = 'pending';
-- The per-user caps: pending count and the rolling creation window.
CREATE INDEX catalog_submissions_user_created_idx
    ON catalog_submissions (user_id, created_at);
-- The admin queue: pending, oldest first.
CREATE INDEX catalog_submissions_status_created_idx
    ON catalog_submissions (status, created_at);
