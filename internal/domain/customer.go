package domain

import "time"

// MaxDepts bounds the number of top-level departments (affinity vector size).
const MaxDepts = 20

type Segment uint8

const (
	SegRegular Segment = iota
	SegBargain
	SegLoyal
	SegOccasional
	SegFamily
	SegTech
	SegFashion
	NumSegments
)

var segmentNames = [...]string{"regular", "bargain_hunter", "loyal_premium", "occasional", "family", "tech_enthusiast", "fashionista"}

func (s Segment) String() string {
	if int(s) < len(segmentNames) {
		return segmentNames[s]
	}
	return "unknown"
}

const (
	CustActive  uint8 = 0
	CustDeleted uint8 = 1
	CustBlocked uint8 = 2
)

const (
	FlagFraud uint8 = 1 << iota
	FlagTest
	FlagOptIn
	FlagDuplicate
)

var LoyaltyTiers = [...]string{"none", "silver", "gold", "platinum"}

var Channels = [...]string{"organic", "paid_search", "social", "affiliate", "email", "referral", "direct", "influencer"}

var Devices = [...]string{"web_desktop", "web_mobile", "ios_app", "android_app"}

// Customer is the in-memory, compact representation of a registered customer.
// PII lives in the database only (see CustomerProfile).
type Customer struct {
	ID              int64
	AddressID       int64
	CreatedAt       int64
	UpdatedAt       int64
	ChurnAt         int64
	LastOrderAt     int64
	SpentAt         int64
	PhoneVerifiedAt int64   // migration m005
	Rate            float32 // shopping sessions per day while active
	PriceSens       float32 // 0..1
	ReturnProp      float32 // multiplier of return probability
	CouponAff       float32 // 0..1
	Satisfaction    float32 // -1..1
	Spent           float32 // exponentially decayed EUR spend
	Orders          uint16
	Country         uint8
	Segment         Segment
	Gender          uint8 // 0 unknown, 1 female, 2 male
	Age             uint8
	Device          uint8
	Channel         uint8
	PayPref         uint8 // index into the country's payment methods, 255 = none
	Tier            uint8
	Status          uint8
	Flags           uint8
	Active          bool // not churned
	Affinity        [MaxDepts]uint8
	FenIdx          int32 // index in the per-country sampling tree
	Repl            []Replenish
}

// Replenish is a consumable a customer re-buys periodically.
type Replenish struct {
	OfferID int64
	DueAt   int64
	Cycle   int32
}

func (c *Customer) Has(f uint8) bool { return c.Flags&f != 0 }

// CustomerProfile carries PII for inserting a customer row.
type CustomerProfile struct {
	C         *Customer
	Email     string
	FirstName string
	LastName  string
	Phone     string
	BirthDate time.Time
	Language  string
	Country   string
}

// CustomerPII is a change of personal data (email/phone change, GDPR erasure).
type CustomerPII struct {
	CustomerID int64
	Email      string
	FirstName  *string
	LastName   *string
	Phone      *string
	Erase      bool
	At         time.Time
}

type Address struct {
	ID         int64
	CustomerID int64 // 0 = guest
	Recipient  string
	Line1      string
	Line2      string
	PostalCode string
	City       string
	Country    string
	Phone      string
	CreatedAt  time.Time
}

// Country is the runtime view of a market.
type Country struct {
	Idx        int
	Code       string
	Name       string
	Currency   string
	VAT        float64
	VATReduced float64
	TZ         *time.Location
	EU         bool
	LaunchedAt time.Time
	Spec       *CountrySpec
}
