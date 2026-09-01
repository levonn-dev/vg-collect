CREATE TABLE products (
    id                   uuid PRIMARY KEY,
    type                 text NOT NULL,
    origin               text NOT NULL,
    name                 text NOT NULL,
    region               text NOT NULL DEFAULT '',
    edition              text NOT NULL DEFAULT '',
    variant              text NOT NULL DEFAULT '',
    match_hold           boolean NOT NULL DEFAULT false,
    igdb                 jsonb,
    platform             jsonb,
    pricecharting        jsonb,
    community            jsonb,
    promote_candidates   jsonb,
    dismissed_candidates jsonb,
    igdb_game_id     bigint GENERATED ALWAYS AS ((igdb->>'game_id')::bigint) STORED,
    platform_igdb_id bigint GENERATED ALWAYS AS ((platform->>'igdb_id')::bigint) STORED,
    pc_product_id    bigint GENERATED ALWAYS AS ((pricecharting->>'pc_product_id')::bigint) STORED,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX products_game_identity ON products
    (igdb_game_id, platform_igdb_id, pc_product_id) NULLS NOT DISTINCT
    WHERE type = 'game' AND origin = 'provider';
CREATE UNIQUE INDEX products_hardware_identity ON products
    (pc_product_id, region, edition, variant) NULLS NOT DISTINCT
    WHERE type IN ('console', 'accessory') AND origin = 'provider';
CREATE UNIQUE INDEX products_pc_listing_identity ON products
    (pc_product_id) WHERE type = 'pc_listing';
CREATE INDEX products_name ON products (name);
CREATE INDEX products_unmatched_worklist ON products (updated_at, id)
    WHERE origin = 'provider' AND pricecharting IS NULL;

CREATE TABLE igdb_raw (
    id             bigint PRIMARY KEY,
    game           jsonb NOT NULL,
    fetched_at     timestamptz NOT NULL,
    fields_version int NOT NULL
);

CREATE TABLE platforms (
    igdb_id      bigint PRIMARY KEY,
    name         text NOT NULL,
    abbreviation text NOT NULL,
    generation   int NOT NULL,
    logo_url     text NOT NULL,
    fetched_at   timestamptz NOT NULL
);

CREATE TABLE price_snapshots (
    product_id  uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    captured_at timestamptz NOT NULL,
    loose_cents bigint,
    cib_cents   bigint,
    new_cents   bigint,
    PRIMARY KEY (product_id, captured_at)
) PARTITION BY RANGE (captured_at);

CREATE TABLE price_snapshots_default PARTITION OF price_snapshots DEFAULT;
CREATE TABLE price_snapshots_2026 PARTITION OF price_snapshots
    FOR VALUES FROM ('2026-01-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2027 PARTITION OF price_snapshots
    FOR VALUES FROM ('2027-01-01 00:00:00+00') TO ('2028-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2028 PARTITION OF price_snapshots
    FOR VALUES FROM ('2028-01-01 00:00:00+00') TO ('2029-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2029 PARTITION OF price_snapshots
    FOR VALUES FROM ('2029-01-01 00:00:00+00') TO ('2030-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2030 PARTITION OF price_snapshots
    FOR VALUES FROM ('2030-01-01 00:00:00+00') TO ('2031-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2031 PARTITION OF price_snapshots
    FOR VALUES FROM ('2031-01-01 00:00:00+00') TO ('2032-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2032 PARTITION OF price_snapshots
    FOR VALUES FROM ('2032-01-01 00:00:00+00') TO ('2033-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2033 PARTITION OF price_snapshots
    FOR VALUES FROM ('2033-01-01 00:00:00+00') TO ('2034-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2034 PARTITION OF price_snapshots
    FOR VALUES FROM ('2034-01-01 00:00:00+00') TO ('2035-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2035 PARTITION OF price_snapshots
    FOR VALUES FROM ('2035-01-01 00:00:00+00') TO ('2036-01-01 00:00:00+00');
CREATE TABLE price_snapshots_2036 PARTITION OF price_snapshots
    FOR VALUES FROM ('2036-01-01 00:00:00+00') TO ('2037-01-01 00:00:00+00');
