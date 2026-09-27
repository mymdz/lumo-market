// Package app is the application core: it simulates the marketplace and emits
// changes through the ports.
package app

import (
	"log/slog"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

// Config tunes the simulation.
type Config struct {
	Seed               uint64
	HistoryDays        int
	OrdersStart        float64 // baseline orders/day at the start of history
	OrdersNow          float64 // baseline orders/day at the first launch
	GrowthAfter        float64 // yearly baseline growth after the first launch
	InitialProducts    int
	InitialSellers     int
	Dirt               float64 // 0..1
	Speed              float64
	Tick               time.Duration
	LiveFlush          time.Duration
	BackfillCheckpoint time.Duration
	CatchUpLag         time.Duration
	AllowFuture        bool // allow speed > 1, i.e. simulated time ahead of wall time
	Now                func() time.Time
}

func (c *Config) defaults() {
	if c.Seed == 0 {
		c.Seed = 42
	}
	if c.HistoryDays < 0 {
		c.HistoryDays = 0
	}
	if c.OrdersStart <= 0 {
		c.OrdersStart = 5000
	}
	if c.OrdersNow <= 0 {
		c.OrdersNow = 13000
	}
	if c.InitialProducts <= 0 {
		c.InitialProducts = 20000
	}
	if c.InitialSellers <= 0 {
		c.InitialSellers = 250
	}
	if c.Speed <= 0 {
		c.Speed = 1
	}
	if c.Tick <= 0 {
		c.Tick = time.Minute
	}
	if c.LiveFlush <= 0 {
		c.LiveFlush = time.Second
	}
	if c.BackfillCheckpoint <= 0 {
		c.BackfillCheckpoint = 24 * time.Hour
	}
	if c.CatchUpLag <= 0 {
		c.CatchUpLag = 5 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

const (
	PhaseBackfill = "backfill"
	PhaseLive     = "live"
)

// market is the runtime view of a country.
type market struct {
	*domain.Country
	spec       *domain.CountrySpec
	cur        *currencyState
	cities     *Picker[*domain.CitySpec]
	langs      *Picker[string]
	carriers   *Picker[string]
	lockers    *Picker[string]
	payments   *Picker[string]
	payList    []string
	emails     *Picker[string]
	fen        Fenwick
	fenIDs     []int64 // fenwick index -> customer id
	sAcc       float64 // integrated session factor of the current day (minutes)
	targets    []float64
	actuals    []float64
	noise      float64 // daily AR(1) log noise
	freeOver   domain.Money
	shipFee    domain.Money
	express    domain.Money
	locker     domain.Money
	sessionAcc float64 // fractional expected sessions carried between ticks
	newAcc     float64
}

type currencyState struct {
	domain.Currency
	rate      float64 // today's rate (units per EUR)
	priceRate float64 // weekly price-list rate
}

type dept struct {
	idx     int
	cat     *domain.Category
	name    string
	weight  float64
	leaves  []*leaf
	leafCum []float64 // hourly cached leaf weights
	leafTot float64
	nowW    float64 // hourly cached department demand multiplier
}

type leaf struct {
	cat      *domain.Category
	p        *domain.LeafProps
	dept     *dept
	products []*domain.Product
	cum      []float64
	total    float64
	related  []*leaf
	brands   []*domain.Brand
	life     domain.LifecycleSpec
	season   [12]float64
	nowW     float64 // hourly cached leaf multiplier
	lastSold int64
}

// cartState is a cart in flight together with who is behind it.
type cartState struct {
	Cart      *domain.Cart
	CustID    int64
	Prospect  *prospect
	Market    string
	Stage     string // decide | recover
	Budget    float64
	Replenish bool
	Recovered bool
	Bot       bool
}

// prospect is a not-yet-registered visitor.
type prospect struct {
	C       domain.Customer
	Profile domain.CustomerProfile
	Addr    domain.Address
}

type idGen struct {
	Category, Brand, Seller, Warehouse, Supplier                int64
	Product, Variant, Offer, PriceChange, StockMove, PO, POItem int64
	Customer, Address, Campaign, Coupon                         int64
	Cart, CartItem, Order, OrderItem, History, Payment, Refund  int64
	Shipment, Return, Review                                    int64
}

type dayStat struct {
	Day     time.Time
	Orders  int
	Revenue float64
	Target  float64
	SFactor float64
}

type stats struct {
	today      dayStat
	days       []dayStat
	sAcc       float64 // integrated seasonal factor (minutes)
	sMin       float64
	lostDemand int
	sessions   int
	offerSales map[int64]int
}

type controller struct {
	Gain float64
}

type dupPlan struct {
	at      time.Time
	profile domain.CustomerProfile
	addr    domain.Address
	market  string
	src     domain.Customer
}

// World is the complete in-memory state of the simulation.
type World struct {
	cfg Config
	ref *domain.ReferenceData
	rng *Rand
	log *slog.Logger

	now     time.Time
	start   time.Time
	horizon time.Time
	phase   string

	currencies   map[string]*currencyState
	markets      []*market
	marketByCode map[string]*market
	warehouses   []*domain.Warehouse
	whByID       map[int16]*domain.Warehouse
	carriers     map[string]domain.CarrierSpec

	depts       []*dept
	cats        []*domain.Category
	catByID     map[int32]*domain.Category
	leaves      []*leaf
	leafByCat   map[int32]*leaf
	brands      []*domain.Brand
	brandByName map[string]*domain.Brand
	brandByID   map[int32]*domain.Brand
	brandPools  map[string][]*domain.Brand
	suppliers   map[int32]*domain.Supplier // by brand id
	sellers     []*domain.Seller
	sellerByID  map[int32]*domain.Seller
	ownSeller   *domain.Seller
	products    map[int64]*domain.Product
	variants    map[int64]*domain.Variant
	offers      map[int64]*domain.Offer

	customers []*domain.Customer // index = id-1
	orders    map[int64]*domain.Order
	carts     map[int64]*cartState
	pos       map[int64]*domain.PurchaseOrder

	campaigns    []*domain.Campaign
	coupons      []*domain.Coupon
	couponByCode map[string]*domain.Coupon
	cal          *calendar

	q          eventQueue
	ids        idGen
	uow        *uow
	st         stats
	ctrl       controller
	applied    map[string]bool
	appliedIDs []string
	dups       []dupPlan
	testCusts  []int64

	poWritten map[int64]bool
	late      []lateTouch

	sellersByDept  [][]*domain.Seller
	activeSellers  []*domain.Seller
	gains          map[string]float64
	orderCount     map[string]int
	events         []string // recent notable events for the status API
	needCheckpoint bool

	lastHourly    time.Time
	lastDaily     time.Time
	nightlyDone   time.Time
	ecoBackfilled bool
}

// lateTouch re-touches an order when a late-arriving row becomes visible.
type lateTouch struct {
	at time.Time
	o  *domain.Order
}

func (w *World) customer(id int64) *domain.Customer {
	if id <= 0 || int(id) > len(w.customers) {
		return nil
	}
	return w.customers[id-1]
}

func (w *World) live() bool { return w.phase == PhaseLive }

func (w *World) daysSinceStart() float64 { return w.now.Sub(w.start).Hours() / 24 }

func dayOf(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
