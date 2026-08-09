-- Region goes open-world: known values (ntsc_u, ntsc_j, pal,
-- region_free) keep the machinery keyed to them in code; any other
-- trimmed string is an honest display fact. The CHECK gate retires.
ALTER TABLE entries DROP CONSTRAINT entries_region_check;
