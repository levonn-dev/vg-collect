ALTER TABLE entries ADD COLUMN custom_value_cents bigint CHECK (custom_value_cents >= 0);
ALTER TABLE entries ADD COLUMN custom_value_set_at timestamptz;

ALTER TABLE entries DROP CONSTRAINT entries_pricing_mode_check;
ALTER TABLE entries ADD CONSTRAINT entries_pricing_mode_check
    CHECK (pricing_mode IN ('auto', 'proxy', 'custom', 'disabled'));

-- The pair travels together; custom mode requires a value. Under any
-- other mode the pair persists as "last custom price" memory.
ALTER TABLE entries ADD CONSTRAINT entries_custom_value_pair_check
    CHECK ((custom_value_cents IS NULL) = (custom_value_set_at IS NULL));
ALTER TABLE entries ADD CONSTRAINT entries_custom_mode_value_check
    CHECK (pricing_mode <> 'custom' OR custom_value_cents IS NOT NULL);
