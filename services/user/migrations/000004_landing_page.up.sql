ALTER TABLE users ADD COLUMN landing_page text NOT NULL DEFAULT 'feed'
    CHECK (landing_page IN ('collection', 'feed', 'explore'));
