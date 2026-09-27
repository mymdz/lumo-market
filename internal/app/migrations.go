package app

import (
	"time"

	"github.com/mymdz/lumo-market/internal/ports"
)

// schemaMigration is a schema change that happens at a point in simulated
// time, like a real product team shipping a feature. Offsets are relative to
// the first launch ("now"): negative ones happen during the backfill, positive
// ones while running live. Rows created before a migration keep NULLs.
type schemaMigration struct {
	ID     string
	Offset int // days relative to the horizon
	SQL    string
}

var schemaMigrations = []schemaMigration{
	{"m001_shipments_co2", -580, "ALTER TABLE shop.shipments ADD COLUMN IF NOT EXISTS co2_grams integer"},
	{"m002_orders_utm", -470, "ALTER TABLE shop.orders ADD COLUMN IF NOT EXISTS utm_source text, ADD COLUMN IF NOT EXISTS utm_campaign text"},
	{"m003_products_eco_score", -310, "ALTER TABLE shop.products ADD COLUMN IF NOT EXISTS eco_score char(1)"},
	{"m004_order_items_gift_wrap", -130, "ALTER TABLE shop.order_items ADD COLUMN IF NOT EXISTS gift_wrap boolean NOT NULL DEFAULT false"},
	{"m005_customers_phone_verified", 14, "ALTER TABLE shop.customers ADD COLUMN IF NOT EXISTS phone_verified_at timestamptz"},
	{"m006_reviews_media", 45, "ALTER TABLE shop.reviews ADD COLUMN IF NOT EXISTS media_count smallint"},
}

func (w *World) migrationTime(m schemaMigration) time.Time {
	return w.horizon.Add(time.Duration(m.Offset) * 24 * time.Hour)
}

func (w *World) applyDueMigrations() {
	for _, m := range schemaMigrations {
		if w.applied[m.ID] || w.now.Before(w.migrationTime(m)) {
			continue
		}
		w.applied[m.ID] = true
		w.appliedIDs = append(w.appliedIDs, m.ID)
		w.uow.b.Migrations = append(w.uow.b.Migrations, ports.Migration{ID: m.ID, SQL: m.SQL})
		if !w.now.Equal(w.start) {
			w.note("schema migration applied: %s", m.ID)
		}
	}
	// follow-up data backfill two months after the eco score column appeared
	if w.applied["m003_products_eco_score"] && !w.ecoBackfilled {
		for _, m := range schemaMigrations {
			if m.ID == "m003_products_eco_score" && w.now.After(w.migrationTime(m).AddDate(0, 2, 0)) {
				w.ecoBackfilled = true
				n := 0
				for _, id := range w.sortedProductIDs() {
					p := w.products[id]
					if p.EcoScore == "" && p.Status != "deleted" && w.rng.Bool(0.4) {
						p.EcoScore = Pick(w.rng, []string{"A", "B", "B", "C", "C", "C", "D", "E"})
						p.UpdatedAt = w.now
						w.uow.product(p)
						n++
					}
				}
				w.note("eco score backfill job updated %d products", n)
			}
		}
	}
	// phone verification rolls out to existing customers after m005
	if w.applied["m005_customers_phone_verified"] {
		n := w.rng.Poisson(float64(len(w.customers)) * 0.002 / 24)
		for i := 0; i < n && len(w.customers) > 0; i++ {
			c := w.customers[w.rng.IntN(len(w.customers))]
			if c != nil && c.PhoneVerifiedAt == 0 && c.Status == 0 {
				c.PhoneVerifiedAt = w.now.Unix()
				w.touchCustomer(c)
			}
		}
	}
}
