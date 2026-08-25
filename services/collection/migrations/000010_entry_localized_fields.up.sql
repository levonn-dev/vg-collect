-- Region-picked presentation snapshot from the product's localization bundles;
-- NULL falls back to display_name / cover_url.
ALTER TABLE entries
    ADD COLUMN localized_name text,
    ADD COLUMN localized_name_translit text,
    ADD COLUMN localized_cover_url text;
