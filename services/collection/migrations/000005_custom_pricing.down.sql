ALTER TABLE entries DROP CONSTRAINT entries_custom_mode_value_check;
ALTER TABLE entries DROP CONSTRAINT entries_custom_value_pair_check;

ALTER TABLE entries DROP CONSTRAINT entries_pricing_mode_check;
ALTER TABLE entries ADD CONSTRAINT entries_pricing_mode_check
    CHECK (pricing_mode IN ('auto', 'proxy', 'disabled'));

ALTER TABLE entries DROP COLUMN custom_value_set_at;
ALTER TABLE entries DROP COLUMN custom_value_cents;
