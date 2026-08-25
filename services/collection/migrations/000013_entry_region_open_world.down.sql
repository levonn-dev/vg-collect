-- Safe only while no free-text regions exist; run the normalize lever first if populated.
ALTER TABLE entries ADD CONSTRAINT entries_region_check
  CHECK (region IN ('ntsc_u', 'ntsc_j', 'pal', 'region_free'));
