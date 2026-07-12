ALTER TABLE entries ADD COLUMN custom_value_entered_cents bigint
    CHECK (custom_value_entered_cents >= 0);
ALTER TABLE entries ADD COLUMN custom_value_entered_currency text
    CHECK (custom_value_entered_currency ~ '^[A-Z]{3}$');
ALTER TABLE entries ADD CONSTRAINT entries_custom_value_entered_pair_check
    CHECK ((custom_value_entered_cents IS NULL) = (custom_value_entered_currency IS NULL));
