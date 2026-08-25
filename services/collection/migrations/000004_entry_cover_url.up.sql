-- Cover art snapshot from the product's IGDB projection; null for hardware
-- and custom entries (UI shows a placeholder). Immutable after creation.
ALTER TABLE entries ADD COLUMN cover_url text;
