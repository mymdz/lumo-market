-- Base schema. Secondary indexes and foreign keys are created after the
-- initial backfill (see constraints.sql). Columns added over time live in
-- migrations applied by the simulator at their simulated date.
CREATE SCHEMA IF NOT EXISTS shop;
CREATE SCHEMA IF NOT EXISTS sim;

CREATE TABLE IF NOT EXISTS shop.currencies (
    code        char(3) PRIMARY KEY,
    name        text NOT NULL,
    minor_units smallint NOT NULL DEFAULT 2
);

CREATE TABLE IF NOT EXISTS shop.countries (
    code        char(2) PRIMARY KEY,
    name        text NOT NULL,
    currency    char(3) NOT NULL,
    vat_rate    numeric(5,2) NOT NULL,
    vat_reduced numeric(5,2) NOT NULL,
    timezone    text NOT NULL,
    is_eu       boolean NOT NULL,
    launched_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.fx_rates (
    rate_date    date NOT NULL,
    currency     char(3) NOT NULL,
    rate_per_eur numeric(18,6) NOT NULL,
    PRIMARY KEY (rate_date, currency)
);

CREATE TABLE IF NOT EXISTS shop.categories (
    id         integer PRIMARY KEY,
    parent_id  integer,
    name       text NOT NULL,
    slug       text NOT NULL,
    path       text NOT NULL,
    level      smallint NOT NULL,
    is_active  boolean NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.brands (
    id               integer PRIMARY KEY,
    name             text NOT NULL,
    tier             text NOT NULL,
    country          char(2),
    is_private_label boolean NOT NULL,
    created_at       timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.sellers (
    id          integer PRIMARY KEY,
    name        text NOT NULL,
    legal_name  text NOT NULL,
    country     char(2) NOT NULL,
    seller_type text NOT NULL,
    status      text NOT NULL,
    rating      numeric(3,2),
    joined_at   timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    closed_at   timestamptz
);

CREATE TABLE IF NOT EXISTS shop.warehouses (
    id        smallint PRIMARY KEY,
    code      text NOT NULL,
    name      text NOT NULL,
    country   char(2) NOT NULL,
    city      text NOT NULL,
    timezone  text NOT NULL,
    opened_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.suppliers (
    id             integer PRIMARY KEY,
    brand_id       integer,
    name           text NOT NULL,
    country        char(2),
    lead_time_days numeric(5,1) NOT NULL,
    created_at     timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.products (
    id              bigint PRIMARY KEY,
    category_id     integer NOT NULL,
    brand_id        integer,
    title           text NOT NULL,
    description     text,
    status          text NOT NULL,
    weight_g        integer,
    attributes      jsonb,
    successor_id    bigint,
    launched_at     timestamptz NOT NULL,
    discontinued_at timestamptz,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.product_variants (
    id           bigint PRIMARY KEY,
    product_id   bigint NOT NULL,
    sku          text NOT NULL,
    ean          char(13),
    variant_name text,
    attributes   jsonb,
    status       text NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.offers (
    id            bigint PRIMARY KEY,
    variant_id    bigint NOT NULL,
    seller_id     integer NOT NULL,
    price         numeric(12,2) NOT NULL,
    list_price    numeric(12,2),
    currency      char(3) NOT NULL DEFAULT 'EUR',
    fulfillment   text NOT NULL,
    stock_qty     integer,
    handling_days smallint,
    status        text NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.price_history (
    id         bigint PRIMARY KEY,
    offer_id   bigint NOT NULL,
    old_price  numeric(12,2) NOT NULL,
    new_price  numeric(12,2) NOT NULL,
    reason     text NOT NULL,
    changed_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.stock_levels (
    warehouse_id  smallint NOT NULL,
    offer_id      bigint NOT NULL,
    qty_on_hand   integer NOT NULL,
    qty_reserved  integer NOT NULL,
    reorder_point integer NOT NULL,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (warehouse_id, offer_id)
);

CREATE TABLE IF NOT EXISTS shop.stock_movements (
    id           bigint PRIMARY KEY,
    warehouse_id smallint NOT NULL,
    offer_id     bigint NOT NULL,
    qty_delta    integer NOT NULL,
    reason       text NOT NULL,
    ref_type     text,
    ref_id       bigint,
    created_at   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.purchase_orders (
    id           bigint PRIMARY KEY,
    seller_id    integer NOT NULL,
    supplier_id  integer,
    warehouse_id smallint NOT NULL,
    status       text NOT NULL,
    ordered_at   timestamptz NOT NULL,
    expected_at  timestamptz NOT NULL,
    received_at  timestamptz,
    updated_at   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.purchase_order_items (
    id                bigint PRIMARY KEY,
    purchase_order_id bigint NOT NULL,
    offer_id          bigint NOT NULL,
    qty_ordered       integer NOT NULL,
    qty_received      integer,
    unit_cost         numeric(12,2) NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.customers (
    id                  bigint PRIMARY KEY,
    email               text NOT NULL,
    first_name          text,
    last_name           text,
    phone               text,
    birth_date          date,
    gender              text,
    country             char(2) NOT NULL,
    language            text,
    marketing_opt_in    boolean NOT NULL,
    loyalty_tier        text NOT NULL,
    acquisition_channel text,
    signup_device       text,
    default_address_id  bigint,
    status              text NOT NULL,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL,
    deleted_at          timestamptz
);

CREATE TABLE IF NOT EXISTS shop.addresses (
    id             bigint PRIMARY KEY,
    customer_id    bigint,
    recipient_name text,
    line1          text NOT NULL,
    line2          text,
    postal_code    text,
    city           text NOT NULL,
    country        char(2) NOT NULL,
    phone          text,
    created_at     timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.campaigns (
    id            integer PRIMARY KEY,
    name          text NOT NULL,
    campaign_type text NOT NULL,
    starts_at     timestamptz NOT NULL,
    ends_at       timestamptz NOT NULL,
    discount_pct  numeric(5,2),
    countries     text[],
    created_at    timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.coupons (
    id              integer PRIMARY KEY,
    code            text NOT NULL,
    campaign_id     integer,
    discount_type   text NOT NULL,
    discount_value  numeric(10,2) NOT NULL,
    min_order_value numeric(10,2) NOT NULL,
    valid_from      timestamptz NOT NULL,
    valid_to        timestamptz,
    max_uses        integer,
    times_used      integer NOT NULL,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.carts (
    id                 bigint PRIMARY KEY,
    customer_id        bigint,
    session_id         uuid NOT NULL,
    status             text NOT NULL,
    country            char(2) NOT NULL,
    currency           char(3) NOT NULL,
    converted_order_id bigint,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.cart_items (
    id         bigint PRIMARY KEY,
    cart_id    bigint NOT NULL,
    offer_id   bigint NOT NULL,
    qty        integer NOT NULL,
    unit_price numeric(12,2) NOT NULL,
    added_at   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.orders (
    id                  bigint PRIMARY KEY,
    order_number        text NOT NULL,
    customer_id         bigint,
    guest_email         text,
    cart_id             bigint,
    status              text NOT NULL,
    currency            char(3) NOT NULL,
    fx_rate             numeric(18,6) NOT NULL,
    country             char(2) NOT NULL,
    items_subtotal      numeric(12,2) NOT NULL,
    discount_total      numeric(12,2) NOT NULL,
    shipping_fee        numeric(12,2) NOT NULL,
    tax_total           numeric(12,2) NOT NULL,
    grand_total         numeric(12,2) NOT NULL,
    coupon_id           integer,
    shipping_address_id bigint,
    billing_address_id  bigint,
    channel             text NOT NULL,
    shipping_method     text NOT NULL,
    placed_at           timestamptz NOT NULL,
    paid_at             timestamptz,
    cancelled_at        timestamptz,
    cancel_reason       text,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL
);

-- order_items intentionally has no updated_at: status changes are only
-- visible through CDC or by comparing snapshots.
CREATE TABLE IF NOT EXISTS shop.order_items (
    id              bigint PRIMARY KEY,
    order_id        bigint NOT NULL,
    line_no         smallint NOT NULL,
    offer_id        bigint NOT NULL,
    variant_id      bigint NOT NULL,
    product_id      bigint NOT NULL,
    seller_id       integer NOT NULL,
    title           text NOT NULL,
    qty             integer NOT NULL,
    unit_price      numeric(12,2) NOT NULL,
    discount        numeric(12,2) NOT NULL,
    tax_rate        numeric(5,2) NOT NULL,
    tax_amount      numeric(12,2) NOT NULL,
    line_total      numeric(12,2) NOT NULL,
    commission_rate numeric(5,4) NOT NULL,
    status          text NOT NULL,
    returned_qty    integer NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS shop.order_status_history (
    id          bigint PRIMARY KEY,
    order_id    bigint NOT NULL,
    from_status text,
    to_status   text NOT NULL,
    actor       text NOT NULL,
    changed_at  timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.payments (
    id              bigint PRIMARY KEY,
    order_id        bigint NOT NULL,
    method          text NOT NULL,
    provider        text,
    status          text NOT NULL,
    amount          numeric(12,2) NOT NULL,
    currency        char(3) NOT NULL,
    refunded_amount numeric(12,2) NOT NULL,
    failure_reason  text,
    created_at      timestamptz NOT NULL,
    captured_at     timestamptz,
    updated_at      timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.refunds (
    id         bigint PRIMARY KEY,
    payment_id bigint NOT NULL,
    order_id   bigint NOT NULL,
    return_id  bigint,
    amount     numeric(12,2) NOT NULL,
    reason     text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.shipments (
    id              bigint PRIMARY KEY,
    order_id        bigint NOT NULL,
    seller_id       integer NOT NULL,
    warehouse_id    smallint,
    carrier         text NOT NULL,
    tracking_number text,
    status          text NOT NULL,
    shipping_method text NOT NULL,
    promised_at     timestamptz,
    shipped_at      timestamptz,
    delivered_at    timestamptz,
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);

-- received_at comes from the legacy WMS: local warehouse time, no time zone.
CREATE TABLE IF NOT EXISTS shop.returns (
    id            bigint PRIMARY KEY,
    order_id      bigint NOT NULL,
    order_item_id bigint NOT NULL,
    qty           integer NOT NULL,
    reason        text NOT NULL,
    status        text NOT NULL,
    requested_at  timestamptz NOT NULL,
    received_at   timestamp,
    refund_amount numeric(12,2),
    updated_at    timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS shop.reviews (
    id            bigint PRIMARY KEY,
    product_id    bigint NOT NULL,
    customer_id   bigint,
    order_item_id bigint,
    rating        smallint NOT NULL,
    title         text,
    body          text,
    language      text,
    is_verified   boolean NOT NULL,
    helpful_votes integer NOT NULL,
    status        text NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL
);

-- ---------- simulator internals ----------

CREATE TABLE IF NOT EXISTS sim.meta (
    id         smallint PRIMARY KEY DEFAULT 1,
    phase      text NOT NULL,
    sim_time   timestamptz NOT NULL,
    horizon    timestamptz NOT NULL,
    start_time timestamptz NOT NULL,
    seed       bigint NOT NULL,
    state      jsonb,
    finalized  boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sim.migrations (
    id         text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now(),
    sim_time   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS sim.customer_traits (
    customer_id       bigint PRIMARY KEY,
    country           smallint NOT NULL,
    address_id        bigint,
    segment           smallint NOT NULL,
    gender            smallint NOT NULL,
    age               smallint NOT NULL,
    device            smallint NOT NULL,
    channel           smallint NOT NULL,
    pay_pref          smallint NOT NULL,
    tier              smallint NOT NULL,
    status            smallint NOT NULL,
    flags             smallint NOT NULL,
    active            boolean NOT NULL,
    rate              real NOT NULL,
    price_sens        real NOT NULL,
    return_prop       real NOT NULL,
    coupon_aff        real NOT NULL,
    satisfaction      real NOT NULL,
    spent             real NOT NULL,
    orders            integer NOT NULL,
    created_at        bigint NOT NULL,
    updated_at        bigint NOT NULL,
    churn_at          bigint NOT NULL,
    last_order_at     bigint NOT NULL,
    spent_at          bigint NOT NULL,
    phone_verified_at bigint NOT NULL,
    affinity          bytea NOT NULL,
    repl              jsonb
);

CREATE TABLE IF NOT EXISTS sim.product_traits (
    product_id bigint PRIMARY KEY,
    traits     jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS sim.offer_traits (
    offer_id   bigint PRIMARY KEY,
    base_price numeric(12,2) NOT NULL,
    cost       numeric(12,2) NOT NULL,
    demand_ema real NOT NULL,
    promo_id   integer NOT NULL
);

CREATE TABLE IF NOT EXISTS sim.seller_traits (
    seller_id integer PRIMARY KEY,
    traits    jsonb NOT NULL,
    shipped   integer NOT NULL,
    cancelled integer NOT NULL
);

CREATE TABLE IF NOT EXISTS sim.pending (
    kind   text NOT NULL,
    id     bigint NOT NULL,
    due_at timestamptz,
    data   jsonb NOT NULL,
    PRIMARY KEY (kind, id)
);
