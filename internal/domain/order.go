package domain

import "time"

// Order statuses.
const (
	OrderPendingPayment    = "pending_payment"
	OrderPaid              = "paid"
	OrderProcessing        = "processing"
	OrderPartiallyShipped  = "partially_shipped"
	OrderShipped           = "shipped"
	OrderDelivered         = "delivered"
	OrderPartiallyReturned = "partially_returned"
	OrderReturned          = "returned"
	OrderCancelled         = "cancelled"
)

// Shipment statuses.
const (
	ShipPending   = "pending"
	ShipPacked    = "packed"
	ShipShipped   = "shipped"
	ShipInTransit = "in_transit"
	ShipDelivered = "delivered"
	ShipLost      = "lost"
	ShipReturned  = "returned_to_sender"
	ShipCancelled = "cancelled"
)

// Payment statuses.
const (
	PayPending           = "pending"
	PayAuthorized        = "authorized"
	PayCaptured          = "captured"
	PayFailed            = "failed"
	PayVoided            = "voided"
	PayRefunded          = "refunded"
	PayPartiallyRefunded = "partially_refunded"
	PayChargeback        = "chargeback"
)

// Return statuses.
const (
	RetRequested = "requested"
	RetInTransit = "in_transit"
	RetReceived  = "received"
	RetRefunded  = "refunded"
	RetRejected  = "rejected"
)

// Order is the aggregate root of a customer purchase.
type Order struct {
	ID           int64
	Number       string
	CustomerID   int64 // 0 = guest checkout
	GuestEmail   string
	CartID       int64
	Status       string
	Currency     string
	FX           float64 // units of Currency per 1 EUR
	Country      string
	Subtotal     Money
	Discount     Money
	ShippingFee  Money
	Tax          Money
	Total        Money
	CouponID     int32
	ShipAddrID   int64
	BillAddrID   int64
	Channel      string // device / sales channel
	ShipMethod   string
	PlacedAt     time.Time
	PaidAt       time.Time
	CancelledAt  time.Time
	CancelReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	UTMSource    string // migration m002
	UTMCampaign  string // migration m002

	Items     []*OrderItem
	Payments  []*Payment
	Shipments []*Shipment
	Returns   []*Return
	Refunds   []*Refund
	History   []*StatusChange

	Sim OrderSim
}

// OrderSim holds simulation-only state of an order.
type OrderSim struct {
	Fraud     bool
	Test      bool
	Late      bool
	BadExp    bool
	NewCust   bool
	PayTries  int
	NextAt    time.Time
	Step      string // order-level step: pay, pay_retry, cancel_unpaid, customer_cancel, chargeback
	StepAt    time.Time
	Settled   bool
	Persisted bool
	Dirty     bool
}

type OrderItem struct {
	ID          int64
	OrderID     int64
	LineNo      int
	OfferID     int64
	VariantID   int64
	ProductID   int64
	SellerID    int32
	Title       string
	Qty         int
	UnitPrice   Money
	Discount    Money
	TaxRate     float64
	Tax         Money
	LineTotal   Money
	Commission  float64
	Status      string // ordered | cancelled | shipped | delivered | returned
	GiftWrap    bool   // migration m004
	ShipmentIdx int

	ReturnAt    time.Time
	ReviewAt    time.Time
	ReturnedQty int
	Bracket     bool // bought in several sizes, extra ones will be returned
	Dirty       bool
}

type Payment struct {
	ID             int64
	OrderID        int64
	Method         string
	Provider       string
	Status         string
	Amount         Money
	Currency       string
	FailureReason  string
	RefundedAmount Money
	CreatedAt      time.Time
	CapturedAt     time.Time
	UpdatedAt      time.Time
	VisibleAt      time.Time // late-arriving webhook: row becomes visible later than CreatedAt
	Dirty          bool
	Persisted      bool
}

type Shipment struct {
	ID          int64
	OrderID     int64
	SellerID    int32
	WarehouseID int16
	Carrier     string
	Tracking    string
	Status      string
	Method      string
	PromisedAt  time.Time
	ShippedAt   time.Time
	DeliveredAt time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CO2Grams    int // migration m001

	NextAt    time.Time
	NextStep  string
	TransitD  float64
	Dirty     bool
	Persisted bool
}

type Return struct {
	ID          int64
	OrderID     int64
	OrderItemID int64
	Qty         int
	Reason      string
	Status      string
	RequestedAt time.Time
	ReceivedAt  time.Time // stored as local warehouse time without tz (legacy WMS)
	RefundAmt   Money
	UpdatedAt   time.Time

	WarehouseID int16
	NextAt      time.Time
	NextStep    string
	Persisted   bool
	Dirty       bool
}

type Refund struct {
	ID        int64
	PaymentID int64
	OrderID   int64
	ReturnID  int64
	Amount    Money
	Reason    string
	CreatedAt time.Time
	Persisted bool
}

type StatusChange struct {
	ID        int64
	OrderID   int64
	From      string
	To        string
	Actor     string
	ChangedAt time.Time
	Persisted bool
}

type Review struct {
	ID          int64
	ProductID   int64
	CustomerID  int64
	OrderItemID int64
	Rating      int
	Title       string
	Body        string
	Language    string
	Verified    bool
	Helpful     int
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	MediaCount  int // migration m006
}

type Cart struct {
	ID               int64
	CustomerID       int64
	SessionID        string
	Status           string // active | abandoned | converted
	Country          string
	Currency         string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ConvertedOrderID int64
	Items            []*CartItem

	NextAt    time.Time
	Persisted bool
}

type CartItem struct {
	ID        int64
	CartID    int64
	OfferID   int64
	Qty       int
	UnitPrice Money
	AddedAt   time.Time
}

type Campaign struct {
	ID          int32
	Name        string
	Type        string
	StartsAt    time.Time
	EndsAt      time.Time
	DiscountPct float64
	CreatedAt   time.Time

	Traffic   float64
	Tags      []string
	Depts     []int
	Countries []string
	Share     float64 // share of offers in scope that get discounted
	BrandID   int32
	AcqBoost  float64
	Started   bool
	Ended     bool
}

type Coupon struct {
	ID            int32
	Code          string
	CampaignID    int32
	DiscountType  string // percent | fixed | free_shipping
	DiscountValue float64
	MinOrder      Money
	ValidFrom     time.Time
	ValidTo       time.Time
	MaxUses       int
	TimesUsed     int
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Kind      string // welcome | newsletter | campaign | influencer | winback | loyalty
	Countries []string
}

type FxRate struct {
	Date     time.Time
	Currency string
	Rate     float64
}
