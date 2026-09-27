// Package ports defines the boundaries of the application core: driven ports
// (storage, reference data) implemented by adapters, and the driving port
// (simulation control) implemented by the core.
package ports

import (
	"context"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

// ReferenceData provides static world knowledge (taxonomy, geography, names...).
type ReferenceData interface {
	Load() (*domain.ReferenceData, error)
}

// Store is the persistence port. Apply receives a change set that must be
// committed atomically; implementations may encode synchronously and write
// asynchronously, but must preserve batch order.
type Store interface {
	// EnsureSchema creates schemas/tables when absent.
	EnsureSchema(ctx context.Context) error
	// Meta returns persisted simulator metadata (nil if the database is fresh).
	Meta(ctx context.Context) (*Meta, error)
	// Reset drops everything created by the simulator.
	Reset(ctx context.Context) error
	// Load reads back the simulator world for a restart.
	Load(ctx context.Context) (*Snapshot, error)
	// Apply stages a batch for writing.
	Apply(ctx context.Context, b *Batch) error
	// Flush waits until every staged batch is committed.
	Flush(ctx context.Context) error
	// FinalizeBulkLoad adds constraints and indexes after the initial backfill.
	FinalizeBulkLoad(ctx context.Context) error
	// Stats returns row counts of the main tables (approximate is fine).
	Stats(ctx context.Context) (map[string]int64, error)
	Close()
}

// Meta is persisted simulator metadata.
type Meta struct {
	Phase     string    // backfill | live
	SimTime   time.Time // last committed simulation time
	Horizon   time.Time // wall time of the first launch (end of backfill)
	Start     time.Time // start of history
	Seed      uint64
	StateJSON []byte // opaque engine state
}

// Snapshot is everything needed to resume a simulation.
type Snapshot struct {
	Meta       Meta
	Migrations []string
	Categories []*domain.Category
	Brands     []*domain.Brand
	Sellers    []*domain.Seller
	Warehouses []*domain.Warehouse
	Suppliers  []*domain.Supplier
	Products   []*domain.Product
	Variants   []*domain.Variant
	Offers     []*domain.Offer
	Stock      []*domain.StockLevel
	Customers  []*domain.Customer
	Campaigns  []*domain.Campaign
	Coupons    []*domain.Coupon
	FxRates    []domain.FxRate
	Pending    []PendingBlob
}

// PendingBlob is a serialized in-flight aggregate (order, cart, purchase order).
type PendingBlob struct {
	Kind  string
	ID    int64
	DueAt time.Time
	Data  []byte
}

// Migration is a schema change applied at a certain simulation time.
type Migration struct {
	ID  string
	SQL string
}

// Batch is a unit of work produced by the simulation between two checkpoints.
type Batch struct {
	At         time.Time
	Phase      string
	Migrations []Migration

	// reference / catalog
	Currencies []domain.Currency
	Countries  []*domain.Country
	Categories []*domain.Category
	Brands     []*domain.Brand
	Warehouses []*domain.Warehouse
	Suppliers  []*domain.Supplier
	Campaigns  []*domain.Campaign
	FxRates    []domain.FxRate

	NewSellers     []*domain.Seller
	SellerUpdates  []*domain.Seller
	NewProducts    []*domain.Product
	ProductUpdates []*domain.Product
	NewVariants    []*domain.Variant
	NewOffers      []*domain.Offer
	OfferUpdates   []*domain.Offer
	PriceChanges   []*domain.PriceChange
	NewStock       []*domain.StockLevel
	StockUpdates   []*domain.StockLevel
	StockMoves     []*domain.StockMovement
	NewPOs         []*domain.PurchaseOrder
	POUpdates      []*domain.PurchaseOrder

	NewCustomers    []*domain.CustomerProfile
	CustomerUpdates []*domain.Customer
	CustomerPII     []domain.CustomerPII
	NewAddresses    []*domain.Address
	NewCoupons      []*domain.Coupon
	CouponUpdates   []*domain.Coupon

	NewCarts          []*domain.Cart
	CartUpdates       []*domain.Cart
	NewCartItems      []*domain.CartItem
	CartsDeleteBefore time.Time

	// Orders: aggregates whose not-yet-persisted parts must be inserted and
	// persisted-but-changed parts updated.
	Orders       []*domain.Order
	OrderChanges []*OrderChange
	Reviews      []*domain.Review

	// simulator internals
	CustomerTraits []*domain.Customer // upserts
	ProductTraits  []*domain.Product
	SellerTraits   []*domain.Seller
	OfferTraits    []*domain.Offer
	PendingUpserts []PendingBlob
	PendingDeletes []PendingKey
	StateJSON      []byte
	SimTime        time.Time
	Horizon        time.Time
	Start          time.Time
	Seed           uint64
}

type PendingKey struct {
	Kind string
	ID   int64
}

// OrderChange lists what changed in a persisted order since the last batch.
type OrderChange struct {
	Order     *domain.Order
	Root      bool
	Items     []*domain.OrderItem
	Payments  []*domain.Payment
	Shipments []*domain.Shipment
	Returns   []*domain.Return
}

// Size approximates the number of rows in a batch.
func (b *Batch) Size() int {
	n := len(b.NewCustomers) + len(b.CustomerUpdates) + len(b.NewAddresses) + len(b.NewCarts) +
		len(b.NewCartItems) + len(b.CartUpdates) + len(b.Reviews) + len(b.StockMoves) + len(b.PriceChanges) +
		len(b.OfferUpdates) + len(b.StockUpdates) + len(b.NewProducts) + len(b.NewVariants) + len(b.NewOffers)
	for _, o := range b.Orders {
		n += 3 + len(o.Items) + len(o.History)
	}
	n += len(b.OrderChanges) * 2
	return n
}

// Status is a snapshot of the running simulation for the control API.
type Status struct {
	Phase        string    `json:"phase"`
	SimTime      time.Time `json:"sim_time"`
	WallTime     time.Time `json:"wall_time"`
	LagSeconds   float64   `json:"lag_seconds"`
	Speed        float64   `json:"speed"`
	Paused       bool      `json:"paused"`
	CatchingUp   bool      `json:"catching_up"`
	Customers    int       `json:"customers"`
	Products     int       `json:"products"`
	Offers       int       `json:"offers"`
	Sellers      int       `json:"sellers"`
	OpenOrders   int       `json:"open_orders"`
	QueueSize    int       `json:"queue_size"`
	OrdersToday  int       `json:"orders_today"`
	OrdersPerDay []DayStat `json:"orders_last_days"`
	ActiveEvents []string  `json:"active_events"`
	AcqGain      float64   `json:"acquisition_gain"`
	Migrations   []string  `json:"migrations"`
	DirtLevel    float64   `json:"dirt_level"`
	AllowFuture  bool      `json:"allow_future"`
	BackfillPct  float64   `json:"backfill_pct,omitempty"`
}

type DayStat struct {
	Day     string  `json:"day"`
	Orders  int     `json:"orders"`
	Revenue float64 `json:"revenue_eur"`
}

// Control is the driving port used by the HTTP adapter.
type Control interface {
	Status() Status
	SetSpeed(x float64) error
	Pause()
	Resume()
	Resync()
}
