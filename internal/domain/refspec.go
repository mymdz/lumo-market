package domain

// Reference specifications: static knowledge about the world (catalog
// taxonomy, geography, people, texts). They are provided by a reference-data
// adapter and turned into live entities by the application core.

// ReferenceData bundles all reference specifications.
type ReferenceData struct {
	Catalog CatalogSpec
	Geo     GeoSpec
	People  PeopleSpec
	Texts   TextSpec
}

type CatalogSpec struct {
	Pools       map[string][]string
	Styles      map[string][]string
	Axes        map[string]AxisSpec
	Seasons     map[string][12]float64
	Lifecycles  map[string]LifecycleSpec
	Brands      map[string][]BrandSpec
	Departments []*CategorySpec
}

// AxisSpec is a variant dimension (size, colour, storage...).
type AxisSpec struct {
	Name    string
	Values  []string
	Weights []float64 // demand weight per value (optional)
	Price   []float64 // price multiplier per value (optional)
	// PickMin/PickMax: how many values a product uses. 0/0 = all values.
	// Values with price multipliers are picked as a contiguous range from
	// the start, others randomly.
	PickMin, PickMax int
}

type LifecycleSpec struct {
	RampDays    float64
	PlateauDays float64
	DecayDays   float64
	Floor       float64
}

type BrandSpec struct {
	Name    string
	Tier    Tier
	Country string
	Own     bool     // private label of the marketplace itself
	Focus   []string // category names the brand is specialised in (empty = whole dept)
}

// CategorySpec is a node of the taxonomy tree; leaves carry fully resolved
// LeafProps (inheritance is resolved by the adapter).
type CategorySpec struct {
	Name     string
	Children []*CategorySpec
	Props    LeafProps
}

func (c *CategorySpec) IsLeaf() bool { return len(c.Children) == 0 }

type LeafProps struct {
	Noun     string
	PriceMin float64
	PriceMax float64
	Axes     []string
	Tpl      []string
	Pools    map[string][]string
	Weight   float64 // relative demand weight inside its department
	Season   string
	Tags     []string
	Returns  float64 // base return probability of an item
	Own      float64 // share of products whose primary offer is 1P
	Repeat   int     // consumable repurchase cycle in days (0 = not consumable)
	MaxQty   int
	Related  []string
	Style    string
	Brands   string
	VAT      string // "std" | "reduced"
	Life     string
	Density  float64 // products per unit of weight
	WeightKg float64
}

type GeoSpec struct {
	Currencies []Currency
	Countries  []CountrySpec
	Warehouses []WarehouseSpec
	Carriers   map[string]CarrierSpec
}

type CountrySpec struct {
	Code        string
	Name        string
	Currency    string
	VAT         float64
	VATReduced  float64
	TZ          string
	EU          bool
	Langs       map[string]float64
	Intl        float64 // share of names from the international pool
	Weight      float64
	LaunchDays  int // relative to the first launch ("now"); negative = in the past. Large negative = since forever
	Lat, Lon    float64
	PhoneCode   string
	PhoneMobile []string
	PhoneDigits int
	FreeOver    float64
	ShipFee     float64
	Express     float64
	Locker      float64
	LockerShare float64
	Carriers    map[string]float64
	Payments    map[string]float64
	Email       map[string]float64
	Cities      []CitySpec
	Holidays    []string // "MM-DD" fixed public holidays, or special names: easter, easter_monday, orthodox_easter, ascension, whit_monday, good_friday, corpus_christi
}

type CitySpec struct {
	Name     string
	Pop      float64 // thousands
	Zip      []string
	Variants []string // alternative spellings used by "dirty" data
}

type WarehouseSpec struct {
	Code       string
	Name       string
	Country    string
	City       string
	TZ         string
	Lat, Lon   float64
	OpenedDays int // relative to first launch, negative = past
}

type CarrierSpec struct {
	Speed  float64 // transit multiplier
	Lost   float64 // probability a parcel is lost
	Late   float64 // probability of an extra delay
	Locker bool    // offers lockers / pickup points
	Prefix string  // tracking number prefix
}

type PeopleSpec struct {
	Langs      map[string]NamePool
	IntlGroups map[string]NamePool
}

type NamePool struct {
	Male      []string
	Female    []string
	Last      []string
	Streets   []string
	StreetFmt []string
	Line2Fmt  []string
	Line2Prob float64
}

type TextSpec struct {
	Descriptions  map[string][]string // style -> feature sentences
	ReviewTitles  map[int][]string
	ReviewBodies  map[int][]string
	ReviewStyle   map[string]map[string][]string // style -> pos/neg -> phrases
	ReviewLocal   map[string]map[string][]string // lang -> pos/neu/neg -> phrases
	ReviewLate    []string
	SellerWords   []string
	SellerWords2  []string
	SellerCNWords []string
	LegalForms    map[string][]string
	CampaignNames map[string]string
	Influencers   []string
}
