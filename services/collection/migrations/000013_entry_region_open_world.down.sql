-- Safe only while no free-text regions exist; a populated table needs
-- the normalize lever run first.
ALTER TABLE entries ADD CONSTRAINT entries_region_check
  CHECK (region IN ('ntsc_u', 'ntsc_j', 'pal', 'region_free'));
