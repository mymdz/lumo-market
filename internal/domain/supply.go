package domain

import "time"

type Warehouse struct {
	ID       int16
	Code     string
	Name     string
	Country  string
	City     string
	TZName   string
	OpenedAt time.Time

	TZ       *time.Location
	Lat, Lon float64
}

type StockLevel struct {
	WarehouseID  int16
	OfferID      int64
	OnHand       int
	Reserved     int
	ReorderPoint int
	UpdatedAt    time.Time

	OpenPO int64 `json:"-"` // purchase order in flight (0 = none)
}

type StockMovement struct {
	ID          int64
	WarehouseID int16
	OfferID     int64
	Delta       int
	Reason      string // inbound | sale | return | adjustment | damaged
	RefType     string
	RefID       int64
	CreatedAt   time.Time
}

type Supplier struct {
	ID          int32
	BrandID     int32
	Name        string
	Country     string
	LeadDays    float64
	Reliability float64
	CreatedAt   time.Time
}

type PurchaseOrder struct {
	ID          int64
	SellerID    int32 // 1P seller for own purchasing, 3P seller for inbound of fulfilled-by-platform stock
	SupplierID  int32 // 0 for 3P inbound shipments
	WarehouseID int16
	Status      string // placed | confirmed | shipped | received | cancelled
	OrderedAt   time.Time
	ExpectedAt  time.Time
	ReceivedAt  time.Time
	UpdatedAt   time.Time
	Items       []*POItem

	NextAt   time.Time
	NextStep string
}

type POItem struct {
	ID          int64
	POID        int64
	OfferID     int64
	QtyOrdered  int
	QtyReceived int
	UnitCost    Money
}
