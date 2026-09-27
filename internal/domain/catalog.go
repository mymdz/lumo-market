package domain

import "time"

type Tier uint8

const (
	TierBudget Tier = iota
	TierMid
	TierPremium
)

func (t Tier) String() string {
	switch t {
	case TierBudget:
		return "budget"
	case TierPremium:
		return "premium"
	default:
		return "mid"
	}
}

func ParseTier(s string) Tier {
	switch s {
	case "budget":
		return TierBudget
	case "premium":
		return TierPremium
	default:
		return TierMid
	}
}

type Category struct {
	ID        int32
	ParentID  int32 // 0 = root
	Name      string
	Slug      string
	Path      string
	Level     int
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time

	Dept int        // department index
	Leaf *LeafProps // nil for inner nodes
}

type Brand struct {
	ID           int32
	Name         string
	Tier         Tier
	Country      string
	PrivateLabel bool
	CreatedAt    time.Time

	Dept    int
	Focus   []string
	Quality float64 // latent 0..1
}

// Seller is a merchant on the marketplace. The marketplace itself is the 1P seller.
type Seller struct {
	ID        int32
	Name      string
	LegalName string
	Country   string
	Type      string // "1p" | "3p"
	Status    string // onboarding | active | suspended | closed
	Rating    float64
	JoinedAt  time.Time
	UpdatedAt time.Time
	ClosedAt  time.Time

	// latent traits
	Traits SellerTraits
	// running stats
	Shipped, Cancelled, Late int
}

type SellerTraits struct {
	HandlingDays   float64 `json:"h"`
	CancelRate     float64 `json:"c"`
	LateRate       float64 `json:"l"`
	Aggressiveness float64 `json:"a"`
	Quality        float64 `json:"q"`
	PlatformShare  float64 `json:"p"` // share of offers fulfilled by the platform
	Dept           int     `json:"d"`
	ChurnAt        int64   `json:"x"`
}

type Product struct {
	ID             int64
	CategoryID     int32
	BrandID        int32
	Title          string
	Description    string
	Status         string // active | discontinued
	LaunchedAt     time.Time
	DiscontinuedAt time.Time
	WeightG        int
	Attributes     map[string]string
	EcoScore       string // migration m003
	SuccessorID    int64
	CreatedAt      time.Time
	UpdatedAt      time.Time

	Cat      *Category `json:"-"`
	Brand    *Brand    `json:"-"`
	Variants []*Variant
	Traits   ProductTraits
}

type ProductTraits struct {
	BasePop    float64 `json:"p"`
	Quality    float64 `json:"q"`
	Life       string  `json:"l"`
	LifeScale  float64 `json:"s"` // stretches the lifecycle curve
	ViralUntil int64   `json:"vu"`
	ViralBoost float64 `json:"vb"`
	RatingSum  int     `json:"rs"`
	RatingCnt  int     `json:"rc"`
	Gen        int     `json:"g"`
	Line       string  `json:"ln"`
	Decline    float64 `json:"d"` // extra decline factor after a successor launched
}

type Variant struct {
	ID          int64
	ProductID   int64
	SKU         string
	EAN         string
	Name        string            // "256 GB / Black"
	Attrs       map[string]string // axis -> value
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	PriceFactor float64
	Demand      float64 // relative demand among the product's variants

	Product *Product `json:"-"`
	Offers  []*Offer
}

type Offer struct {
	ID           int64
	VariantID    int64
	SellerID     int32
	Price        Money // EUR, current selling price
	ListPrice    Money // EUR, RRP / strike-through price
	Fulfillment  string
	StockQty     int // for seller-fulfilled offers
	HandlingDays int
	Status       string // active | paused | out_of_stock | deleted
	CreatedAt    time.Time
	UpdatedAt    time.Time

	Variant *Variant `json:"-"`
	Seller  *Seller  `json:"-"`
	Stock   []*StockLevel

	// traits
	BasePrice Money   // regular price (without campaign/clearance)
	Cost      Money   // purchase cost for the 1P / platform stock
	DemandEMA float64 // units per day
	PromoID   int32   // campaign currently applied (0 = none)
}

const (
	FulfilPlatform = "platform"
	FulfilSeller   = "seller"
)

// Available units of an offer (sum of warehouses for platform offers).
func (o *Offer) Available() int {
	if o.Fulfillment == FulfilSeller {
		return o.StockQty
	}
	n := 0
	for _, s := range o.Stock {
		n += s.OnHand - s.Reserved
	}
	return n
}

type PriceChange struct {
	ID        int64
	OfferID   int64
	OldPrice  Money
	NewPrice  Money
	Reason    string
	ChangedAt time.Time
}
