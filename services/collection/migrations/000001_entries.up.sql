CREATE TABLE entries (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            uuid NOT NULL,
    -- Null = a custom (off-catalog) entry: user supplies display fields below.
    product_id         uuid,
    item_type          text NOT NULL CHECK (item_type IN ('game', 'console', 'accessory')),
    media_type         text NOT NULL DEFAULT 'physical' CHECK (media_type IN ('physical', 'digital')),

    -- Denormalized catalog snapshot on product-backed entries, immutable
    -- after creation; user-owned and editable on custom entries.
    display_name       text NOT NULL,
    platform_igdb_id   bigint,
    platform_name      text,
    first_release_date date,
    igdb_game_id       bigint,

    region             text NOT NULL CHECK (region IN ('ntsc_u', 'ntsc_j', 'pal', 'region_free')),
    edition            text,
    packaging          text NOT NULL CHECK (packaging IN ('sealed', 'cib', 'loose')),
    has_box            boolean NOT NULL DEFAULT false,
    has_manual         boolean NOT NULL DEFAULT false,
    box_condition      text CHECK (box_condition IN ('mint', 'near_mint', 'very_good', 'good', 'acceptable', 'poor')),
    manual_condition   text CHECK (manual_condition IN ('mint', 'near_mint', 'very_good', 'good', 'acceptable', 'poor')),
    item_condition     text CHECK (item_condition IN ('mint', 'near_mint', 'very_good', 'good', 'acceptable', 'poor')),

    price_paid_cents   bigint CHECK (price_paid_cents >= 0),
    currency           text NOT NULL DEFAULT 'USD' CHECK (currency ~ '^[A-Z]{3}$'),
    purchased_at       date,
    purchased_from     text,

    pricing_mode       text NOT NULL DEFAULT 'auto' CHECK (pricing_mode IN ('auto', 'proxy', 'disabled')),
    pricing_product_id uuid,

    status             text NOT NULL DEFAULT 'backlog' CHECK (status IN ('backlog', 'playing', 'beaten', 'completed', 'dropped', 'shelved')),
    rating             integer CHECK (rating BETWEEN 1 AND 10),
    notes              text,
    storage_location   text,
    pinned             boolean NOT NULL DEFAULT false,
    -- Fractional-index rank; COLLATE "C" so ORDER BY matches the Go generator's byte order.
    backlog_rank       text COLLATE "C",

    -- Reserved for platform sync; the API constrains to manual/physical.
    source             text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'steam', 'psn', 'epic')),
    external_ref       text,

    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),

    -- An igdb platform id never appears without its name; a bare name is free-text.
    CHECK (platform_igdb_id IS NULL OR platform_name IS NOT NULL),
    -- Only games carry an igdb_game_id; on a custom entry it exists only via
    -- a pricing proxy (a repro of X counts as playing X).
    CHECK (item_type = 'game' OR igdb_game_id IS NULL),
    CHECK (igdb_game_id IS NULL OR product_id IS NOT NULL OR pricing_mode = 'proxy'),
    -- Proxy pricing requires pricing_product_id; auto requires product_id
    -- (customs use proxy or disabled).
    CHECK (pricing_mode <> 'proxy' OR pricing_product_id IS NOT NULL),
    CHECK (pricing_mode <> 'auto' OR product_id IS NOT NULL),
    -- A condition grade only makes sense for a part the copy has.
    CHECK (has_box OR box_condition IS NULL),
    CHECK (has_manual OR manual_condition IS NULL),
    -- A rank exists exactly while the entry is in the backlog.
    CHECK ((status = 'backlog') = (backlog_rank IS NOT NULL))
);

CREATE INDEX entries_user_idx ON entries (user_id);
CREATE INDEX entries_user_backlog_rank_idx ON entries (user_id, backlog_rank) WHERE status = 'backlog';
