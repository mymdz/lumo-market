package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

// ---------- arrivals ----------

// generateArrivals schedules shopping sessions within [from, to).
func (w *World) generateArrivals(from, to time.Time) {
	r := w.rng
	dt := to.Sub(from)
	dtMin := dt.Minutes()
	for _, m := range w.markets {
		if m.LaunchedAt.After(from) {
			continue
		}
		s := w.sessionFactor(m, from)
		w.accumulateTarget(m, s, dtMin)
		retRate := m.fen.Total() / 1440 * s
		newRate := w.acquisitionRate(m, from) / 1440 * s
		nRet := r.Poisson(retRate * dtMin)
		nNew := r.Poisson(newRate * dtMin)
		if m.fen.Len() == 0 {
			nRet = 0
		}
		for i := 0; i < nRet; i++ {
			idx := m.fen.Find(r.Float64() * m.fen.Total())
			id := m.fenIDs[idx]
			at := from.Add(time.Duration(r.Float64() * float64(dt)))
			w.q.Push(at, evSession, m, id)
		}
		for i := 0; i < nNew; i++ {
			at := from.Add(time.Duration(r.Float64() * float64(dt)))
			w.q.Push(at, evSession, m, 0)
		}
	}
	// bot traffic: junk carts
	if v, ok := w.cal.incidentFactor(from, "bot_attack", ""); ok {
		n := r.Poisson(v / 60 * dtMin)
		for i := 0; i < n; i++ {
			m := w.markets[r.WeightedIndex(marketWeights(w))]
			if m.LaunchedAt.After(from) {
				continue
			}
			at := from.Add(time.Duration(r.Float64() * float64(dt)))
			w.q.Push(at, evSession, m, -1)
		}
	}
}

// launchRamp is how established a market is (0..1) plus a launch push.
func (w *World) launchRamp(m *market, t time.Time) float64 {
	age := t.Sub(m.LaunchedAt).Hours() / 24
	if age < 0 {
		return 0
	}
	return math.Min(1, math.Pow(age/90, 0.7))
}

func (w *World) launchedShare(t time.Time) float64 {
	s := 0.0
	for _, m := range w.markets {
		s += m.spec.Weight * w.launchRamp(m, t)
	}
	return s
}

// baselineOrders is the smooth target of orders per day (without seasonality).
func (w *World) baselineOrders(t time.Time) float64 {
	cfg := w.cfg
	s0 := w.launchedShare(w.start)
	if s0 <= 0 {
		s0 = 1
	}
	sh := w.launchedShare(w.horizon)
	g0 := cfg.OrdersStart
	g1 := cfg.OrdersNow * s0 / math.Max(sh, 1e-9)
	var g float64
	total := w.horizon.Sub(w.start).Hours()
	if total <= 0 || !t.Before(w.horizon) {
		years := t.Sub(w.horizon).Hours() / 24 / 365
		if total <= 0 {
			g = g1 * math.Pow(1+cfg.GrowthAfter, years)
		} else {
			g = g1 * math.Pow(1+cfg.GrowthAfter, years)
		}
	} else {
		x := t.Sub(w.start).Hours() / total
		if x < 0 {
			x = 0
		}
		g = g0 * math.Pow(g1/g0, x)
	}
	return g * w.launchedShare(t) / s0
}

func (w *World) marketShare(m *market, t time.Time) float64 {
	tot := w.launchedShare(t)
	if tot <= 0 {
		return 0
	}
	return m.spec.Weight * w.launchRamp(m, t) / tot
}

// acquisitionRate returns new-visitor sessions per day in a market.
func (w *World) acquisitionRate(m *market, t time.Time) float64 {
	base := 0.42 * w.baselineOrders(t) * w.marketShare(m, t) / 0.24
	age := t.Sub(m.LaunchedAt).Hours() / 24
	if age < 60 {
		base *= 1 + 0.8*math.Exp(-age/30) // launch marketing push
	}
	for _, cp := range w.cal.activeCampaigns(t) {
		if cp.AcqBoost > 0 && containsStr(cp.Countries, m.Code) {
			left := cp.EndsAt.Sub(t).Hours() / (24 * 7)
			base *= 1 + (cp.AcqBoost-1)*left
		}
	}
	g := w.gain(m)
	return base * g
}

func (w *World) gain(m *market) float64 {
	if w.gains == nil {
		w.gains = map[string]float64{}
	}
	g, ok := w.gains[m.Code]
	if !ok {
		g = 1
		w.gains[m.Code] = g
	}
	return g
}

// ---------- sessions ----------

func (w *World) onSession(ev event) {
	m := ev.ref.(*market)
	r := w.rng
	var c *domain.Customer
	cs := &cartState{Market: m.Code}
	switch {
	case ev.aux > 0:
		c = w.customer(ev.aux)
		if c == nil || c.Status != domain.CustActive {
			return
		}
		if c.Active && w.now.Unix() > c.ChurnAt {
			c.Active = false
			w.updateWeight(c)
			w.uow.custTraits(c)
			return
		}
		if !c.Active && r.Bool(0.5) {
			return
		}
		cs.CustID = c.ID
	case ev.aux == 0:
		pr := w.newProspect(m, w.now)
		cs.Prospect = pr
		c = &pr.C
	default:
		cs.Bot = true
	}
	w.startCart(cs, c, m, false)
}

func (w *World) startCart(cs *cartState, c *domain.Customer, m *market, replenish bool) {
	r := w.rng
	w.ids.Cart++
	cart := &domain.Cart{
		ID: w.ids.Cart, CustomerID: cs.CustID, SessionID: uuid(r), Status: "active",
		Country: m.Code, Currency: m.Currency, CreatedAt: w.now, UpdatedAt: w.now,
	}
	cs.Cart = cart
	cs.Replenish = replenish
	w.st.sessions++
	if cs.Bot {
		n := r.IntRange(1, 3)
		for i := 0; i < n; i++ {
			l := w.leaves[r.IntN(len(w.leaves))]
			if p := l.pickProduct(r); p != nil {
				if o := w.buyBox(w.pickVariant(p, r), r); o != nil {
					w.addCartItem(cs, m, o, 1)
				}
			}
		}
		cart.NextAt = w.now.Add(time.Duration(r.Range(5, 60)) * time.Second)
	} else {
		sd := segDefs[c.Segment]
		cs.Budget = r.LogNorm(sd.budget, 0.8)
		w.fillCart(cs, c, m)
		// items are recorded one after another up to now (never after now)
		at := w.now
		for i := len(cart.Items) - 1; i >= 0; i-- {
			cart.Items[i].AddedAt = at
			at = at.Add(-time.Duration(r.Range(2, 20)) * time.Second)
		}
		if len(cart.Items) > 0 {
			cart.CreatedAt = cart.Items[0].AddedAt
		}
		dur := time.Duration(r.LogNorm(9, 0.6) * float64(time.Minute))
		cart.NextAt = w.now.Add(dur)
	}
	cs.Stage = "decide"
	w.carts[cart.ID] = cs
	w.q.Push(cart.NextAt, evCart, cs, cart.NextAt.UnixNano())
	w.uow.cart(cs)
}

func uuid(r *Rand) string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(r.IntN(256))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// pickDept selects a department for a customer.
func (w *World) pickDept(c *domain.Customer) *dept {
	ws := make([]float64, len(w.depts))
	for i, d := range w.depts {
		ws[i] = d.nowW * (float64(c.Affinity[d.idx])/255 + 0.03)
	}
	return w.depts[w.rng.WeightedIndex(ws)]
}

func (w *World) pickLeaf(d *dept, m *market) *leaf {
	we := w.weatherAt(w.now, m.Code)
	ws := make([]float64, len(d.leaves))
	for i, l := range d.leaves {
		if l.total <= 0 {
			continue
		}
		ws[i] = l.p.Weight * l.nowW
		if len(we) > 0 {
			ws[i] *= w.weatherBoost(l, we)
		}
	}
	return d.leaves[w.rng.WeightedIndex(ws)]
}

func tierPref(c *domain.Customer, t domain.Tier) float64 {
	s := float64(c.PriceSens)
	switch t {
	case domain.TierBudget:
		return 0.5 + s
	case domain.TierPremium:
		return math.Max(0.08, 1.4-1.4*s)
	default:
		return 1
	}
}

// pickFromLeaf picks a product the customer is willing to consider: brand
// tier preference and the session budget act as soft filters.
func (w *World) pickFromLeaf(l *leaf, c *domain.Customer, budget float64) *domain.Product {
	r := w.rng
	var p *domain.Product
	for i := 0; i < 6; i++ {
		p = l.pickProduct(r)
		if p == nil {
			return nil
		}
		t := domain.TierMid
		if p.Brand != nil {
			t = p.Brand.Tier
		}
		accept := tierPref(c, t) / 1.5
		if budget > 0 {
			if price := w.productBasePrice(p); price > budget {
				accept *= math.Pow(budget/price, 0.8)
			}
		}
		if r.Float64() < accept {
			return p
		}
	}
	return p
}

func (w *World) fillCart(cs *cartState, c *domain.Customer, m *market) {
	r := w.rng
	sd := segDefs[c.Segment]
	n := 1 + r.Poisson(sd.basket-1)
	if n > 8 {
		n = 8
	}
	// replenishment first
	if len(c.Repl) > 0 {
		now := w.now.Unix()
		for i := range c.Repl {
			rp := &c.Repl[i]
			if rp.DueAt < now+int64(rp.Cycle)*86400/5 && r.Bool(0.7) {
				if o := w.offers[rp.OfferID]; o != nil && o.Status == "active" && o.Available() > 0 {
					l := w.leafByCat[o.Variant.Product.CategoryID]
					qty := 1
					if l != nil && l.p.MaxQty > 1 {
						qty = r.IntRange(1, l.p.MaxQty)
					}
					w.addCartItem(cs, m, o, qty)
					rp.DueAt = now + int64(float64(rp.Cycle)*86400*r.LogNorm(1, 0.25))
					n--
				}
			}
		}
	}
	var lastLeaf *leaf
	var lastProd *domain.Product
	for i := 0; i < n; i++ {
		var l *leaf
		switch {
		case lastLeaf != nil && len(lastLeaf.related) > 0 && r.Bool(0.35):
			l = lastLeaf.related[r.IntN(len(lastLeaf.related))]
		case lastLeaf != nil && r.Bool(0.25):
			l = lastLeaf
		default:
			l = w.pickLeaf(w.pickDept(c), m)
		}
		p := w.pickFromLeaf(l, c, cs.Budget)
		if p == nil {
			continue
		}
		v := w.pickVariant(p, r)
		o := w.buyBox(v, r)
		if o == nil {
			w.st.lostDemand++
			if r.Bool(0.6) { // substitute
				p = w.pickFromLeaf(l, c, cs.Budget)
				if p == nil {
					continue
				}
				v = w.pickVariant(p, r)
				o = w.buyBox(v, r)
			}
			if o == nil {
				continue
			}
		}
		qty := 1
		if l.p.MaxQty > 1 && l.p.Repeat > 0 {
			qty = int(math.Min(float64(l.p.MaxQty), float64(1+r.Poisson(0.6))))
		} else if r.Bool(0.04) {
			qty = 2
		}
		w.addCartItem(cs, m, o, qty)
		// bracketing: order the same item in two sizes, return one later
		if c.ReturnProp > 1.6 && len(p.Variants) > 2 && strings.Contains(l.dept.name, "Fashion") && r.Bool(0.35) {
			for _, v2 := range p.Variants {
				if v2 != v && v2.Attrs["size_apparel"] != v.Attrs["size_apparel"] && samecolor(v, v2) {
					if o2 := w.buyBox(v2, r); o2 != nil {
						w.addCartItem(cs, m, o2, 1)
						break
					}
				}
			}
		}
		lastLeaf, lastProd = l, p
	}
	_ = lastProd
}

func samecolor(a, b *domain.Variant) bool {
	for k, v := range a.Attrs {
		if strings.HasPrefix(k, "color") && b.Attrs[k] != v {
			return false
		}
	}
	return true
}

func (w *World) addCartItem(cs *cartState, m *market, o *domain.Offer, qty int) {
	for _, it := range cs.Cart.Items {
		if it.OfferID == o.ID {
			it.Qty += qty
			return
		}
	}
	w.ids.CartItem++
	cs.Cart.Items = append(cs.Cart.Items, &domain.CartItem{
		ID: w.ids.CartItem, CartID: cs.Cart.ID, OfferID: o.ID, Qty: qty,
		UnitPrice: w.localPrice(m, o.Price), AddedAt: w.now,
	})
}

// localPrice converts an EUR shelf price into the market's shelf price.
func (w *World) localPrice(m *market, eur domain.Money) domain.Money {
	if m.cur.Code == "EUR" {
		return eur
	}
	return domain.PsychPrice(eur.Float()*m.cur.priceRate, m.cur.Rounding)
}

// convert converts an EUR amount (fees, thresholds) into local currency.
func (w *World) convert(m *market, eur domain.Money) domain.Money {
	if m.cur.Code == "EUR" {
		return eur
	}
	return domain.PsychPrice(eur.Float()*m.cur.priceRate, m.cur.Rounding)
}

// ---------- checkout ----------

func (w *World) onCart(ev event) {
	cs := ev.ref.(*cartState)
	c := cs.Cart
	if c.NextAt.UnixNano() != ev.aux || c.NextAt.IsZero() {
		return
	}
	r := w.rng
	m := w.marketByCode[cs.Market]
	switch cs.Stage {
	case "decide":
		if cs.Bot || len(c.Items) == 0 {
			w.abandon(cs, false)
			return
		}
		var cust *domain.Customer
		if cs.CustID > 0 {
			cust = w.customer(cs.CustID)
		} else if cs.Prospect != nil {
			cust = &cs.Prospect.C
		}
		if cust == nil {
			w.abandon(cs, false)
			return
		}
		p := 0.34 * segDefs[cust.Segment].conv
		if cs.CustID == 0 {
			p *= 0.72
		}
		switch cust.Device {
		case 1:
			p *= 0.85
		case 2, 3:
			p *= 1.15
		}
		for _, cp := range w.cal.activeCampaigns(w.now) {
			if cp.Type == "mega_sale" {
				p *= 1.15
			}
		}
		if v, ok := w.cal.incidentFactor(w.now, "site_degraded", ""); ok {
			p *= v
		}
		if cust.Device == 3 {
			if v, ok := w.cal.incidentFactor(w.now, "android_crash", ""); ok {
				p *= v
			}
		}
		total := 0.0
		for _, it := range c.Items {
			total += it.UnitPrice.Float() * float64(it.Qty)
		}
		totalEUR := total / m.cur.priceRate
		if totalEUR > cs.Budget {
			p *= math.Pow(cs.Budget/totalEUR, 0.8)
		}
		if totalEUR < m.spec.FreeOver && cust.Tier < 2 {
			p *= 0.85
		}
		if cs.Replenish {
			p = math.Max(p, 0.6)
		}
		if r.Bool(p) {
			w.checkout(cs, m)
			return
		}
		canRecover := (cs.CustID > 0 && cust.Has(domain.FlagOptIn)) || (cs.CustID == 0 && r.Bool(0.4))
		if canRecover && r.Bool(0.25) {
			cs.Stage = "recover"
			c.Status = "abandoned"
			c.UpdatedAt = w.now
			c.NextAt = w.now.Add(time.Duration(r.LogNorm(5, 0.7) * float64(time.Hour)))
			w.q.Push(c.NextAt, evCart, cs, c.NextAt.UnixNano())
			w.uow.cart(cs)
			return
		}
		w.abandon(cs, true)
	case "recover":
		ok := true
		for _, it := range c.Items {
			if o := w.offers[it.OfferID]; o == nil || o.Status != "active" || o.Available() <= 0 {
				ok = false
			}
		}
		if ok && r.Bool(0.45) {
			cs.Recovered = true
			w.checkout(cs, m)
			return
		}
		w.abandon(cs, true)
	}
}

func (w *World) abandon(cs *cartState, touch bool) {
	c := cs.Cart
	c.Status = "abandoned"
	if touch && w.bump("carts") {
		c.UpdatedAt = w.now
	}
	c.NextAt = time.Time{}
	delete(w.carts, c.ID)
	w.uow.cart(cs)
}

// bump reports whether updated_at should be bumped (false during an
// "updated_at not maintained" bug window for that table).
func (w *World) bump(table string) bool {
	if w.cfg.Dirt <= 0 {
		return true
	}
	_, bug := w.cal.incidentFactor(w.now, "updated_at_bug", table)
	return !bug
}

func (w *World) checkout(cs *cartState, m *market) {
	r := w.rng
	cart := cs.Cart
	var cust *domain.Customer
	newCust := false
	guestEmail := ""
	var shipAddr int64
	if cs.CustID > 0 {
		cust = w.customer(cs.CustID)
	} else if cs.Prospect != nil {
		if r.Bool(0.88) {
			cust = w.register(cs.Prospect, w.now)
			newCust = true
			cart.CustomerID = cust.ID
			cs.CustID = cust.ID
		} else {
			guestEmail = cs.Prospect.Profile.Email
			if w.dirty(0.1) {
				guestEmail = strings.ToUpper(guestEmail[:1]) + guestEmail[1:] + " "
			}
			w.ids.Address++
			a := cs.Prospect.Addr
			a.ID = w.ids.Address
			a.CreatedAt = w.now
			w.uow.addAddress(&a)
			shipAddr = a.ID
		}
	}
	if cust != nil {
		shipAddr = cust.AddressID
		if r.Bool(0.03) {
			w.ids.Address++
			pr := w.newProspect(m, w.now)
			a := pr.Addr
			a.ID = w.ids.Address
			a.CustomerID = cust.ID
			a.CreatedAt = w.now
			w.uow.addAddress(&a)
			shipAddr = a.ID
		}
	}
	o := w.placeOrder(cs, m, cust, newCust, guestEmail, shipAddr)
	cart.Status = "converted"
	cart.ConvertedOrderID = o.ID
	cart.UpdatedAt = w.now
	cart.NextAt = time.Time{}
	delete(w.carts, cart.ID)
	w.uow.cart(cs)
}

var redirectMethods = map[string]bool{
	"ideal": true, "bancontact": true, "blik": true, "paypal": true, "eps": true, "giropay": true,
	"przelewy24": true, "mbway": true, "multibanco": true, "bizum": true, "satispay": true, "swish": true,
	"mobilepay": true, "trustly": true, "mia": true,
}

func providerFor(method string, r *Rand) string {
	switch method {
	case "paypal":
		return "paypal"
	case "klarna":
		return "klarna"
	case "cod":
		return ""
	case "bank_transfer":
		return "bank"
	case "card", "apple_pay", "google_pay":
		if r.Bool(0.3) {
			return "stripe"
		}
		return "adyen"
	default:
		return "adyen"
	}
}

func (w *World) payMethod(m *market, c *domain.Customer) string {
	if c != nil && c.PayPref != 255 && int(c.PayPref) < len(m.payList) && w.rng.Bool(0.85) {
		return m.payList[c.PayPref]
	}
	return m.payments.Pick(w.rng)
}

var utmByChannel = map[uint8]string{0: "google_organic", 1: "google_ads", 2: "instagram", 3: "awin", 4: "newsletter", 5: "referral", 6: "", 7: "tiktok"}

func (w *World) placeOrder(cs *cartState, m *market, cust *domain.Customer, newCust bool, guestEmail string, shipAddr int64) *domain.Order {
	r := w.rng
	now := w.now
	w.ids.Order++
	o := &domain.Order{
		ID: w.ids.Order, Number: fmt.Sprintf("LM-%d-%07d", now.Year(), w.ids.Order),
		GuestEmail: guestEmail, CartID: cs.Cart.ID, Status: domain.OrderPendingPayment,
		Currency: m.Currency, FX: m.cur.rate, Country: m.Code, ShipAddrID: shipAddr, BillAddrID: shipAddr,
		PlacedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	o.Sim.NewCust = newCust
	var traits *domain.Customer
	if cust != nil {
		o.CustomerID = cust.ID
		traits = cust
		o.Channel = domain.Devices[cust.Device]
		if cust.Has(domain.FlagFraud) {
			o.Sim.Fraud = true
		}
		if cust.Has(domain.FlagTest) {
			o.Sim.Test = true
		}
	} else if cs.Prospect != nil {
		traits = &cs.Prospect.C
		o.Channel = domain.Devices[traits.Device]
	} else {
		o.Channel = "web_desktop"
	}
	if w.applied["m002_orders_utm"] && traits != nil {
		o.UTMSource = utmByChannel[traits.Channel]
		for _, cp := range w.cal.activeCampaigns(now) {
			if cp.Type != "influencer" {
				o.UTMCampaign = strings.ToLower(strings.ReplaceAll(cp.Name, " ", "_"))
				break
			}
		}
	}

	// items
	for i, ci := range cs.Cart.Items {
		off := w.offers[ci.OfferID]
		if off == nil {
			continue
		}
		l := w.leafByCat[off.Variant.Product.CategoryID]
		w.ids.OrderItem++
		title := off.Variant.Product.Title
		if off.Variant.Name != "" {
			title += ", " + off.Variant.Name
		}
		rate := m.VAT
		if l != nil && l.p.VAT == "reduced" {
			rate = m.VATReduced
		}
		it := &domain.OrderItem{
			ID: w.ids.OrderItem, OrderID: o.ID, LineNo: i + 1, OfferID: off.ID, VariantID: off.VariantID,
			ProductID: off.Variant.ProductID, SellerID: off.SellerID, Title: title, Qty: ci.Qty,
			UnitPrice: w.localPrice(m, off.Price), TaxRate: rate, Status: "ordered",
		}
		if off.SellerID != w.ownSeller.ID && l != nil {
			it.Commission = commissionByDept[l.dept.name]
		}
		if w.applied["m004_order_items_gift_wrap"] {
			pg := 0.03
			if now.Month() == time.December {
				pg = 0.12
			}
			it.GiftWrap = r.Bool(pg)
		}
		o.Items = append(o.Items, it)
		o.Subtotal += it.UnitPrice * domain.Money(it.Qty)
	}
	// bracketing marks
	seen := map[int64]int{}
	for _, it := range o.Items {
		seen[it.ProductID]++
		if seen[it.ProductID] > 1 {
			it.Bracket = true
		}
	}

	// coupon
	w.applyCoupon(o, m, traits, newCust)
	// shipping
	lockerShare := m.spec.LockerShare
	switch {
	case r.Bool(lockerShare):
		o.ShipMethod = "locker"
	case r.Bool(0.08):
		o.ShipMethod = "express"
	default:
		o.ShipMethod = "standard"
	}
	net := o.Subtotal - o.Discount
	freeShip := net >= w.convert(m, m.freeOver) || (traits != nil && traits.Tier >= 2)
	if o.CouponID != 0 {
		for _, cu := range w.coupons {
			if cu.ID == o.CouponID && cu.DiscountType == "free_shipping" {
				freeShip = true
			}
		}
	}
	switch {
	case o.ShipMethod == "express":
		o.ShippingFee = w.convert(m, m.express)
	case freeShip:
		o.ShippingFee = 0
	case o.ShipMethod == "locker":
		o.ShippingFee = w.convert(m, m.locker)
	default:
		o.ShippingFee = w.convert(m, m.shipFee)
	}
	// taxes (prices are gross)
	for _, it := range o.Items {
		it.LineTotal = it.UnitPrice*domain.Money(it.Qty) - it.Discount
		it.Tax = domain.MoneyFromFloat(it.LineTotal.Float() - it.LineTotal.Float()/(1+it.TaxRate/100))
		o.Tax += it.Tax
	}
	o.Tax += domain.MoneyFromFloat(o.ShippingFee.Float() - o.ShippingFee.Float()/(1+m.VAT/100))
	o.Total = o.Subtotal - o.Discount + o.ShippingFee
	if w.dirty(0.001) {
		o.Total += 1 // rounding bug in a legacy checkout service
	}

	w.buildShipments(o, m)
	w.setStatus(o, "", domain.OrderPendingPayment, "customer")

	// payment
	method := w.payMethod(m, traits)
	if method == "cod" {
		o.Status = domain.OrderProcessing
		w.setStatus(o, domain.OrderPendingPayment, domain.OrderProcessing, "system")
		w.newPayment(o, method, domain.PayPending)
		w.startFulfilment(o)
	} else {
		w.newPayment(o, method, domain.PayPending)
		delay := time.Duration(r.Range(5, 25)) * time.Second
		if redirectMethods[method] {
			delay = time.Duration(r.Range(30, 240)) * time.Second
		}
		if method == "bank_transfer" {
			if r.Bool(0.85) {
				delay = time.Duration(r.Range(2, 60)) * time.Hour
			} else {
				o.Sim.Step, o.Sim.StepAt = "bank_timeout", now.Add(7*24*time.Hour)
				delay = 0
			}
		}
		if delay > 0 {
			o.Sim.Step, o.Sim.StepAt = "pay", now.Add(delay)
		}
	}
	if o.Sim.Fraud || (newCust && o.Total.Float()/m.cur.rate > 600 && r.Bool(0.004)) {
		o.Sim.Fraud = true
	}

	// customer stats
	if cust != nil {
		w.afterPurchase(cust, o, m)
	}
	w.st.today.Orders++
	w.st.today.Revenue += o.Total.Float() / m.cur.rate
	w.orderCount[m.Code]++
	w.orders[o.ID] = o
	w.scheduleOrder(o)
	return o
}

func (w *World) afterPurchase(c *domain.Customer, o *domain.Order, m *market) {
	r := w.rng
	now := w.now.Unix()
	eur := o.Total.Float() / m.cur.rate
	if c.SpentAt > 0 {
		c.Spent *= float32(math.Exp(-float64(now-c.SpentAt) / (180 * 86400)))
	}
	c.Spent += float32(eur)
	c.SpentAt = now
	c.Orders++
	c.LastOrderAt = now
	if !c.Active {
		c.Active = true
		c.ChurnAt = w.now.Add(time.Duration(r.Exp(300)*24) * time.Hour).Unix()
		w.updateWeight(c)
	}
	// consumables to re-buy
	for _, it := range o.Items {
		off := w.offers[it.OfferID]
		if off == nil {
			continue
		}
		l := w.leafByCat[off.Variant.Product.CategoryID]
		if l == nil || l.p.Repeat == 0 || !r.Bool(0.6) {
			continue
		}
		cycle := int32(float64(l.p.Repeat) * float64(it.Qty) * r.LogNorm(1, 0.25))
		rp := domain.Replenish{OfferID: off.ID, DueAt: now + int64(cycle)*86400, Cycle: cycle}
		replaced := false
		for i := range c.Repl {
			if c.Repl[i].OfferID == off.ID {
				c.Repl[i] = rp
				replaced = true
			}
		}
		if !replaced {
			if len(c.Repl) >= 4 {
				c.Repl = c.Repl[1:]
			}
			c.Repl = append(c.Repl, rp)
		}
		w.q.Push(time.Unix(rp.DueAt, 0), evReplenish, c, int64(off.ID))
	}
	w.uow.custTraits(c)
}

func (w *World) onReplenish(ev event) {
	c := ev.ref.(*domain.Customer)
	if c.Status != domain.CustActive {
		return
	}
	due := false
	for _, rp := range c.Repl {
		if rp.OfferID == ev.aux && rp.DueAt <= w.now.Unix()+3600 {
			due = true
		}
	}
	if !due || !w.rng.Bool(0.55) {
		return
	}
	m := w.marketOf(c)
	cs := &cartState{Market: m.Code, CustID: c.ID}
	w.startCart(cs, c, m, true)
}

func (w *World) applyCoupon(o *domain.Order, m *market, c *domain.Customer, newCust bool) {
	if c == nil {
		return
	}
	r := w.rng
	aff := float64(c.CouponAff)
	var cands []*domain.Coupon
	for _, cu := range w.activeCoupons() {
		if len(cu.Countries) > 0 && !containsStr(cu.Countries, m.Code) {
			continue
		}
		switch cu.Kind {
		case "welcome":
			if newCust || c.Orders <= 1 {
				cands = append(cands, cu)
			}
		case "loyalty":
			if c.Tier >= 2 {
				cands = append(cands, cu)
			}
		case "winback":
			if c.Orders > 1 && c.LastOrderAt > 0 && w.now.Unix()-c.LastOrderAt > 150*86400 {
				cands = append(cands, cu)
			}
		default:
			cands = append(cands, cu)
		}
	}
	if len(cands) == 0 || !r.Bool(aff*0.55) {
		return
	}
	cu := cands[r.IntN(len(cands))]
	if cu.MaxUses > 0 && cu.TimesUsed >= cu.MaxUses {
		return
	}
	minOrder := w.convert(m, cu.MinOrder)
	if o.Subtotal < minOrder {
		return
	}
	var disc domain.Money
	switch cu.DiscountType {
	case "percent":
		disc = o.Subtotal.Mul(cu.DiscountValue / 100)
	case "fixed":
		disc = w.convert(m, domain.MoneyFromFloat(cu.DiscountValue))
	}
	if disc > o.Subtotal {
		disc = o.Subtotal
	}
	o.CouponID = cu.ID
	o.Discount = disc
	// allocate over lines
	var alloc domain.Money
	for i, it := range o.Items {
		line := it.UnitPrice * domain.Money(it.Qty)
		share := domain.Money(math.Round(float64(disc) * float64(line) / float64(o.Subtotal)))
		if i == len(o.Items)-1 {
			share = disc - alloc
		}
		it.Discount = share
		alloc += share
	}
	cu.TimesUsed++
	cu.UpdatedAt = w.now
	w.uow.coupon(cu)
}

func (w *World) activeCoupons() []*domain.Coupon {
	var out []*domain.Coupon
	for _, cu := range w.coupons {
		if !w.now.Before(cu.ValidFrom) && (cu.ValidTo.IsZero() || w.now.Before(cu.ValidTo)) {
			out = append(out, cu)
		}
	}
	return out
}

// reserve takes stock for an ordered offer and returns the warehouse it ships
// from (0 for seller-fulfilled offers).
func (w *World) reserve(o *domain.Offer, qty int, m *market) int16 {
	w.st.offerSales[o.ID] += qty
	if o.Fulfillment == domain.FulfilSeller {
		o.StockQty -= qty
		if o.StockQty < 0 && !w.dirty(0.3) {
			o.StockQty = 0
		}
		w.uow.offer(o)
		return 0
	}
	var best *domain.StockLevel
	bestD := math.Inf(1)
	for _, s := range o.Stock {
		if s.OnHand-s.Reserved < qty {
			continue
		}
		wh := w.whByID[s.WarehouseID]
		d := haversine(wh.Lat, wh.Lon, m.spec.Lat, m.spec.Lon)
		if d < bestD {
			best, bestD = s, d
		}
	}
	if best == nil && len(o.Stock) > 0 {
		best = o.Stock[0] // oversold
	}
	if best == nil {
		return w.warehouses[0].ID
	}
	best.Reserved += qty
	best.UpdatedAt = w.now
	w.uow.stockLevel(best)
	return best.WarehouseID
}
