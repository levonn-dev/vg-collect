-- Credit snapshots (developer and publisher company names): IGDB
-- company credits where the product carries them, the community
-- block's curated lists as gap-fill, or the user's own facts on a
-- custom entry. NULL = no credits known; display omits the line and
-- filters never match.
ALTER TABLE entries
    ADD COLUMN developers text[],
    ADD COLUMN publishers text[];
