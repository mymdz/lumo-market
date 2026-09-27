# Lumo Market: detailed guide

[Русская версия](../ru/overview.md) · [Database schema](database.md) · [README](../../README.md)

A simulator of a European marketplace, **Lumo Market**, that continuously writes
its activity into PostgreSQL. It is meant to be a "living" OLTP source for
experiments with lakehouses, CDC, incremental loads and BI.

- ~20,000+ products (and growing), 390 leaf categories in 17 departments, ~360 brands
- own retail (1P) + hundreds of third-party sellers (3P), fulfilment by Lumo
- 15 countries, 6 currencies (EUR, PLN, CZK, SEK, DKK, **MDL**), per-country VAT
- 2 years of history are generated in minutes, then the simulation runs in real time
- 5,000 → 13,000 orders per day, plus seasonality, sales, incidents and noise

## Quick start

```bash
docker compose up -d --build
# watch the progress
docker compose logs -f simulator
curl localhost:8088/status
```

Postgres listens on `localhost:5433` (`lumo` / `lumo`, database `lumo`).
Business data lives in the `shop` schema, the simulator's internal state in `sim`.

First the simulator generates history (`history_days`, 730 by default); a full
backfill takes about 12 minutes. Then it creates indexes and foreign keys and
switches to live mode: orders appear in real time.

Locally without Docker (only Postgres is needed):

```bash
go run ./cmd/lumo -config config.yaml run
go run ./cmd/lumo -config config.yaml reset   # drop all data
```

For a quick trial run: `LUMO_HISTORY_DAYS=30 LUMO_INITIAL_PRODUCTS=5000`.

Resources with default settings (2 years, 5k→13k orders/day): the database takes
about 17–20 GB; the simulator keeps the world in memory and peaks at about
2.6 GB RAM. Less history means proportionally less of both.

Ports can be changed with `LUMO_PG_PORT` (default 5433) and
`LUMO_API_PORT` (default 8088). In Docker the simulator reads the mounted
`config.yaml`; when running the binary directly, every key can also be overridden
with a `LUMO_<NAME>` environment variable, e.g. `LUMO_DIRT_LEVEL=0`.

## Time, downtime and dates

```bash
curl localhost:8088/status                # phase, clock, orders per day, active events
curl localhost:8088/stats                 # approximate table sizes
curl -XPOST 'localhost:8088/speed?x=0.5'  # slow down (the simulation starts lagging behind wall time)
curl -XPOST localhost:8088/pause
curl -XPOST localhost:8088/resume
curl -XPOST localhost:8088/resync         # back to speed 1 and catch up with wall time
go run ./cmd/lumo check              # find rows dated in the future
```

**Downtime.** You can stop the generator for a week: on the next start it
restores the world from the database, simulates the missed period at full speed
(orders, deliveries, returns, purchase orders, campaigns — everything that
should have happened) and then continues in real time. A week at ~13k
orders/day is caught up in about 30 seconds. Everything simulated during the
catch-up is written with regular INSERT/UPDATE statements carrying historical
timestamps, so for CDC it looks like a burst of changes at startup. Pause and
slow-down work the same way: after `resume`/`resync` the missed time is replayed.

**The future.** No event is ever dated later than the current moment: simulated
time never overtakes wall time. Speeding up (`speed > 1`) is only possible with
`allow_future: true`, in which case data deliberately goes into the future. If
you then turn `allow_future` off, the simulator waits for wall time to catch up.

Only **planning** columns may legitimately hold future values:
`shipments.promised_at` (promised delivery date), `purchase_orders.expected_at`
(expected delivery), `campaigns.starts_at/ends_at` (campaigns are created 3–6
weeks before they start), `coupons.valid_from/valid_to`. `returns.received_at`
is deliberately stored in local warehouse time without a time zone, so in UTC
it looks 1–2 hours "ahead". `lumo check` accounts for this and checks all
other columns.

- Restarts are safe: all state (including in-flight orders) is stored in the
  database, every checkpoint is written in a single transaction.
- If the process dies during the initial history backfill, the backfill starts over.

## The `shop` schema

| Area | Tables |
|---|---|
| Reference data | `currencies`, `countries`, `fx_rates`, `warehouses`, `categories` (tree), `brands` |
| Catalog | `products`, `product_variants` (SKU/EAN, size/colour/storage), `offers` (seller price), `price_history` |
| Marketplace | `sellers` (1P + 3P, statuses onboarding/active/suspended/closed) |
| Warehouse | `stock_levels`, `stock_movements`, `suppliers`, `purchase_orders`, `purchase_order_items` |
| Customers | `customers`, `addresses` |
| Marketing | `campaigns`, `coupons` |
| Sales | `carts`, `cart_items`, `orders`, `order_items`, `order_status_history`, `payments`, `refunds`, `shipments`, `returns`, `reviews` |

Order amounts are stored in the order currency (`orders.currency`);
`orders.fx_rate` is the number of currency units per 1 EUR on the order date.
Offer prices are in EUR.

Every column, its meaning and possible values are described in
[database.md](database.md). Sample queries: [`examples/queries.sql`](../../examples/queries.sql).

## What makes the data feel alive

**Demand.** Sessions arrive as a non-homogeneous Poisson process per country:
a daily profile in the local time zone, weekdays, months, national holidays
(including Orthodox Easter in Moldova), paydays, AR(1) noise. Returning
customers are picked in proportion to their activity; new ones are brought in by
"marketing", whose budget is adjusted daily to hit the target growth of each
country separately.

**Calendar.** Winter Sale, Valentine's, Easter, Mother's Day, the football
championship (even years), Lumo Days (a July mega sale), Summer Sale, Back to
School, Halloween, Singles Day, Black Friday Week (peaking at ×2.3 on Friday),
Christmas with a delivery deadline and a dip on 24–25 December, After-Christmas
Sale, brand weeks, influencer collaborations (promo codes like `ANNA15`).
Weather waves: heat (fans, air conditioners, swimwear ×3–4) and cold (heaters,
down jackets).

**Catalog.** Product popularity is heavy-tailed: the top 1% of products bring
~20% of revenue, the top 20% bring ~80%, many products barely sell. Products
have a life cycle (growth, plateau, decline, discontinuation and clearance), and
electronics get new generations ("Nova 12 Pro" → "Nova 13 Pro"). Demand reacts
to price (elasticity), review ratings, stock availability and viral spikes.

**Shoppers.** 7 segments (bargain hunters, loyal premium, families,
fashionistas...); department preferences depend on gender, age, kids and pets.
Carts get abandoned (~65%), some are recovered after a reminder email.
Consumables are re-purchased on a cycle. Customers churn (some are one-off
buyers), and a bad experience (late delivery, seller cancellation, lost parcel,
a badly rated product) speeds churn up. Loyalty tiers are recalculated monthly,
and there is a monthly win-back campaign.

**Orders.** Payments fail at method-specific rates (cards ~5%, Klarna ~8%), with
retries, cancellation of unpaid orders, anti-fraud, authorization → capture on
shipment, and cash on delivery refused at the door. The warehouse has a 14:00
cut-off, is closed on Sundays and builds a backlog at peaks. Third-party sellers
ship slower and sometimes cancel. Each country has carriers with their own
speed, delays and losses. Returns within the EU 14-day window (fashion ~30%,
electronics ~5%, "bracketing" — ordering two sizes), multilingual reviews with
ratings driven by product quality and delivery experience.

**Business events over time.** Market launches (PT/IE, PL, CZ, the Nordics,
Moldova) and warehouse openings (Poznań, Zaragoza — delivery times to the
region drop after the opening), seller onboarding, suspension and departure,
purchase orders with supplier lead times and missed deliveries, stock-outs and
lost demand.

**Incidents.** Payment provider and website outages, degradation during Black
Friday, carrier strikes, warehouse delays, a pricing bug (a product at 1–10% of
its price → an order spike → mass cancellation), bot attacks on carts, an
Android app crash. Active events are shown in `/status`.

## Dirt (`dirt_level`, 0..1)

- duplicate customers (the same person with another email), test accounts `@lumomarket.test`
- emails with spaces/capitals, phone numbers in 5 formats, city spellings
  (`München`/`Munich`/`Muenchen`, `Chișinău`/`Chisinau`/`Кишинёв`), postal codes
  without the leading zero, NL postal codes without the space, empty Eircodes
- NULL or HTML descriptions, double spaces in titles, products without a brand
- overselling: negative stock, seller cancellations
- **late-arriving data**: payments that show up in the database with a `created_at` in the past
- **"bug" windows**: for hours or days a given table is updated without touching
  `updated_at` (a trap for incremental loads driven by `updated_at`)
- duplicate rows in `order_status_history`
- `order_items` has no `updated_at` at all (statuses change, visible only via CDC)
- `returns.received_at` in local warehouse time without a time zone
- hard deletes: carts older than 30 days are deleted daily
- GDPR: anonymization of customers and their addresses
- rare 1-cent mismatches in `orders.grand_total`

## Schema evolution

Migrations are applied at a simulated point in time (rows created before a
migration have NULL):

| Migration | When | What |
|---|---|---|
| `m001_shipments_co2` | −580 days | `shipments.co2_grams` |
| `m002_orders_utm` | −470 days | `orders.utm_source`, `utm_campaign` |
| `m003_products_eco_score` | −310 days | `products.eco_score` (+ a bulk backfill 2 months later) |
| `m004_order_items_gift_wrap` | −130 days | `order_items.gift_wrap NOT NULL DEFAULT false` |
| `m005_customers_phone_verified` | **+14 days** | `customers.phone_verified_at` (in live mode) |
| `m006_reviews_media` | **+45 days** | `reviews.media_count` (in live mode) |

Offsets are relative to the first launch. The list lives in
[`internal/app/migrations.go`](../../internal/app/migrations.go).

## CDC

Postgres runs with `wal_level=logical` and `track_commit_timestamp=on`, so
Debezium / logical replication can connect right away.

## Architecture (hexagonal)

```
cmd/lumo                 wiring
internal/domain          entities and reference specs (no dependencies)
internal/ports           Store, ReferenceData (driven), Control (driving)
internal/app             the core: discrete-event simulation engine, behaviour models
internal/adapters/
  postgres               COPY loading, batched UPDATEs via temp tables, snapshot for restarts
  refdata                embedded YAML reference data (categories, brands, countries, names, texts)
  httpapi                time control
  config                 YAML + LUMO_* environment variables
```

The core is a discrete-event simulation with its own clock. Orders, carts and
purchase orders are aggregates with their next step in an event queue. During
the backfill aggregates are written once, when their life cycle is complete
(fast, no UPDATEs); in live mode every change is a real INSERT/UPDATE.

Tests: `go test ./...`; an integration downtime test against a real Postgres
(creates and drops a separate database, checks catching up a week and the
absence of future dates):

```bash
LUMO_TEST_DSN='postgres://lumo:lumo@localhost:5433/lumo?sslmode=disable' \
  go test -tags integration -v -timeout 60m ./internal/integration/
```

Reference data lives in `internal/adapters/refdata/data/*.yaml`: categories,
title templates, brands, cities and names can be edited without touching code.
