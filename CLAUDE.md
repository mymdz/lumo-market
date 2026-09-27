# lumo-market

Go simulator of a European marketplace ("Lumo Market") that writes realistic,
continuously changing OLTP data into PostgreSQL (schema `shop`, internal state
in schema `sim`). Used as a source for lakehouse / CDC / BI experiments.
User-facing docs: `README.md` (English, GitHub landing page) and `docs/en/`,
`docs/ru/` (`overview.md` — detailed guide, `database.md` — every table/column).
Keep EN and RU docs in sync when behaviour or schema changes.

## Commands

```bash
go build ./...
go test ./...                                   # unit tests
LUMO_TEST_DSN='postgres://lumo:lumo@localhost:5433/lumo?sslmode=disable' \
  go test -tags integration -v -timeout 60m ./internal/integration/   # real Postgres, uses a throwaway DB
docker compose up -d --build                    # Postgres :5433, control API :8088
go run ./cmd/lumo -config config.yaml run|reset|check
curl localhost:8088/status
```

Compose publishes non-default host ports 5433/8088 to avoid clashing with a local
Postgres or other services (`LUMO_PG_PORT`, `LUMO_API_PORT`). Every
config key can be overridden with `LUMO_<NAME>` when running the binary
directly (see `internal/adapters/config`); the compose container only reads the
mounted `config.yaml`.

Quick experiments: `LUMO_HISTORY_DAYS=14 LUMO_INITIAL_PRODUCTS=3000`.
A full 730-day backfill takes ~12 min and ~17-20 GB; the process peaks ~2.6 GB RAM.

## Architecture (hexagonal — keep it that way)

- `internal/domain` — entities and reference specs. No dependencies on anything else.
- `internal/ports` — `Store`, `ReferenceData` (driven), `Control` (driving), `Batch` (unit of work DTO).
- `internal/app` — the core: discrete-event simulation.
  - `engine.go` loop, checkpoints, control port; `clock.go` sim/wall time mapping
  - `world.go` in-memory state; `uow.go` write policy; `state.go` persisted engine state; `restore.go` rebuild from DB
  - behaviour: `shopper.go` (sessions → carts → orders), `lifecycle.go` (payment, shipment, returns, reviews),
    `supply.go` (purchase orders), `jobs.go` (hourly/nightly jobs), `calendar.go` (holidays, campaigns,
    weather, incidents), `catalog.go`, `people.go`, `bootstrap.go`, `migrations.go`
- `internal/adapters/postgres` — COPY inserts, UPDATE/UPSERT via temp tables, async writer, snapshot `Load`, `FutureDated` check.
- `internal/adapters/refdata` — embedded YAML: taxonomy, brands, geo, names, texts. Content changes go here, not in code.
- `internal/adapters/httpapi`, `internal/adapters/config`, `cmd/lumo` (wiring only).

The core never imports adapters; adapters map domain objects to SQL.

## How the simulation works

- Single goroutine owns the `World`; the store encodes a batch synchronously in
  `Apply` and writes it asynchronously (queue depth 1), one transaction per batch.
- Time advances in 1-minute ticks; sessions are generated per market from a
  non-homogeneous Poisson rate; aggregates (orders, carts, POs) schedule their
  next step in a heap (`queue.go`); stale events are detected by comparing `NextAt`.
- Phases: **backfill** (history, fast, no updates: aggregates are inserted once
  settled, mutable entities flushed at the end) → **live** (every change is a
  real INSERT/UPDATE, commit every `live_flush`).
- Backfill is not resumable: if it dies, the next start drops everything and
  starts over. Live mode is crash-safe: all in-flight state (`sim.pending`,
  traits, `sim.meta.state`) is committed in the same transaction as the data.
- Downtime: at speed 1 the clock catches up as fast as possible whenever sim
  time is > 5 s behind wall time (a week at ~13k orders/day ≈ 30 s). Speed < 1
  lags on purpose; `resync` returns to speed 1 (and catches up).
- A per-market acquisition "controller" nudges new-customer traffic daily so
  orders follow `orders_per_day_start → orders_per_day_now` × seasonality.

## Invariants — do not break

- **No event is dated in the future.** Sim time never passes wall time unless
  `allow_future: true` (only then may speed > 1). Anything that plans ahead must
  not store the planned moment in an event column: publish rows when the moment
  comes (see campaigns in `jobs.go:ensureYears`, hourly `launches`). Plan
  columns that may be in the future: `shipments.promised_at`,
  `purchase_orders.expected_at`, `campaigns.starts_at/ends_at`,
  `coupons.valid_from/valid_to`. `returns.received_at` is deliberately local
  warehouse time without a time zone. Verify with `lumo check`
  (also asserted by the integration test).
- Determinism: all randomness comes from `w.rng` or seeded `NewRand(hashSeed(...))`
  (calendar, brand quality). Iterate maps in sorted order where it affects output
  (`sortedProductIDs`).
- IDs are generated in-process (`w.ids`) and persisted in engine state.
- Schema changes to `shop` after release go through `internal/app/migrations.go`
  (offset in days relative to the first launch) and the column must be tagged
  `name@migration_id` in `adapters/postgres/encode.go`. Rows created before the
  migration keep NULL. Base tables live in `schema.sql`; FKs/indexes in
  `constraints.sql` (applied after backfill, `NOT VALID`).
- New mutable fields that must survive restarts need to be persisted (row, trait
  table, pending blob or `engineState`) and restored in `restore.go` + `load.go`.
- Large writes must stay chunked (`dumpState`); a single huge batch OOM-killed
  the container before (in an ~8 GB Docker Desktop VM).
- Dirty-data generators use `w.dirty(p)` so `dirt_level: 0` yields clean data.

## Conventions

- Code, identifiers, comments and YAML content in English; docs in both English and Russian.
- Match the surrounding style: small focused functions on `*World`, comments
  explain *why* (business logic), not what.
- After changing behaviour, validate on real data: run a short history against
  the compose Postgres and query it (see `examples/queries.sql`).
