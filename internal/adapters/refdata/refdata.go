// Package refdata is a driven adapter providing reference data embedded into
// the binary as YAML files.
package refdata

import (
	"embed"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mymdz/lumo-market/internal/domain"
)

//go:embed data/*.yaml
var files embed.FS

// Embedded implements ports.ReferenceData.
type Embedded struct{}

func New() *Embedded { return &Embedded{} }

func (Embedded) Load() (*domain.ReferenceData, error) {
	rd := &domain.ReferenceData{}
	if err := loadCatalog(rd); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if err := loadGeo(rd); err != nil {
		return nil, fmt.Errorf("geo: %w", err)
	}
	if err := loadPeople(rd); err != nil {
		return nil, fmt.Errorf("people: %w", err)
	}
	if err := loadTexts(rd); err != nil {
		return nil, fmt.Errorf("texts: %w", err)
	}
	return rd, nil
}

func read(name string, v any) error {
	b, err := files.ReadFile("data/" + name)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// ---------- catalog ----------

type rawAxis struct {
	Values  []string  `yaml:"values"`
	Weights []float64 `yaml:"weights"`
	Price   []float64 `yaml:"price"`
	Pick    []int     `yaml:"pick"`
}

type rawLife struct {
	Ramp    float64 `yaml:"ramp"`
	Plateau float64 `yaml:"plateau"`
	Decay   float64 `yaml:"decay"`
	Floor   float64 `yaml:"floor"`
}

type rawCatalog struct {
	Styles     map[string][]string  `yaml:"styles"`
	Pools      map[string][]string  `yaml:"pools"`
	Seasons    map[string][]float64 `yaml:"seasons"`
	Lifecycles map[string]rawLife   `yaml:"lifecycles"`
	Axes       map[string]rawAxis   `yaml:"axes"`
}

type rawNode struct {
	Name     string              `yaml:"name"`
	Children []*rawNode          `yaml:"children"`
	Noun     *string             `yaml:"noun"`
	Price    []float64           `yaml:"price"`
	Axes     []string            `yaml:"axes"`
	Tpl      []string            `yaml:"tpl"`
	Pools    map[string][]string `yaml:"pools"`
	W        *float64            `yaml:"w"`
	Season   *string             `yaml:"season"`
	Tags     []string            `yaml:"tags"`
	Ret      *float64            `yaml:"ret"`
	Own      *float64            `yaml:"own"`
	Repeat   *int                `yaml:"repeat"`
	Qty      *int                `yaml:"qty"`
	Rel      []string            `yaml:"rel"`
	Style    *string             `yaml:"style"`
	Brands   *string             `yaml:"brands"`
	VAT      *string             `yaml:"vat"`
	Life     *string             `yaml:"life"`
	Dens     *float64            `yaml:"dens"`
	Kg       *float64            `yaml:"kg"`
}

func loadCatalog(rd *domain.ReferenceData) error {
	var rc rawCatalog
	if err := read("catalog.yaml", &rc); err != nil {
		return err
	}
	var rb struct {
		Brands map[string][]string `yaml:"brands"`
	}
	if err := read("brands.yaml", &rb); err != nil {
		return err
	}
	var rt struct {
		Departments []*rawNode `yaml:"departments"`
	}
	if err := read("departments.yaml", &rt); err != nil {
		return err
	}

	c := &rd.Catalog
	c.Styles = rc.Styles
	c.Pools = rc.Pools
	c.Seasons = map[string][12]float64{}
	for k, v := range rc.Seasons {
		if len(v) != 12 {
			return fmt.Errorf("season %s must have 12 values", k)
		}
		var a [12]float64
		copy(a[:], v)
		c.Seasons[k] = a
	}
	c.Lifecycles = map[string]domain.LifecycleSpec{}
	for k, v := range rc.Lifecycles {
		c.Lifecycles[k] = domain.LifecycleSpec{RampDays: v.Ramp, PlateauDays: v.Plateau, DecayDays: v.Decay, Floor: v.Floor}
	}
	c.Axes = map[string]domain.AxisSpec{}
	for k, v := range rc.Axes {
		a := domain.AxisSpec{Name: k, Values: v.Values, Weights: v.Weights, Price: v.Price}
		if len(v.Pick) == 2 {
			a.PickMin, a.PickMax = v.Pick[0], v.Pick[1]
		}
		if len(a.Weights) != 0 && len(a.Weights) != len(a.Values) {
			return fmt.Errorf("axis %s: weights/values mismatch", k)
		}
		if len(a.Price) != 0 && len(a.Price) != len(a.Values) {
			return fmt.Errorf("axis %s: price/values mismatch", k)
		}
		c.Axes[k] = a
	}
	c.Brands = map[string][]domain.BrandSpec{}
	for pool, list := range rb.Brands {
		for _, s := range list {
			parts := strings.Split(s, "|")
			if len(parts) != 3 {
				return fmt.Errorf("brand %q: expected Name|tier|country", s)
			}
			b := domain.BrandSpec{Name: parts[0], Country: parts[2]}
			if parts[1] == "own" {
				b.Own = true
				b.Tier = domain.TierBudget
			} else {
				b.Tier = domain.ParseTier(parts[1])
			}
			c.Brands[pool] = append(c.Brands[pool], b)
		}
	}

	root := domain.LeafProps{Weight: 1, Season: "flat", Returns: 0.05, Own: 0.5, MaxQty: 2, Style: "generic", Brands: "tech", VAT: "std", Life: "consumer", Density: 1, WeightKg: 1}
	for _, d := range rt.Departments {
		spec, err := resolve(d, root, nil, true, c)
		if err != nil {
			return err
		}
		c.Departments = append(c.Departments, spec)
	}
	return nil
}

func resolve(n *rawNode, parent domain.LeafProps, parentTags []string, isDept bool, c *domain.CatalogSpec) (*domain.CategorySpec, error) {
	p := parent
	p.Weight = 1
	p.Related = nil
	p.Noun = ""
	if n.Noun != nil {
		p.Noun = *n.Noun
	}
	if len(n.Price) == 2 {
		p.PriceMin, p.PriceMax = n.Price[0], n.Price[1]
	}
	if n.Axes != nil {
		p.Axes = n.Axes
	}
	if n.Tpl != nil {
		p.Tpl = n.Tpl
	}
	if n.Pools != nil {
		merged := map[string][]string{}
		for k, v := range parent.Pools {
			merged[k] = v
		}
		for k, v := range n.Pools {
			merged[k] = v
		}
		p.Pools = merged
	}
	if n.W != nil {
		p.Weight = *n.W
	}
	if n.Season != nil {
		p.Season = *n.Season
	}
	tags := append([]string{}, parentTags...)
	for _, t := range n.Tags {
		if !contains(tags, t) {
			tags = append(tags, t)
		}
	}
	p.Tags = tags
	if n.Ret != nil {
		p.Returns = *n.Ret
	}
	if n.Own != nil {
		p.Own = *n.Own
	}
	if n.Repeat != nil {
		p.Repeat = *n.Repeat
	}
	if n.Qty != nil {
		p.MaxQty = *n.Qty
	}
	p.Related = n.Rel
	if n.Style != nil {
		p.Style = *n.Style
	}
	if n.Brands != nil {
		p.Brands = *n.Brands
	}
	if n.VAT != nil {
		p.VAT = *n.VAT
	}
	if n.Life != nil {
		p.Life = *n.Life
	}
	if n.Dens != nil {
		p.Density = *n.Dens
	}
	if n.Kg != nil {
		p.WeightKg = *n.Kg
	}
	spec := &domain.CategorySpec{Name: n.Name}
	if len(n.Children) == 0 {
		if p.Noun == "" {
			p.Noun = n.Name
		}
		if p.PriceMax <= 0 {
			return nil, fmt.Errorf("leaf %q has no price range", n.Name)
		}
		if _, ok := c.Seasons[p.Season]; !ok {
			return nil, fmt.Errorf("leaf %q: unknown season %q", n.Name, p.Season)
		}
		if _, ok := c.Lifecycles[p.Life]; !ok {
			return nil, fmt.Errorf("leaf %q: unknown lifecycle %q", n.Name, p.Life)
		}
		if _, ok := c.Brands[p.Brands]; !ok {
			return nil, fmt.Errorf("leaf %q: unknown brand pool %q", n.Name, p.Brands)
		}
		if len(p.Tpl) == 0 {
			if _, ok := c.Styles[p.Style]; !ok {
				return nil, fmt.Errorf("leaf %q: unknown style %q", n.Name, p.Style)
			}
		}
		for _, a := range p.Axes {
			if _, ok := c.Axes[a]; !ok {
				return nil, fmt.Errorf("leaf %q: unknown axis %q", n.Name, a)
			}
		}
	}
	spec.Props = p
	for _, ch := range n.Children {
		cs, err := resolve(ch, p, tags, false, c)
		if err != nil {
			return nil, err
		}
		spec.Children = append(spec.Children, cs)
	}
	if isDept && n.W != nil {
		spec.Props.Weight = *n.W
	}
	return spec, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---------- geo ----------

type rawCountry struct {
	Code       string             `yaml:"code"`
	Name       string             `yaml:"name"`
	Currency   string             `yaml:"currency"`
	VAT        float64            `yaml:"vat"`
	VATReduced float64            `yaml:"vat_reduced"`
	TZ         string             `yaml:"tz"`
	EU         bool               `yaml:"eu"`
	Langs      map[string]float64 `yaml:"langs"`
	Intl       float64            `yaml:"intl"`
	Weight     float64            `yaml:"weight"`
	Launch     int                `yaml:"launch"`
	Lat        float64            `yaml:"lat"`
	Lon        float64            `yaml:"lon"`
	Phone      struct {
		Code   string   `yaml:"code"`
		Digits int      `yaml:"digits"`
		Mobile []string `yaml:"mobile"`
	} `yaml:"phone"`
	Ship struct {
		FreeOver    float64 `yaml:"free_over"`
		Fee         float64 `yaml:"fee"`
		Express     float64 `yaml:"express"`
		Locker      float64 `yaml:"locker"`
		LockerShare float64 `yaml:"locker_share"`
	} `yaml:"ship"`
	Carriers map[string]float64 `yaml:"carriers"`
	Payments map[string]float64 `yaml:"payments"`
	Email    map[string]float64 `yaml:"email"`
	Holidays []string           `yaml:"holidays"`
	Cities   [][]any            `yaml:"cities"`
}

type rawGeo struct {
	Currencies []struct {
		Code     string  `yaml:"code"`
		Name     string  `yaml:"name"`
		Rounding string  `yaml:"rounding"`
		Rate     float64 `yaml:"rate"`
		Drift    float64 `yaml:"drift"`
		Vol      float64 `yaml:"vol"`
	} `yaml:"currencies"`
	Warehouses []struct {
		Code    string  `yaml:"code"`
		Name    string  `yaml:"name"`
		Country string  `yaml:"country"`
		City    string  `yaml:"city"`
		TZ      string  `yaml:"tz"`
		Lat     float64 `yaml:"lat"`
		Lon     float64 `yaml:"lon"`
		Opened  int     `yaml:"opened"`
	} `yaml:"warehouses"`
	Carriers map[string]struct {
		Speed  float64 `yaml:"speed"`
		Lost   float64 `yaml:"lost"`
		Late   float64 `yaml:"late"`
		Locker bool    `yaml:"locker"`
		Prefix string  `yaml:"prefix"`
	} `yaml:"carriers"`
	Countries []rawCountry `yaml:"countries"`
}

func loadGeo(rd *domain.ReferenceData) error {
	var rg rawGeo
	if err := read("geo.yaml", &rg); err != nil {
		return err
	}
	g := &rd.Geo
	for _, c := range rg.Currencies {
		g.Currencies = append(g.Currencies, domain.Currency{Code: c.Code, Name: c.Name, Rounding: domain.RoundingStyle(c.Rounding), Rate: c.Rate, Drift: c.Drift, Vol: c.Vol})
	}
	for _, w := range rg.Warehouses {
		g.Warehouses = append(g.Warehouses, domain.WarehouseSpec{Code: w.Code, Name: w.Name, Country: w.Country, City: w.City, TZ: w.TZ, Lat: w.Lat, Lon: w.Lon, OpenedDays: w.Opened})
	}
	g.Carriers = map[string]domain.CarrierSpec{}
	for k, v := range rg.Carriers {
		g.Carriers[k] = domain.CarrierSpec{Speed: v.Speed, Lost: v.Lost, Late: v.Late, Locker: v.Locker, Prefix: v.Prefix}
	}
	for _, rc := range rg.Countries {
		cs := domain.CountrySpec{
			Code: rc.Code, Name: rc.Name, Currency: rc.Currency, VAT: rc.VAT, VATReduced: rc.VATReduced,
			TZ: rc.TZ, EU: rc.EU, Langs: rc.Langs, Intl: rc.Intl, Weight: rc.Weight, LaunchDays: rc.Launch,
			Lat: rc.Lat, Lon: rc.Lon, PhoneCode: rc.Phone.Code, PhoneMobile: rc.Phone.Mobile, PhoneDigits: rc.Phone.Digits,
			FreeOver: rc.Ship.FreeOver, ShipFee: rc.Ship.Fee, Express: rc.Ship.Express, Locker: rc.Ship.Locker, LockerShare: rc.Ship.LockerShare,
			Carriers: rc.Carriers, Payments: rc.Payments, Email: rc.Email, Holidays: rc.Holidays,
		}
		for car := range rc.Carriers {
			if _, ok := g.Carriers[car]; !ok {
				return fmt.Errorf("country %s: unknown carrier %q", rc.Code, car)
			}
		}
		for i, row := range rc.Cities {
			city, err := parseCity(row)
			if err != nil {
				return fmt.Errorf("country %s city #%d: %w", rc.Code, i, err)
			}
			cs.Cities = append(cs.Cities, city)
		}
		g.Countries = append(g.Countries, cs)
	}
	return nil
}

func parseCity(row []any) (domain.CitySpec, error) {
	var c domain.CitySpec
	if len(row) < 3 {
		return c, fmt.Errorf("expected [name, pop, [zips], [variants]]")
	}
	c.Name = fmt.Sprint(row[0])
	switch v := row[1].(type) {
	case int:
		c.Pop = float64(v)
	case float64:
		c.Pop = v
	default:
		f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
		if err != nil {
			return c, err
		}
		c.Pop = f
	}
	c.Zip = toStrings(row[2])
	if len(row) > 3 {
		c.Variants = toStrings(row[3])
	}
	return c, nil
}

func toStrings(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return []string{fmt.Sprint(v)}
	}
	out := make([]string, 0, len(list))
	for _, x := range list {
		out = append(out, fmt.Sprint(x))
	}
	return out
}

// ---------- people ----------

type rawPool struct {
	Male      []string `yaml:"male"`
	Female    []string `yaml:"female"`
	Last      []string `yaml:"last"`
	Streets   []string `yaml:"streets"`
	StreetFmt []string `yaml:"street_fmt"`
	Line2Fmt  []string `yaml:"line2_fmt"`
	Line2Prob float64  `yaml:"line2_prob"`
}

func (r rawPool) toDomain() domain.NamePool {
	return domain.NamePool{Male: r.Male, Female: r.Female, Last: r.Last, Streets: r.Streets, StreetFmt: r.StreetFmt, Line2Fmt: r.Line2Fmt, Line2Prob: r.Line2Prob}
}

func loadPeople(rd *domain.ReferenceData) error {
	var rp struct {
		Langs map[string]rawPool `yaml:"langs"`
		Intl  map[string]rawPool `yaml:"intl_groups"`
	}
	if err := read("people.yaml", &rp); err != nil {
		return err
	}
	rd.People.Langs = map[string]domain.NamePool{}
	for k, v := range rp.Langs {
		rd.People.Langs[k] = v.toDomain()
	}
	rd.People.IntlGroups = map[string]domain.NamePool{}
	for k, v := range rp.Intl {
		rd.People.IntlGroups[k] = v.toDomain()
	}
	for _, c := range rd.Geo.Countries {
		for l := range c.Langs {
			if _, ok := rd.People.Langs[l]; !ok {
				return fmt.Errorf("country %s uses unknown language %q", c.Code, l)
			}
		}
	}
	return nil
}

// ---------- texts ----------

func loadTexts(rd *domain.ReferenceData) error {
	var rt struct {
		Descriptions  map[string][]string            `yaml:"descriptions"`
		ReviewTitles  map[int][]string               `yaml:"review_titles"`
		ReviewBodies  map[int][]string               `yaml:"review_bodies"`
		ReviewStyle   map[string]map[string][]string `yaml:"review_style"`
		ReviewLocal   map[string]map[string][]string `yaml:"review_local"`
		ReviewLate    []string                       `yaml:"review_late"`
		SellerWords   []string                       `yaml:"seller_words"`
		SellerWords2  []string                       `yaml:"seller_words2"`
		SellerCNWords []string                       `yaml:"seller_cn_words"`
		LegalForms    map[string][]string            `yaml:"legal_forms"`
		Influencers   []string                       `yaml:"influencers"`
	}
	if err := read("texts.yaml", &rt); err != nil {
		return err
	}
	rd.Texts = domain.TextSpec{
		Descriptions: rt.Descriptions, ReviewTitles: rt.ReviewTitles, ReviewBodies: rt.ReviewBodies,
		ReviewStyle: rt.ReviewStyle, ReviewLocal: rt.ReviewLocal, ReviewLate: rt.ReviewLate,
		SellerWords: rt.SellerWords, SellerWords2: rt.SellerWords2, SellerCNWords: rt.SellerCNWords,
		LegalForms: rt.LegalForms, Influencers: rt.Influencers,
	}
	return nil
}
