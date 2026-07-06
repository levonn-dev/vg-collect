-- Cover art joins the creation-time catalog snapshot: set from the
-- product's IGDB projection for games with art, null for hardware and
-- custom entries (the UI renders a placeholder tile). Immutable after
-- creation like the other snapshot fields; rows predating this column
-- simply have no cover.
ALTER TABLE entries ADD COLUMN cover_url text;
