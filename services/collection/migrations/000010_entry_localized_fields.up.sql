-- Region-picked presentation snapshots (native-script title, its
-- transliteration, regional box art), derived from the product's
-- localization bundles by the entry's region at snapshot time. NULL =
-- the region has no localized presentation; display falls back to
-- display_name / cover_url.
ALTER TABLE entries
    ADD COLUMN localized_name text,
    ADD COLUMN localized_name_translit text,
    ADD COLUMN localized_cover_url text;
