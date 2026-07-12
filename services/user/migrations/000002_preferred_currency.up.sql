ALTER TABLE users ADD COLUMN preferred_currency text NOT NULL DEFAULT 'USD'
    CHECK (preferred_currency ~ '^[A-Z]{3}$');
