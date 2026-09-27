package app

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

var commissionByDept = map[string]float64{
	"Electronics": 0.08, "Home Appliances": 0.09, "Women's Fashion": 0.15, "Men's Fashion": 0.15,
	"Kids & Baby": 0.12, "Toys & Games": 0.12, "Home & Living": 0.14, "DIY & Tools": 0.12,
	"Garden & Outdoor": 0.12, "Sports & Outdoors": 0.13, "Beauty & Personal Care": 0.15,
	"Health & Household": 0.10, "Grocery & Drinks": 0.10, "Pet Supplies": 0.11,
	"Books & Stationery": 0.15, "Automotive": 0.11, "Jewellery & Watches": 0.18,
}

var marginByDept = map[string]float64{
	"Electronics": 0.12, "Home Appliances": 0.2, "Women's Fashion": 0.55, "Men's Fashion": 0.55,
	"Kids & Baby": 0.35, "Toys & Games": 0.35, "Home & Living": 0.45, "DIY & Tools": 0.3,
	"Garden & Outdoor": 0.35, "Sports & Outdoors": 0.4, "Beauty & Personal Care": 0.45,
	"Health & Household": 0.25, "Grocery & Drinks": 0.25, "Pet Supplies": 0.3,
	"Books & Stationery": 0.3, "Automotive": 0.3, "Jewellery & Watches": 0.6,
}

// departments where several sellers typically offer the very same product
var competitiveDept = map[string]bool{
	"Electronics": true, "Home Appliances": true, "Toys & Games": true, "Beauty & Personal Care": true,
	"Health & Household": true, "Grocery & Drinks": true, "Pet Supplies": true, "Books & Stationery": true,
	"DIY & Tools": true, "Automotive": true, "Kids & Baby": true, "Sports & Outdoors": true,
}

var gs1Prefix = map[string][]int{
	"DE": {400, 440}, "FR": {300, 379}, "NL": {870, 879}, "BE": {540, 549}, "AT": {900, 919}, "IT": {800, 839},
	"ES": {840, 849}, "PT": {560, 560}, "IE": {539, 539}, "PL": {590, 590}, "CZ": {859, 859}, "SE": {730, 739},
	"DK": {570, 579}, "FI": {640, 649}, "MD": {484, 484}, "CN": {690, 699}, "JP": {450, 459}, "KR": {880, 880},
	"TW": {471, 471}, "US": {1, 13}, "GB": {500, 509}, "NO": {700, 709}, "CH": {760, 769},
}

func ean13(r *Rand, country string) string {
	pr, ok := gs1Prefix[country]
	if !ok {
		pr = []int{400, 440}
	}
	p := r.IntRange(pr[0], pr[1])
	digits := fmt.Sprintf("%03d%09d", p, r.Int64N(1_000_000_000))
	sum := 0
	for i, ch := range digits {
		d := int(ch - '0')
		if i%2 == 1 {
			d *= 3
		}
		sum += d
	}
	check := (10 - sum%10) % 10
	return digits + strconv.Itoa(check)
}

func lifeFactor(ls domain.LifecycleSpec, scale, age float64) float64 {
	if age < 0 {
		return 0
	}
	if scale <= 0 {
		scale = 1
	}
	ramp, plateau, decay := ls.RampDays*scale, ls.PlateauDays*scale, ls.DecayDays*scale
	switch {
	case age < ramp:
		return 0.15 + 0.85*age/ramp
	case age < ramp+plateau:
		return 1
	default:
		v := math.Exp(-(age - ramp - plateau) / decay)
		return math.Max(ls.Floor, v)
	}
}

// ---------- title rendering ----------

type renderCtx struct {
	gen  int
	line string
}

func (w *World) render(tpl string, l *leaf, brand string, r *Rand, ctx *renderCtx, depth int) string {
	var sb strings.Builder
	for i := 0; i < len(tpl); i++ {
		ch := tpl[i]
		if ch != '{' {
			sb.WriteByte(ch)
			continue
		}
		j := strings.IndexByte(tpl[i:], '}')
		if j < 0 {
			sb.WriteString(tpl[i:])
			break
		}
		name := tpl[i+1 : i+j]
		i += j
		sb.WriteString(w.token(name, l, brand, r, ctx, depth))
	}
	return sb.String()
}

func (w *World) token(name string, l *leaf, brand string, r *Rand, ctx *renderCtx, depth int) string {
	switch name {
	case "brand":
		return brand
	case "noun":
		return l.p.Noun
	case "gen":
		if ctx.gen == 0 {
			ctx.gen = r.IntRange(2, 15)
		}
		return strconv.Itoa(ctx.gen)
	case "model":
		letters := "ABCDEFGHJKLMNPRSTVXZ"
		switch r.IntN(4) {
		case 0:
			return fmt.Sprintf("%c%c%d", letters[r.IntN(len(letters))], letters[r.IntN(len(letters))], r.IntRange(10, 990))
		case 1:
			return fmt.Sprintf("%c%d", letters[r.IntN(len(letters))], r.IntRange(2, 99))
		case 2:
			return fmt.Sprintf("%c%c-%d", letters[r.IntN(len(letters))], letters[r.IntN(len(letters))], r.IntRange(100, 9900))
		default:
			return fmt.Sprintf("%c%d00", letters[r.IntN(len(letters))], r.IntRange(1, 9))
		}
	case "num":
		return strconv.Itoa(r.IntRange(2, 12))
	case "author":
		return w.randomPersonName(r)
	}
	var pool []string
	if l.p.Pools != nil {
		pool = l.p.Pools[name]
	}
	if pool == nil {
		pool = w.ref.Catalog.Pools[name]
	}
	if len(pool) == 0 {
		return ""
	}
	v := Pick(r, pool)
	if name == "tech_line" || name == "home_coll" || name == "sport_line" {
		if ctx.line != "" {
			v = ctx.line
		} else {
			ctx.line = v
		}
	}
	if depth < 4 && strings.ContainsRune(v, '{') {
		v = w.render(v, l, brand, r, ctx, depth+1)
	}
	return v
}

func cleanTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, " ,", ",")
	s = strings.ReplaceAll(s, ",,", ",")
	s = strings.TrimRight(s, ", -")
	return s
}

func (w *World) randomPersonName(r *Rand) string {
	langs := []string{"de", "fr", "nl", "it", "es", "pl", "sv", "en", "pt", "cs", "da", "fi", "ro"}
	pool := w.ref.People.Langs[Pick(r, langs)]
	first := pool.Male
	if r.Bool(0.5) {
		first = pool.Female
	}
	return Pick(r, first) + " " + Pick(r, pool.Last)
}

// ---------- product creation ----------

// brandFit says whether a brand makes products in a leaf: every brand covers
// a deterministic subset of the leaves its pool serves (a camera maker does
// not sell phone cases).
func (w *World) brandFit(b *domain.Brand, l *leaf) float64 {
	cover := 0.25 + 0.5*float64(hashSeed(w.cfg.Seed, "brand-cover", b.Name)%1000)/1000
	if b.PrivateLabel {
		cover = 0.7
	}
	if len(l.brands) <= 5 {
		cover = 0.9
	}
	if float64(hashSeed(w.cfg.Seed, "brand-leaf", b.Name, l.cat.Path)%1000)/1000 < cover {
		return 1
	}
	return 0.01
}

func (w *World) pickBrand(l *leaf, r *Rand) *domain.Brand {
	ws := make([]float64, len(l.brands))
	for i, b := range l.brands {
		switch {
		case b.PrivateLabel:
			ws[i] = 0.5
		case b.Tier == domain.TierBudget:
			ws[i] = 1.1
		case b.Tier == domain.TierPremium:
			ws[i] = 0.6
		default:
			ws[i] = 1.0
		}
		ws[i] *= w.brandFit(b, l)
	}
	return l.brands[r.WeightedIndex(ws)]
}

func tierCenter(t domain.Tier) float64 {
	switch t {
	case domain.TierBudget:
		return 0.18
	case domain.TierPremium:
		return 0.78
	default:
		return 0.45
	}
}

// newProduct creates a product (with variants and offers) in a leaf.
func (w *World) newProduct(l *leaf, launched time.Time, brand *domain.Brand, seller *domain.Seller, pred *domain.Product) *domain.Product {
	r := w.rng
	if brand == nil {
		brand = w.pickBrand(l, r)
	}
	ctx := &renderCtx{}
	var title string
	if pred != nil && pred.Traits.Gen > 0 {
		old := strconv.Itoa(pred.Traits.Gen)
		ctx.gen = pred.Traits.Gen + 1
		ctx.line = pred.Traits.Line
		if strings.Contains(pred.Title, " "+old) {
			title = strings.Replace(pred.Title, " "+old, " "+strconv.Itoa(ctx.gen), 1)
		} else {
			title = pred.Title + " (" + ordinal(ctx.gen) + " Gen)"
		}
	} else {
		tpls := l.p.Tpl
		if len(tpls) == 0 {
			tpls = w.ref.Catalog.Styles[l.p.Style]
		}
		title = cleanTitle(w.render(Pick(r, tpls), l, brand.Name, r, ctx, 0))
		if l.p.Style == "book" || strings.HasPrefix(title, "{") {
			title = cleanTitle(title)
		}
	}
	w.ids.Product++
	p := &domain.Product{
		ID: w.ids.Product, CategoryID: l.cat.ID, BrandID: brand.ID, Title: title, Status: "active",
		LaunchedAt: launched, CreatedAt: minTime(launched, w.now), UpdatedAt: minTime(launched, w.now),
		WeightG: int(math.Max(10, l.p.WeightKg*1000*r.LogNorm(1, 0.35))),
		Cat:     l.cat, Brand: brand, Attributes: map[string]string{},
	}
	p.Traits.Quality = r.Clamp(brand.Quality+r.Norm(0, 0.12), 0.05, 0.98)
	// base price: position inside the leaf range depends on the brand tier
	lo, hi := math.Log(l.p.PriceMin), math.Log(l.p.PriceMax)
	pos := r.Clamp(tierCenter(brand.Tier)+r.Norm(0, 0.16), 0, 1)
	basePrice := math.Exp(lo + (hi-lo)*pos)
	if pred != nil {
		basePrice = w.productBasePrice(pred) * r.Range(1.0, 1.15)
	}
	// popularity: heavy-tailed, cheaper items sell more units
	mid := math.Sqrt(l.p.PriceMin * l.p.PriceMax)
	p.Traits.BasePop = math.Min(400, r.Pareto(1, 1.2)*math.Sqrt(p.Traits.Quality+0.2)*math.Pow(basePrice/mid, -0.9))
	p.Traits.Life = l.p.Life
	p.Traits.LifeScale = r.LogNorm(1, 0.35)
	p.Traits.Gen = ctx.gen
	p.Traits.Line = ctx.line
	if pred != nil {
		p.Traits.BasePop = math.Max(p.Traits.BasePop, pred.Traits.BasePop*r.Range(0.8, 1.3))
		p.Traits.Quality = r.Clamp(pred.Traits.Quality+r.Norm(0.02, 0.05), 0.05, 0.98)
	}
	if l.p.Style == "book" {
		p.Attributes["author"] = w.randomPersonName(r)
		p.Attributes["pages"] = strconv.Itoa(r.IntRange(96, 720))
		p.Attributes["language"] = Pick(r, []string{"English", "English", "English", "German", "French", "Spanish", "Italian", "Dutch", "Polish"})
	}
	p.Attributes["tier"] = brand.Tier.String()
	if w.applied["m003_products_eco_score"] {
		p.EcoScore = Pick(r, []string{"A", "B", "B", "C", "C", "C", "D", "E"})
	}
	p.Description = w.describe(p, l, r)
	if w.dirty(0.01) {
		p.BrandID = 0 // unbranded / missing brand in the PIM
	}
	if w.dirty(0.02) {
		p.Title = strings.Replace(p.Title, " ", "  ", 1) + " "
	}

	w.makeVariants(p, l, r)
	w.makeOffers(p, l, basePrice, seller, r)
	w.products[p.ID] = p
	l.products = append(l.products, p)
	w.uow.addProduct(p)
	for _, v := range p.Variants {
		w.variants[v.ID] = v
		for _, o := range v.Offers {
			w.offers[o.ID] = o
			w.uow.addOffer(o)
		}
	}
	return p
}

func ordinal(n int) string {
	switch n % 10 {
	case 1:
		if n%100 != 11 {
			return fmt.Sprintf("%dst", n)
		}
	case 2:
		if n%100 != 12 {
			return fmt.Sprintf("%dnd", n)
		}
	case 3:
		if n%100 != 13 {
			return fmt.Sprintf("%drd", n)
		}
	}
	return fmt.Sprintf("%dth", n)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (w *World) productBasePrice(p *domain.Product) float64 {
	for _, v := range p.Variants {
		for _, o := range v.Offers {
			if v.PriceFactor > 0 {
				return o.BasePrice.Float() / v.PriceFactor
			}
		}
	}
	return 20
}

func (w *World) describe(p *domain.Product, l *leaf, r *Rand) string {
	pool := w.ref.Texts.Descriptions[l.p.Style]
	if len(pool) == 0 {
		pool = w.ref.Texts.Descriptions["generic"]
	}
	a, b := Pick(r, pool), Pick(r, pool)
	desc := p.Title + ". " + a
	if b != a {
		desc += " " + b
	}
	if author, ok := p.Attributes["author"]; ok {
		desc = p.Title + " by " + author + ". " + a
	}
	switch {
	case w.dirty(0.08):
		return ""
	case w.dirty(0.05):
		return "<p>" + strings.ReplaceAll(desc, "&", "&amp;") + "</p>"
	}
	return desc
}

func (w *World) makeVariants(p *domain.Product, l *leaf, r *Rand) {
	type val struct {
		axis   string
		v      string
		weight float64
		price  float64
	}
	var dims [][]val
	for _, an := range l.p.Axes {
		ax := w.ref.Catalog.Axes[an]
		idx := make([]int, len(ax.Values))
		for i := range idx {
			idx[i] = i
		}
		if ax.PickMax > 0 {
			n := r.IntRange(ax.PickMin, ax.PickMax)
			if n > len(idx) {
				n = len(idx)
			}
			if len(ax.Price) > 0 {
				start := r.IntN(len(idx) - n + 1)
				idx = idx[start : start+n]
			} else {
				ws := make([]float64, len(ax.Values))
				for i := range ws {
					ws[i] = 1
					if len(ax.Weights) > 0 {
						ws[i] = ax.Weights[i]
					}
				}
				chosen := map[int]bool{}
				for len(chosen) < n {
					chosen[r.WeightedIndex(ws)] = true
				}
				idx = idx[:0]
				for i := range ax.Values {
					if chosen[i] {
						idx = append(idx, i)
					}
				}
			}
		}
		var d []val
		for _, i := range idx {
			v := val{axis: an, v: ax.Values[i], weight: 1, price: 1}
			if len(ax.Weights) > 0 {
				v.weight = ax.Weights[i]
			}
			if len(ax.Price) > 0 {
				v.price = ax.Price[i]
			}
			d = append(d, v)
		}
		dims = append(dims, d)
	}
	combos := [][]val{{}}
	for _, d := range dims {
		var next [][]val
		for _, c := range combos {
			for _, v := range d {
				nc := append(append([]val{}, c...), v)
				next = append(next, nc)
			}
		}
		if len(next) > 36 {
			break
		}
		combos = next
	}
	country := "DE"
	if p.Brand != nil {
		country = p.Brand.Country
	}
	for i, c := range combos {
		w.ids.Variant++
		v := &domain.Variant{
			ID: w.ids.Variant, ProductID: p.ID, Status: "active", CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt,
			PriceFactor: 1, Demand: 1, Product: p, Attrs: map[string]string{},
		}
		var names []string
		for _, x := range c {
			v.Attrs[x.axis] = x.v
			v.Demand *= x.weight
			v.PriceFactor *= x.price
			names = append(names, x.v)
		}
		v.Name = strings.Join(names, " / ")
		v.SKU = fmt.Sprintf("LM-%s-%02d", strings.ToUpper(strconv.FormatInt(p.ID+1_000_000, 36)), i+1)
		v.EAN = ean13(r, country)
		p.Variants = append(p.Variants, v)
	}
}

func (w *World) sellerForLeaf(l *leaf, r *Rand, exclude map[int32]bool) *domain.Seller {
	cands := w.sellersByDept[l.dept.idx]
	if len(cands) == 0 {
		cands = w.activeSellers
	}
	if len(cands) == 0 {
		return w.ownSeller
	}
	for tries := 0; tries < 8; tries++ {
		s := cands[r.IntN(len(cands))]
		if !exclude[s.ID] {
			return s
		}
	}
	return nil
}

func (w *World) makeOffers(p *domain.Product, l *leaf, basePrice float64, seller *domain.Seller, r *Rand) {
	primary := seller
	if primary == nil {
		if p.Brand != nil && p.Brand.PrivateLabel || r.Bool(l.p.Own) {
			primary = w.ownSeller
		} else {
			primary = w.sellerForLeaf(l, r, nil)
			if primary == nil {
				primary = w.ownSeller
			}
		}
	}
	sellers := []*domain.Seller{primary}
	if competitiveDept[l.dept.name] && p.Traits.BasePop > 4 && (p.Brand == nil || !p.Brand.PrivateLabel) {
		n := 1 + r.IntN(3)
		if p.Traits.BasePop > 15 {
			n++
		}
		exclude := map[int32]bool{primary.ID: true}
		for i := 0; i < n; i++ {
			var s *domain.Seller
			if primary != w.ownSeller && r.Bool(0.35) && !exclude[w.ownSeller.ID] {
				s = w.ownSeller
			} else {
				s = w.sellerForLeaf(l, r, exclude)
			}
			if s == nil {
				continue
			}
			exclude[s.ID] = true
			sellers = append(sellers, s)
		}
	}
	margin := marginByDept[l.dept.name]
	if margin == 0 {
		margin = 0.3
	}
	for si, s := range sellers {
		priceMul := 1.0
		if si > 0 {
			priceMul = r.Range(0.92, 1.12)
		}
		for _, v := range p.Variants {
			if si > 0 && len(p.Variants) > 1 && r.Bool(0.4) {
				continue // competitors don't carry every variant
			}
			raw := basePrice * v.PriceFactor * priceMul
			price := domain.PsychPrice(raw, domain.RoundCents99)
			w.ids.Offer++
			o := &domain.Offer{
				ID: w.ids.Offer, VariantID: v.ID, SellerID: s.ID, Price: price, BasePrice: price,
				Status: "active", CreatedAt: p.CreatedAt, UpdatedAt: p.CreatedAt, Variant: v, Seller: s,
				HandlingDays: 1,
			}
			if si > 0 {
				o.CreatedAt = p.CreatedAt.Add(time.Duration(r.Range(1, 90*24)) * time.Hour)
				if o.CreatedAt.After(w.now) {
					o.CreatedAt = w.now
				}
				o.UpdatedAt = o.CreatedAt
			}
			if r.Bool(0.45) {
				o.ListPrice = domain.PsychPrice(raw*r.Range(1.1, 1.4), domain.RoundCents99)
			} else {
				o.ListPrice = price
			}
			o.Cost = domain.MoneyFromFloat(price.Float() / 1.2 * (1 - margin*r.Range(0.7, 1.3)))
			if s == w.ownSeller {
				o.Fulfillment = domain.FulfilPlatform
			} else if r.Bool(s.Traits.PlatformShare) {
				o.Fulfillment = domain.FulfilPlatform
			} else {
				o.Fulfillment = domain.FulfilSeller
				o.HandlingDays = int(math.Max(1, math.Round(s.Traits.HandlingDays)))
				o.StockQty = r.IntRange(3, 60)
			}
			if o.Fulfillment == domain.FulfilPlatform {
				w.placeStock(o, r)
			}
			v.Offers = append(v.Offers, o)
		}
	}
}

// placeStock assigns platform stock to one or two open warehouses.
func (w *World) placeStock(o *domain.Offer, r *Rand) {
	probs := map[string]float64{"LEJ1": 0.85, "TLB1": 0.45, "POZ1": 0.35, "ZAZ1": 0.3}
	for _, wh := range w.warehouses {
		if wh.OpenedAt.After(w.now) {
			continue
		}
		if r.Bool(probs[wh.Code]) {
			o.Stock = append(o.Stock, &domain.StockLevel{WarehouseID: wh.ID, OfferID: o.ID, UpdatedAt: o.CreatedAt})
		}
	}
	if len(o.Stock) == 0 {
		o.Stock = append(o.Stock, &domain.StockLevel{WarehouseID: w.warehouses[0].ID, OfferID: o.ID, UpdatedAt: o.CreatedAt})
	}
}

// expectedDaily estimates units/day an offer will sell (used for initial stock).
func (w *World) expectedDaily(o *domain.Offer, l *leaf) float64 {
	p := o.Variant.Product
	totalDept := 0.0
	for _, d := range w.depts {
		totalDept += d.weight
	}
	leafTot := 0.0
	for _, x := range l.dept.leaves {
		leafTot += x.p.Weight
	}
	popTot := 0.0
	for _, x := range l.products {
		popTot += x.Traits.BasePop
	}
	if popTot == 0 || leafTot == 0 {
		return 0.05
	}
	varTot := 0.0
	for _, v := range p.Variants {
		varTot += v.Demand
	}
	share := l.dept.weight / totalDept * l.p.Weight / leafTot * p.Traits.BasePop / popTot * o.Variant.Demand / varTot / float64(len(o.Variant.Offers))
	return w.baselineOrders(w.now) * 2.2 * share
}

// initStock sizes stock for a new platform offer.
func (w *World) initStock(o *domain.Offer, l *leaf, r *Rand) {
	if o.Fulfillment != domain.FulfilPlatform {
		return
	}
	d := w.expectedDaily(o, l)
	o.DemandEMA = d
	per := d / float64(len(o.Stock))
	for _, s := range o.Stock {
		s.OnHand = int(math.Max(float64(r.IntRange(2, 12)), math.Ceil(per*r.Range(20, 45))))
		s.ReorderPoint = int(math.Max(1, math.Ceil(per*r.Range(10, 18))))
	}
}

// ---------- demand weights ----------

func (w *World) tagBoost(t time.Time) map[string]float64 {
	b := map[string]float64{}
	mul := func(tag string, f float64) {
		if v, ok := b[tag]; ok {
			b[tag] = v * f
		} else {
			b[tag] = f
		}
	}
	for _, cp := range w.cal.activeCampaigns(t) {
		switch cp.Name {
		case "Valentine's Day":
			days := cp.EndsAt.Sub(t).Hours() / 24
			mul("valentine", 1.5+2.0*math.Max(0, 1-days/14))
		case "Easter Specials":
			mul("easter", 2.0)
		case "Mother's Day Gifts":
			mul("gift", 1.3)
		case "Football Championship":
			mul("football", 2.2)
		case "Back to School":
			mul("school", 1.6)
		case "Black Friday Week":
			mul("blackfriday", 1.5)
			mul("gift", 1.25)
		case "Lumo Days":
			mul("blackfriday", 1.3)
		case "Christmas Gifts":
			day := float64(t.Day())
			mul("gift", 1.5+0.7*math.Min(1, day/15))
			mul("xmas", 1.4)
		case "Halloween":
			mul("halloween", 1.5)
		}
	}
	return b
}

// refreshDemand recomputes cached demand weights (hourly).
func (w *World) refreshDemand() {
	t := w.now
	boost := w.tagBoost(t)
	mi := int(t.Month()) - 1
	df := float64(t.Day()) / 31
	for _, d := range w.depts {
		sum, base := 0.0, 0.0
		for _, l := range d.leaves {
			s := l.season[mi]*(1-df) + l.season[(mi+1)%12]*df
			for _, tg := range l.p.Tags {
				if f, ok := boost[tg]; ok {
					s *= f
				}
			}
			l.nowW = s
			w.refreshLeaf(l)
			if l.total > 0 {
				sum += l.p.Weight * s
			}
			base += l.p.Weight
		}
		if base > 0 {
			d.nowW = d.weight * sum / base
		}
	}
}

func (w *World) productWeight(p *domain.Product) float64 {
	if p.Status != "active" && p.Status != "discontinued" {
		return 0
	}
	l := w.leafByCat[p.CategoryID]
	age := w.now.Sub(p.LaunchedAt).Hours() / 24
	lf := lifeFactor(l.life, p.Traits.LifeScale, age)
	if p.Status == "discontinued" {
		lf *= 0.5
	}
	wt := p.Traits.BasePop * lf * (1 - p.Traits.Decline)
	if p.Traits.RatingCnt >= 3 {
		avg := float64(p.Traits.RatingSum) / float64(p.Traits.RatingCnt)
		wt *= 0.55 + 0.1*avg
	}
	best := math.Inf(1)
	ratio := 1.0
	avail := false
	for _, v := range p.Variants {
		for _, o := range v.Offers {
			if o.Status != "active" {
				continue
			}
			if o.Available() > 0 {
				avail = true
			}
			if pr := o.Price.Float(); pr < best {
				best = pr
				if o.BasePrice > 0 {
					ratio = pr / o.BasePrice.Float()
				}
			}
		}
	}
	if math.IsInf(best, 1) {
		return 0
	}
	wt *= math.Min(8, math.Max(0.3, math.Pow(ratio, -2.2)))
	if !avail {
		wt *= 0.04
	}
	if p.Traits.ViralUntil > w.now.Unix() {
		left := float64(p.Traits.ViralUntil-w.now.Unix()) / 86400
		wt *= 1 + p.Traits.ViralBoost*math.Min(1, left/3)
	}
	return wt
}

func (w *World) refreshLeaf(l *leaf) {
	// drop products that are gone for good
	keep := l.products[:0]
	for _, p := range l.products {
		if p.Status == "deleted" {
			continue
		}
		keep = append(keep, p)
	}
	l.products = keep
	if cap(l.cum) < len(l.products) {
		l.cum = make([]float64, len(l.products))
	}
	l.cum = l.cum[:len(l.products)]
	tot := 0.0
	for i, p := range l.products {
		tot += w.productWeight(p)
		l.cum[i] = tot
	}
	l.total = tot
}

func (l *leaf) pickProduct(r *Rand) *domain.Product {
	if l.total <= 0 || len(l.products) == 0 {
		return nil
	}
	x := r.Float64() * l.total
	i := sort.SearchFloat64s(l.cum, x)
	if i >= len(l.products) {
		i = len(l.products) - 1
	}
	return l.products[i]
}

// weatherBoost multiplies demand of heat/cold sensitive leaves.
func (w *World) weatherBoost(l *leaf, events []*weatherEvent) float64 {
	f := 1.0
	for _, we := range events {
		for _, tg := range l.p.Tags {
			if tg == we.kind {
				f *= we.strength
			}
		}
	}
	return f
}

// buyBox picks the offer a customer ends up buying for a variant.
func (w *World) buyBox(v *domain.Variant, r *Rand) *domain.Offer {
	var best *domain.Offer
	bestScore := -1.0
	var cands []*domain.Offer
	for _, o := range v.Offers {
		if o.Status != "active" || o.Available() <= 0 {
			continue
		}
		cands = append(cands, o)
		score := math.Pow(o.Price.Float(), -4)
		if o.Seller != nil {
			score *= 0.7 + 0.1*o.Seller.Rating
		}
		if o.Fulfillment == domain.FulfilPlatform {
			score *= 1.15
		}
		if score > bestScore {
			best, bestScore = o, score
		}
	}
	if len(cands) > 1 && r.Bool(0.15) {
		return cands[r.IntN(len(cands))]
	}
	return best
}

func (w *World) pickVariant(p *domain.Product, r *Rand) *domain.Variant {
	if len(p.Variants) == 1 {
		return p.Variants[0]
	}
	ws := make([]float64, len(p.Variants))
	for i, v := range p.Variants {
		ws[i] = v.Demand
		ok := false
		for _, o := range v.Offers {
			if o.Status == "active" && o.Available() > 0 {
				ok = true
				break
			}
		}
		if !ok {
			ws[i] *= 0.15
		}
	}
	return p.Variants[r.WeightedIndex(ws)]
}
