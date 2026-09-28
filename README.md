# Lumo Market

**A fake European marketplace that fills a living, messy e-commerce database
to practice data engineering on.**

[Русская версия](docs/ru/overview.md)

Lumo Market is a simulator of a fictional online marketplace. It writes
everything that happens in the shop into PostgreSQL: orders, payments,
shipments, returns, price changes, stock movements, reviews and more. It first
generates two years of history in about 12 minutes, then keeps running in real time.

## Why

Learning lakehouses, CDC, dbt or BI needs data that behaves like a real
production database. Public datasets are static snapshots: nothing changes,
nothing arrives late, nothing breaks your incremental model at 3 a.m.

This project was built to fix that. It gives you an OLTP source that:

- **keeps changing**: new orders every second, and old rows change as orders
  get paid, shipped, delivered, returned and refunded;
- **has a story**: seasonality, Black Friday, Christmas, market launches,
  warehouse openings, product generations, customer churn and loyalty tiers;
- **is realistically dirty**: duplicate customers, inconsistent formats,
  late-arriving rows, `updated_at` that sometimes doesn't move, hard deletes,
  GDPR erasure, schema migrations in the middle of history;
- **survives downtime**: switch it off for a week, and on restart it replays
  the missed week (about 30 seconds) without ever writing a timestamp in the future.

## What you can practice with it

- **CDC**: Postgres runs with `wal_level=logical`, ready for Debezium, Kafka
  or native logical replication. `order_items` has no `updated_at`, so some
  changes are visible only through CDC.
- **Incremental loads**: loading by `updated_at`, with bug windows where
  `updated_at` doesn't move, late-arriving payments, hard-deleted carts.
- **Lakehouse**: land the data into Iceberg / Delta / Hudi and handle upserts,
  deletes and schema evolution (six migrations add columns over time, two of
  them after you start).
- **dbt / modelling**: staging, SCD2 snapshots of products, offers and
  customers, a star schema around orders, multi-currency revenue with daily FX
  rates, time zones.
- **Data quality**: tests for duplicates, formats, referential integrity
  (FKs are `NOT VALID` for history), totals that drift by a cent.
- **BI and analytics**: seasonality, campaign uplift, cohorts and retention,
  Pareto revenue, return rates, delivery SLAs by carrier, payment failures
  during provider outages.

## Quick start

Requires Docker.

```bash
git clone https://github.com/mymdz/lumo-market.git
cd lumo-market
docker compose up -d --build
docker compose logs -f simulator     # watch the history backfill
curl localhost:8088/status           # phase, simulated clock, orders per day
```

Connect to Postgres at `localhost:5433` (user `lumo`, password `lumo`,
database `lumo`). Business data is in the `shop` schema; start with
[`examples/queries.sql`](examples/queries.sql).

The default full run (730 days, 5k → 13k orders/day) needs about **17–20 GB of
disk** and **~3 GB of RAM** for the simulator. For a quick try, lower
`history_days` (e.g. to 14) and `initial_products` (e.g. to 3000) before the
first start.

Settings are layered: [`config.yaml`](config.yaml) holds the committed
defaults, and an optional git-ignored `config.local.yaml` next to it overrides
only the keys it contains. Copy
[`config.local.example.yaml`](config.local.example.yaml) to get started. Both
files are mounted into the container. Every key can also be overridden with a
`LUMO_<NAME>` environment variable, e.g. `LUMO_HISTORY_DAYS=14`, which wins
over both files.

Other useful settings: `dirt_level: 0` for clean data, `seed` for a different
but reproducible world.

## What's inside

| Area | Tables |
|---|---|
| Reference data | `currencies`, `countries`, `fx_rates`, `warehouses`, `categories`, `brands` |
| Catalog | `products`, `product_variants`, `offers`, `price_history` |
| Marketplace | `sellers` (own retail + hundreds of third-party sellers) |
| Warehouse | `stock_levels`, `stock_movements`, `suppliers`, `purchase_orders`, `purchase_order_items` |
| Customers | `customers`, `addresses` |
| Marketing | `campaigns`, `coupons` |
| Sales | `carts`, `cart_items`, `orders`, `order_items`, `order_status_history`, `payments`, `refunds`, `shipments`, `returns`, `reviews` |

About 20,000 products in 390 categories, 15 countries, 6 currencies (EUR, PLN,
CZK, SEK, DKK, MDL) with per-country VAT and local payment methods.

## Controlling time

```bash
curl -XPOST 'localhost:8088/speed?x=0.5'   # slow down (lags behind wall time)
curl -XPOST localhost:8088/pause
curl -XPOST localhost:8088/resume
curl -XPOST localhost:8088/resync          # back to speed 1, catch up
```

Simulated time never runs ahead of wall time unless you explicitly set
`allow_future: true`.

## Documentation

| | English | Русский |
|---|---|---|
| Detailed guide: how the simulation works, dirt, schema evolution, architecture | [overview](docs/en/overview.md) | [обзор](docs/ru/overview.md) |
| Every table and column | [database](docs/en/database.md) | [схема БД](docs/ru/database.md) |

## Development

Written in Go with a hexagonal architecture: a discrete-event simulation core
(`internal/app`) and adapters for Postgres, embedded YAML reference data, the
HTTP control API and config.

```bash
go build ./...
go test ./...
go run ./cmd/lumo -config config.yaml run|reset|check
```

Catalog content (categories, brands, cities, names, review texts) lives in
`internal/adapters/refdata/data/*.yaml` and can be changed without touching code.

## DISCLAIMER

All data (names, addresses, emails, etc.) generated by this project is synthetic and randomly produced. Any resemblance to real persons, companies or actual customer data is purely coincidental.

This priject is not intended to ingest or process real user data.
