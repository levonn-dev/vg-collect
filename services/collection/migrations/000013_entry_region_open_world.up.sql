-- Region goes open-world: known values (ntsc_u, ntsc_j, pal, region_free) stay
-- keyed in code; any other trimmed string is a plain display fact.
ALTER TABLE entries DROP CONSTRAINT entries_region_check;
