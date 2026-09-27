package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

const farPast = -100000

func relDays(h time.Time, days int, start time.Time) time.Time {
	if days <= farPast {
		return start.AddDate(-5, 0, 0)
	}
	return h.Add(time.Duration(days) * 24 * time.Hour)
}

// initReference builds markets, currencies, warehouses (static, derived from
// reference data and the horizon).
func (w *World) initReference() error {
	g := &w.ref.Geo
	w.currencies = map[string]*currencyState{}
	for _, c := range g.Currencies {
		w.currencies[c.Code] = &currencyState{Currency: c, rate: c.Rate, priceRate: c.Rate}
	}
	w.carriers = g.Carriers
	w.marketByCode = map[string]*market{}
	for i := range g.Countries {
		cs := &g.Countries[i]
		loc, err := time.LoadLocation(cs.TZ)
		if err != nil {
			return fmt.Errorf("timezone %s: %w", cs.TZ, err)
		}
		cur := w.currencies[cs.Currency]
		if cur == nil {
			return fmt.Errorf("country %s: unknown currency %s", cs.Code, cs.Currency)
		}
		m := &market{
			Country: &domain.Country{Idx: i, Code: cs.Code, Name: cs.Name, Currency: cs.Currency, VAT: cs.VAT, VATReduced: cs.VATReduced, TZ: loc, EU: cs.EU, Spec: cs},
			spec:    cs, cur: cur,
		}
		m.LaunchedAt = relDays(w.horizon, cs.LaunchDays, w.start)
		cw := make([]float64, len(cs.Cities))
		cities := make([]*domain.CitySpec, len(cs.Cities))
		for j := range cs.Cities {
			cities[j] = &cs.Cities[j]
			cw[j] = math.Pow(cs.Cities[j].Pop, 0.85)
		}
		m.cities = NewPicker(cities, cw)
		m.langs = PickerFromMap(cs.Langs)
		m.carriers = PickerFromMap(cs.Carriers)
		lockers := map[string]float64{}
		for k, v := range cs.Carriers {
			if g.Carriers[k].Locker {
				lockers[k] = v
			}
		}
		if len(lockers) == 0 {
			lockers = cs.Carriers
		}
		m.lockers = PickerFromMap(lockers)
		m.payments = PickerFromMap(cs.Payments)
		m.payList = m.payments.Items()
		m.emails = PickerFromMap(cs.Email)
		m.freeOver = domain.MoneyFromFloat(cs.FreeOver)
		m.shipFee = domain.MoneyFromFloat(cs.ShipFee)
		m.express = domain.MoneyFromFloat(cs.Express)
		m.locker = domain.MoneyFromFloat(cs.Locker)
		w.markets = append(w.markets, m)
		w.marketByCode[cs.Code] = m
	}
	return nil
}

func (w *World) initWarehouses() error {
	w.whByID = map[int16]*domain.Warehouse{}
	for i, ws := range w.ref.Geo.Warehouses {
		loc, err := time.LoadLocation(ws.TZ)
		if err != nil {
			return err
		}
		wh := &domain.Warehouse{ID: int16(i + 1), Code: ws.Code, Name: ws.Name, Country: ws.Country, City: ws.City, TZName: ws.TZ, TZ: loc, Lat: ws.Lat, Lon: ws.Lon}
		wh.OpenedAt = relDays(w.horizon, ws.OpenedDays, w.start)
		w.warehouses = append(w.warehouses, wh)
		w.whByID[wh.ID] = wh
	}
	return nil
}

func slug(s string) string {
	s = asciiLower(strings.ReplaceAll(strings.ReplaceAll(s, "&", "and"), " ", "-"))
	return s
}

func slugKeep(name string) string {
	var sb strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(name) {
		if t, ok := translit[r]; ok {
			sb.WriteString(t)
			prevDash = false
			continue
		}
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
			prevDash = false
		case r == '&':
			if !prevDash {
				sb.WriteString("-")
			}
			sb.WriteString("and-")
			prevDash = true
		default:
			if !prevDash {
				sb.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(sb.String(), "-")
}

// initCatalogTree creates categories, departments and leaves from the spec.
// When existing categories are given (restart), ids are taken from them by path.
func (w *World) initCatalogTree(existing []*domain.Category) {
	byPath := map[string]*domain.Category{}
	for _, c := range existing {
		byPath[c.Path] = c
	}
	w.catByID = map[int32]*domain.Category{}
	w.leafByCat = map[int32]*leaf{}
	r := NewRand(hashSeed(w.cfg.Seed, "categories"))
	var walk func(spec *domain.CategorySpec, parent *domain.Category, d *dept, level int)
	walk = func(spec *domain.CategorySpec, parent *domain.Category, d *dept, level int) {
		path := spec.Name
		pid := int32(0)
		if parent != nil {
			path = parent.Path + " > " + spec.Name
			pid = parent.ID
		}
		c, ok := byPath[path]
		if !ok {
			w.ids.Category++
			created := w.start.AddDate(0, 0, -r.IntRange(400, 1500))
			c = &domain.Category{ID: int32(w.ids.Category), ParentID: pid, Name: spec.Name, Slug: slugKeep(spec.Name), Path: path, Level: level, Active: true, CreatedAt: created, UpdatedAt: created}
			if existing == nil {
				w.uow.b.Categories = append(w.uow.b.Categories, c)
			}
		}
		if level == 0 {
			d = &dept{idx: len(w.depts), cat: c, name: spec.Name, weight: spec.Props.Weight}
			w.depts = append(w.depts, d)
		}
		c.Dept = d.idx
		w.cats = append(w.cats, c)
		w.catByID[c.ID] = c
		if spec.IsLeaf() {
			props := spec.Props
			c.Leaf = &props
			l := &leaf{cat: c, p: c.Leaf, dept: d, life: w.ref.Catalog.Lifecycles[props.Life], season: w.ref.Catalog.Seasons[props.Season]}
			w.leaves = append(w.leaves, l)
			w.leafByCat[c.ID] = l
			d.leaves = append(d.leaves, l)
		}
		for _, ch := range spec.Children {
			walk(ch, c, d, level+1)
		}
	}
	for _, dspec := range w.ref.Catalog.Departments {
		walk(dspec, nil, nil, 0)
	}
	for _, c := range existing {
		if int64(c.ID) > w.ids.Category {
			w.ids.Category = int64(c.ID)
		}
	}
	// related leaves: same department first
	for _, l := range w.leaves {
		for _, name := range l.p.Related {
			var found *leaf
			for _, x := range l.dept.leaves {
				if x.cat.Name == name {
					found = x
				}
			}
			if found == nil {
				for _, x := range w.leaves {
					if x.cat.Name == name {
						found = x
						break
					}
				}
			}
			if found != nil {
				l.related = append(l.related, found)
			}
		}
	}
}

// initBrands creates brands from the pools (or links existing ones by name).
func (w *World) initBrands(existing []*domain.Brand) {
	w.brandByName = map[string]*domain.Brand{}
	w.brandByID = map[int32]*domain.Brand{}
	w.brandPools = map[string][]*domain.Brand{}
	for _, b := range existing {
		w.brandByName[b.Name] = b
		w.brandByID[b.ID] = b
		if int64(b.ID) > w.ids.Brand {
			w.ids.Brand = int64(b.ID)
		}
	}
	poolDept := map[string]int{}
	for _, l := range w.leaves {
		if _, ok := poolDept[l.p.Brands]; !ok {
			poolDept[l.p.Brands] = l.dept.idx
		}
	}
	pools := make([]string, 0, len(w.ref.Catalog.Brands))
	for k := range w.ref.Catalog.Brands {
		pools = append(pools, k)
	}
	sort.Strings(pools)
	r := NewRand(hashSeed(w.cfg.Seed, "brands"))
	for _, b := range existing {
		q := map[domain.Tier]float64{domain.TierBudget: 0.35, domain.TierMid: 0.6, domain.TierPremium: 0.8}[b.Tier]
		br := NewRand(hashSeed(w.cfg.Seed, "brand-quality", b.Name))
		b.Quality = br.Clamp(br.Norm(q, 0.1), 0.1, 0.95)
	}
	for _, pool := range pools {
		for _, bs := range w.ref.Catalog.Brands[pool] {
			b, ok := w.brandByName[bs.Name]
			if !ok {
				w.ids.Brand++
				q := map[domain.Tier]float64{domain.TierBudget: 0.35, domain.TierMid: 0.6, domain.TierPremium: 0.8}[bs.Tier]
				br := NewRand(hashSeed(w.cfg.Seed, "brand-quality", bs.Name))
				b = &domain.Brand{ID: int32(w.ids.Brand), Name: bs.Name, Tier: bs.Tier, Country: bs.Country, PrivateLabel: bs.Own,
					CreatedAt: w.start.AddDate(0, 0, -r.IntRange(200, 1600)), Dept: poolDept[pool], Quality: br.Clamp(br.Norm(q, 0.1), 0.1, 0.95)}
				w.brandByName[b.Name] = b
				w.brandByID[b.ID] = b
				if existing == nil {
					w.uow.b.Brands = append(w.uow.b.Brands, b)
				}
			}
			w.brandPools[pool] = append(w.brandPools[pool], b)
		}
	}
	w.brands = w.brands[:0]
	for _, b := range w.brandByName {
		w.brands = append(w.brands, b)
	}
	sort.Slice(w.brands, func(i, j int) bool { return w.brands[i].ID < w.brands[j].ID })
	for _, l := range w.leaves {
		l.brands = w.brandPools[l.p.Brands]
	}
}

var sellerCountries = NewPicker(
	[]string{"DE", "NL", "FR", "IT", "ES", "PL", "CZ", "BE", "AT", "CN", "PT", "SE", "DK", "IE", "MD"},
	[]float64{25, 10, 10, 8, 8, 12, 3, 3, 3, 12, 2, 2, 1, 1, 0.5},
)

func (w *World) newSeller(at time.Time, status string) *domain.Seller {
	r := w.rng
	t := &w.ref.Texts
	country := sellerCountries.Pick(r)
	var name, legal string
	if country == "CN" {
		a, b := Pick(r, t.SellerCNWords), Pick(r, t.SellerCNWords)
		name = a + strings.ToLower(b) + " " + Pick(r, []string{"Direct", "Store", "Official", "Tech", "Home", "EU"})
		legal = Pick(r, []string{"Shenzhen", "Guangzhou", "Yiwu", "Ningbo", "Dongguan", "Hangzhou"}) + " " + a + strings.ToLower(b) + " " + Pick(r, t.LegalForms["CN"])
	} else {
		name = Pick(r, t.SellerWords) + Pick(r, t.SellerWords2)
		if r.Bool(0.2) {
			name = Pick(r, t.SellerWords) + " " + Pick(r, t.SellerWords2)
		}
		forms := t.LegalForms[country]
		if len(forms) == 0 {
			forms = []string{"Ltd"}
		}
		legal = name + " " + Pick(r, forms)
	}
	dw := make([]float64, len(w.depts))
	for i, d := range w.depts {
		dw[i] = d.weight
		if country == "CN" && (strings.HasPrefix(d.name, "Electronics") || strings.HasPrefix(d.name, "Toys") || strings.HasPrefix(d.name, "Home") || strings.HasPrefix(d.name, "Garden") || strings.HasPrefix(d.name, "Sports")) {
			dw[i] *= 3
		}
		if strings.HasPrefix(d.name, "Grocery") || strings.HasPrefix(d.name, "Books") {
			dw[i] *= 0.4
		}
	}
	w.ids.Seller++
	s := &domain.Seller{ID: int32(w.ids.Seller), Name: name, LegalName: legal, Country: country, Type: "3p", Status: status, JoinedAt: at, UpdatedAt: at}
	tr := &s.Traits
	tr.Dept = r.WeightedIndex(dw)
	tr.HandlingDays = r.LogNorm(1.3, 0.5)
	tr.Quality = r.Range(0.3, 0.95)
	if country == "CN" {
		tr.HandlingDays = r.LogNorm(2.5, 0.4)
		tr.Quality = r.Range(0.2, 0.8)
	}
	tr.CancelRate = r.Clamp(math.Exp(r.Norm(math.Log(0.015), 0.8)), 0.002, 0.2)
	if tr.Quality < 0.4 {
		tr.CancelRate *= 2
	}
	tr.LateRate = r.Clamp(0.25-0.25*tr.Quality+r.Norm(0, 0.03), 0.01, 0.4)
	tr.Aggressiveness = r.Float64()
	tr.PlatformShare = r.Range(0, 0.8)
	if country == "CN" {
		tr.PlatformShare = r.Range(0, 0.3)
	}
	s.Rating = math.Round(r.Clamp(2.4+2.6*tr.Quality+r.Norm(0, 0.15), 1, 5)*100) / 100
	w.sellers = append(w.sellers, s)
	w.sellerByID[s.ID] = s
	w.uow.addSeller(s)
	return s
}

func (w *World) initSellers() {
	r := w.rng
	w.sellerByID = map[int32]*domain.Seller{}
	w.ids.Seller++
	own := &domain.Seller{ID: int32(w.ids.Seller), Name: "Lumo Retail", LegalName: "Lumo Market B.V.", Country: "NL", Type: "1p", Status: "active", Rating: 4.6, JoinedAt: w.start.AddDate(-3, 0, 0), UpdatedAt: w.start.AddDate(-3, 0, 0)}
	own.Traits = domain.SellerTraits{HandlingDays: 0.5, CancelRate: 0.003, LateRate: 0.03, Quality: 0.85, PlatformShare: 1, Dept: -1}
	w.sellers = append(w.sellers, own)
	w.sellerByID[own.ID] = own
	w.ownSeller = own
	w.uow.addSeller(own)
	for i := 0; i < w.cfg.InitialSellers; i++ {
		at := w.start.Add(-time.Duration(r.Range(30, 4*365)*24) * time.Hour)
		w.newSeller(at, "active")
	}
	w.rebuildSellerIndex()
}

func (w *World) initProducts() {
	r := w.rng
	total := 0.0
	type lw struct {
		l *leaf
		w float64
	}
	var ws []lw
	for _, d := range w.depts {
		lt := 0.0
		for _, l := range d.leaves {
			lt += l.p.Weight
		}
		for _, l := range d.leaves {
			v := d.weight * l.p.Density * l.p.Weight / lt
			ws = append(ws, lw{l, v})
			total += v
		}
	}
	for _, x := range ws {
		n := int(math.Round(float64(w.cfg.InitialProducts) * x.w / total))
		if n < 3 {
			n = 3
		}
		for i := 0; i < n; i++ {
			age := math.Min(2000, r.LogNorm(200, 0.9))
			if x.l.p.Life == "evergreen" {
				age = r.Range(30, 1500)
			}
			launched := w.start.Add(-time.Duration(age*24) * time.Hour)
			w.newProduct(x.l, launched, nil, nil, nil)
		}
	}
	for _, l := range w.leaves {
		for _, p := range l.products {
			for _, v := range p.Variants {
				for _, o := range v.Offers {
					w.initStock(o, l, r)
				}
			}
		}
	}
}

func (w *World) initCoupons() {
	add := func(id int32, code, typ string, val, min float64, kind string, from time.Time) {
		c := &domain.Coupon{ID: id, Code: code, DiscountType: typ, DiscountValue: val, MinOrder: domain.MoneyFromFloat(min), ValidFrom: from, Kind: kind, CreatedAt: from, UpdatedAt: from}
		w.coupons = append(w.coupons, c)
		w.couponByCode[code] = c
		w.uow.addCoupon(c)
	}
	base := w.start.AddDate(-2, 0, 0)
	add(1, "WELCOME10", "percent", 10, 20, "welcome", base)
	add(2, "NEWS5", "fixed", 5, 40, "newsletter", base)
	add(3, "GOLD10", "percent", 10, 0, "loyalty", base)
	add(4, "COMEBACK15", "percent", 15, 30, "winback", base)
	add(5, "FREESHIP", "free_shipping", 0, 0, "newsletter", base)
}

func (w *World) initTestCustomers() {
	if w.cfg.Dirt <= 0 {
		return
	}
	names := [][3]string{{"Test", "Test", "test.test@lumomarket.test"}, {"QA", "Automation", "qa+1@lumomarket.test"}, {"QA", "Automation", "qa+2@lumomarket.test"}, {"Max", "Mustermann", "max.mustermann@example.com"}, {"Jan", "Jansen", "test-nl@lumomarket.test"}, {"Load", "Test", "loadtest@lumomarket.test"}}
	for i, n := range names {
		m := w.markets[0]
		if i == 4 {
			m = w.marketByCode["NL"]
		}
		pr := w.newProspect(m, w.start)
		pr.C.Flags |= domain.FlagTest
		pr.C.ChurnAt = w.start.AddDate(20, 0, 0).Unix()
		pr.Profile.FirstName, pr.Profile.LastName, pr.Profile.Email = n[0], n[1], n[2]
		pr.Addr.Recipient = n[0] + " " + n[1]
		c := w.register(pr, w.start.AddDate(-1, 0, -i*20))
		c.Flags |= domain.FlagTest
		w.updateWeight(c)
		w.testCusts = append(w.testCusts, c.ID)
	}
}

// initCustomers seeds the pre-existing customer base (migrated from before
// the history window) so that day one already has returning customers.
func (w *World) initCustomers() {
	r := w.rng
	targetW := 0.6 * w.cfg.OrdersStart / 0.3
	totalW := 0.0
	count := 0
	for totalW < targetW && count < 3_000_000 {
		var m *market
		for {
			m = w.markets[r.WeightedIndex(marketWeights(w))]
			if !m.LaunchedAt.After(w.start) {
				break
			}
		}
		pr := w.newProspect(m, w.start)
		signup := w.start.Add(-time.Duration(r.Exp(400)*24+1) * time.Hour)
		if signup.Before(w.start.AddDate(-4, 0, 0)) {
			signup = w.start.AddDate(-4, 0, 0).Add(time.Duration(r.Range(0, 300*24)) * time.Hour)
		}
		c := w.register(pr, signup)
		c.Orders = uint16(r.IntRange(1, 12))
		c.LastOrderAt = w.start.Add(-time.Duration(r.Exp(60)*24) * time.Hour).Unix()
		c.Spent = float32(r.LogNorm(150, 0.9))
		c.SpentAt = c.LastOrderAt
		sd := segDefs[c.Segment]
		c.ChurnAt = w.start.Add(time.Duration(r.Exp(sd.life)*24) * time.Hour).Unix()
		if r.Bool(0.3) {
			c.Active = false
		}
		w.updateWeight(c)
		totalW += w.custWeight(c)
		count++
	}
	w.log.Info("seeded customer base", "customers", count)
}

// bootstrap creates a brand new world at the start of history.
func (w *World) bootstrap() error {
	w.now = w.start
	w.uow.b.Currencies = w.ref.Geo.Currencies
	w.uow.b.Countries = nil
	for _, m := range w.markets {
		w.uow.b.Countries = append(w.uow.b.Countries, m.Country)
	}
	w.uow.b.Warehouses = w.warehouses
	w.initCatalogTree(nil)
	w.initBrands(nil)
	w.initSellers()
	w.cal = newCalendar(w)
	w.applyDueMigrations()
	w.log.Info("generating catalog", "products", w.cfg.InitialProducts)
	w.initProducts()
	w.log.Info("catalog ready", "products", len(w.products), "variants", len(w.variants), "offers", len(w.offers))
	w.initCoupons()
	w.initTestCustomers()
	w.initCustomers()
	w.updateFX()
	w.lastDaily = dayOf(w.now)
	w.nightlyDone = dayOf(w.now)
	w.lastHourly = w.now.Truncate(time.Hour)
	w.ensureYears()
	w.refreshDemand()
	return nil
}
