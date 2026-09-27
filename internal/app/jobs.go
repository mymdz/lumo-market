package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

// runJobs fires time-based jobs when the simulation crosses boundaries.
func (w *World) runJobs() {
	hour := w.now.Truncate(time.Hour)
	if hour.After(w.lastHourly) {
		w.lastHourly = hour
		w.hourly()
	}
	day := dayOf(w.now)
	if day.After(w.lastDaily) {
		if !w.lastDaily.IsZero() {
			w.rollover(w.lastDaily)
		}
		w.lastDaily = day
		w.startOfDay()
	}
	if w.now.Hour() >= 3 && w.nightlyDone.Before(day) {
		w.nightlyDone = day
		w.nightly()
	}
}

func (w *World) note(format string, args ...any) {
	msg := w.now.Format("2006-01-02 15:04") + " " + fmt.Sprintf(format, args...)
	w.events = append(w.events, msg)
	if len(w.events) > 40 {
		w.events = w.events[len(w.events)-40:]
	}
	if w.live() {
		w.log.Info("event", "msg", msg)
	}
}

// ---------- hourly ----------

func (w *World) hourly() {
	w.ensureYears()
	w.applyDueMigrations()
	w.refreshDemand()
	w.campaignPricing()
	w.pricingBugs()
	w.viral()
	w.duplicates()
	w.testOrders()
	w.lateTouches()
	if h := w.now.Hour(); h >= 7 && h <= 18 {
		w.launches(1.0 / 12) // the catalogue team works during the day
	}
	for _, m := range w.markets {
		if !m.LaunchedAt.After(w.now) && m.LaunchedAt.After(w.now.Add(-time.Hour)) {
			w.note("market launched: %s (%s)", m.Name, m.Currency)
		}
	}
	for _, wh := range w.warehouses {
		if !wh.OpenedAt.After(w.now) && wh.OpenedAt.After(w.now.Add(-time.Hour)) {
			w.note("warehouse opened: %s", wh.Name)
			w.stockNewWarehouse(wh)
		}
	}
}

// ensureYears makes sure the calendar (and campaign rows) exist ahead of time.
// ensureYears publishes planned campaigns and coupons at their planned
// creation date (marketing sets them up weeks ahead), so created_at is never
// in the future while starts_at/valid_from may legitimately be.
func (w *World) ensureYears() {
	known := make(map[int32]bool, len(w.campaigns))
	for _, c := range w.campaigns {
		known[c.ID] = true
	}
	knownC := make(map[int32]bool, len(w.coupons))
	for _, c := range w.coupons {
		knownC[c.ID] = true
	}
	for y := w.now.Year() - 1; y <= w.now.Year()+1; y++ {
		yc := w.cal.year(y)
		for _, cp := range yc.campaigns {
			if known[cp.ID] || cp.EndsAt.Before(w.start) || cp.CreatedAt.After(w.now) {
				continue
			}
			known[cp.ID] = true
			w.campaigns = append(w.campaigns, cp)
			w.uow.b.Campaigns = append(w.uow.b.Campaigns, cp)
		}
		for _, cu := range yc.coupons {
			if knownC[cu.ID] || (!cu.ValidTo.IsZero() && cu.ValidTo.Before(w.start)) || cu.CreatedAt.After(w.now) {
				continue
			}
			if cu.CampaignID != 0 && !known[cu.CampaignID] {
				continue // published together with (after) its campaign
			}
			knownC[cu.ID] = true
			w.coupons = append(w.coupons, cu)
			w.couponByCode[cu.Code] = cu
			w.uow.addCoupon(cu)
		}
	}
}

func (w *World) campaignPricing() {
	for _, cp := range w.campaigns {
		if !cp.Started && !w.now.Before(cp.StartsAt) && w.now.Before(cp.EndsAt) {
			cp.Started = true
			if cp.DiscountPct > 0 && cp.Share > 0 {
				n := w.applyPromo(cp)
				w.note("campaign started: %s (%d offers discounted)", cp.Name, n)
			} else {
				w.note("campaign started: %s", cp.Name)
			}
		}
		if cp.Started && !cp.Ended && !w.now.Before(cp.EndsAt) {
			cp.Ended = true
			w.endPromo(cp)
			w.note("campaign ended: %s", cp.Name)
		}
	}
}

func (w *World) inScope(cp *domain.Campaign, p *domain.Product) bool {
	if cp.BrandID != 0 {
		return p.BrandID == cp.BrandID
	}
	l := w.leafByCat[p.CategoryID]
	if len(cp.Depts) > 0 {
		ok := false
		for _, d := range cp.Depts {
			if l.dept.idx == d {
				ok = true
			}
		}
		if ok {
			return true
		}
	}
	if len(cp.Tags) > 0 {
		for _, t := range cp.Tags {
			for _, lt := range l.p.Tags {
				if t == lt {
					return true
				}
			}
		}
		return false
	}
	return len(cp.Depts) == 0
}

func (w *World) applyPromo(cp *domain.Campaign) int {
	r := NewRand(hashSeed(w.cfg.Seed, "promo", fmt.Sprint(cp.ID)))
	n := 0
	for _, pid := range w.sortedProductIDs() {
		p := w.products[pid]
		if p.Status != "active" || !w.inScope(cp, p) || !r.Bool(cp.Share) {
			continue
		}
		disc := r.Clamp(r.Norm(cp.DiscountPct/100, 0.08), 0.05, 0.6)
		for _, v := range p.Variants {
			for _, o := range v.Offers {
				if o.Status != "active" || o.PromoID != 0 {
					continue
				}
				if o.SellerID != w.ownSeller.ID && !r.Bool(0.5) {
					continue // not every merchant joins
				}
				np := domain.PsychPrice(o.BasePrice.Float()*(1-disc), domain.RoundCents99)
				w.changePrice(o, np, "campaign_start")
				o.PromoID = cp.ID
				n++
			}
		}
	}
	return n
}

func (w *World) endPromo(cp *domain.Campaign) {
	for _, pid := range w.sortedProductIDs() {
		p := w.products[pid]
		for _, v := range p.Variants {
			for _, o := range v.Offers {
				if o.PromoID == cp.ID {
					o.PromoID = 0
					if o.Status != "deleted" {
						w.changePrice(o, o.BasePrice, "campaign_end")
					}
				}
			}
		}
	}
}

func (w *World) sortedProductIDs() []int64 {
	ids := make([]int64, 0, len(w.products))
	for id := range w.products {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (w *World) changePrice(o *domain.Offer, np domain.Money, reason string) {
	if np <= 0 || np == o.Price {
		return
	}
	w.ids.PriceChange++
	w.uow.addPriceChange(&domain.PriceChange{ID: w.ids.PriceChange, OfferID: o.ID, OldPrice: o.Price, NewPrice: np, Reason: reason, ChangedAt: w.now})
	o.Price = np
	if w.bump("offers") {
		o.UpdatedAt = w.now
	}
	w.uow.offer(o)
}

func (w *World) pricingBugs() {
	for _, in := range w.cal.activeIncidents(w.now, "pricing_bug") {
		if in.handled {
			continue
		}
		in.handled = true
		// pick a popular own offer with stock
		var best *domain.Offer
		bestW := 0.0
		for _, pid := range w.sortedProductIDs() {
			p := w.products[pid]
			if p.Status != "active" || p.Traits.BasePop < 5 {
				continue
			}
			for _, v := range p.Variants {
				for _, o := range v.Offers {
					if o.SellerID == w.ownSeller.ID && o.Available() > 20 && o.Price.Float() > 80 {
						wt := p.Traits.BasePop * w.rng.Float64()
						if wt > bestW {
							best, bestW = o, wt
						}
					}
				}
			}
		}
		if best == nil {
			continue
		}
		in.target = fmt.Sprint(best.ID)
		np := domain.MoneyFromFloat(math.Max(0.99, math.Round(best.BasePrice.Float()*in.strength*100)/100))
		w.changePrice(best, np, "manual")
		best.Variant.Product.Traits.ViralUntil = in.end.Unix()
		best.Variant.Product.Traits.ViralBoost = 40
		w.refreshLeaf(w.leafByCat[best.Variant.Product.CategoryID])
		w.note("pricing error: offer %d '%s' listed at %s EUR", best.ID, best.Variant.Product.Title, np)
	}
	for _, yc := range w.cal.around(w.now) {
		for i := range yc.incidents {
			in := &yc.incidents[i]
			if in.kind != "pricing_bug" || !in.handled || in.target == "" || w.now.Before(in.end) {
				continue
			}
			var id int64
			fmt.Sscan(in.target, &id)
			in.target = ""
			if o := w.offers[id]; o != nil {
				w.changePrice(o, o.BasePrice, "pricing_fix")
				o.Variant.Product.Traits.ViralUntil = 0
				w.note("pricing error fixed: offer %d", id)
			}
		}
	}
}

func (w *World) viral() {
	r := w.rng
	if !r.Bool(0.2 / 24) {
		return
	}
	l := w.leaves[r.IntN(len(w.leaves))]
	p := l.pickProduct(r)
	if p == nil || p.Status != "active" {
		return
	}
	days := r.Range(3, 10)
	p.Traits.ViralUntil = w.now.Add(time.Duration(days*24) * time.Hour).Unix()
	p.Traits.ViralBoost = r.Range(5, 30)
	w.uow.productTraits(p)
	w.refreshLeaf(l)
	w.note("product went viral: %s (x%.0f for %.0f days)", p.Title, p.Traits.ViralBoost, days)
}

func (w *World) duplicates() {
	keep := w.dups[:0]
	for _, d := range w.dups {
		if d.at.After(w.now) {
			keep = append(keep, d)
			continue
		}
		m := w.marketByCode[d.market]
		if m == nil {
			continue
		}
		pr := &prospect{C: d.src, Profile: d.profile, Addr: d.addr}
		pr.C.Flags |= domain.FlagDuplicate
		pr.C.Orders, pr.C.Spent, pr.C.Repl, pr.C.Tier = 0, 0, nil, 0
		pr.C.Active = true
		if w.dirty(0.5) {
			pr.Addr.Line1 = strings.ToLower(pr.Addr.Line1)
		}
		c := w.register(pr, w.now)
		c.Flags |= domain.FlagDuplicate
	}
	w.dups = keep
}

func (w *World) testOrders() {
	if w.cfg.Dirt <= 0 || len(w.testCusts) == 0 {
		return
	}
	r := w.rng
	n := r.Poisson(3 * w.cfg.Dirt / 24)
	for i := 0; i < n; i++ {
		c := w.customer(w.testCusts[r.IntN(len(w.testCusts))])
		if c == nil {
			continue
		}
		m := w.marketOf(c)
		cs := &cartState{Market: m.Code, CustID: c.ID}
		w.ids.Cart++
		cs.Cart = &domain.Cart{ID: w.ids.Cart, CustomerID: c.ID, SessionID: uuid(r), Status: "active", Country: m.Code, Currency: m.Currency, CreatedAt: w.now, UpdatedAt: w.now}
		l := w.leaves[r.IntN(len(w.leaves))]
		if p := l.pickProduct(r); p != nil {
			if o := w.buyBox(w.pickVariant(p, r), r); o != nil {
				w.addCartItem(cs, m, o, 1)
			}
		}
		if len(cs.Cart.Items) == 0 {
			continue
		}
		w.checkout(cs, m)
	}
}

func (w *World) lateTouches() {
	keep := w.late[:0]
	for _, lt := range w.late {
		if lt.at.After(w.now) {
			keep = append(keep, lt)
			continue
		}
		w.uow.order(lt.o)
	}
	w.late = keep
}

func (w *World) stockNewWarehouse(wh *domain.Warehouse) {
	r := w.rng
	n := 0
	for _, pid := range w.sortedProductIDs() {
		p := w.products[pid]
		if p.Status != "active" {
			continue
		}
		for _, v := range p.Variants {
			for _, o := range v.Offers {
				if o.Fulfillment != domain.FulfilPlatform || o.Status != "active" || o.DemandEMA < 0.2 || !r.Bool(0.5) {
					continue
				}
				has := false
				for _, s := range o.Stock {
					if s.WarehouseID == wh.ID {
						has = true
					}
				}
				if has {
					continue
				}
				sl := &domain.StockLevel{WarehouseID: wh.ID, OfferID: o.ID, OnHand: 0, ReorderPoint: int(math.Ceil(o.DemandEMA * 5)), UpdatedAt: w.now}
				o.Stock = append(o.Stock, sl)
				w.uow.addStock(sl)
				n++
			}
		}
	}
	w.note("warehouse %s listed %d offers", wh.Code, n)
}

// ---------- daily ----------

func (w *World) startOfDay() {
	r := w.rng
	for _, m := range w.markets {
		m.noise = 0.7*m.noise + r.Norm(0, 0.06)
	}
	w.updateFX()
	if w.live() {
		w.uow.b.CartsDeleteBefore = w.now.Add(-30 * 24 * time.Hour)
	}
	w.updateDemandEMA()
}

// accumulateTarget integrates the session factor for the controller.
func (w *World) accumulateTarget(m *market, s, dtMin float64) {
	m.sAcc += s * dtMin
}

// rollover closes a day: statistics and the acquisition controller.
func (w *World) rollover(day time.Time) {
	st := w.st.today
	st.Day = day
	target := 0.0
	for _, m := range w.markets {
		if m.LaunchedAt.After(day.Add(24 * time.Hour)) {
			m.sAcc = 0
			continue
		}
		tm := w.baselineOrders(day.Add(12*time.Hour)) * w.marketShare(m, day.Add(12*time.Hour)) * m.sAcc / 1440
		m.sAcc = 0
		act := float64(w.orderCount[m.Code])
		m.targets = append(m.targets, tm)
		m.actuals = append(m.actuals, act)
		if len(m.targets) > 7 {
			m.targets = m.targets[1:]
			m.actuals = m.actuals[1:]
		}
		target += tm
		ts, as := 0.0, 0.0
		for i := range m.targets {
			ts += m.targets[i]
			as += m.actuals[i]
		}
		g := w.gain(m)
		ratio := (ts + 5) / (as + 5)
		g *= math.Max(0.88, math.Min(1.15, math.Pow(ratio, 0.7)))
		w.gains[m.Code] = math.Max(0.1, math.Min(10, g))
	}
	st.Target = target
	w.st.days = append(w.st.days, st)
	if len(w.st.days) > 14 {
		w.st.days = w.st.days[1:]
	}
	w.st.today = dayStat{}
	w.orderCount = map[string]int{}
	if !w.live() && day.Day() == 1 {
		w.log.Info("backfill progress", "day", day.Format("2006-01-02"), "orders", st.Orders, "target", math.Round(target),
			"customers", len(w.customers), "products", len(w.products), "open_orders", len(w.orders))
	}
}

func (w *World) updateFX() {
	day := dayOf(w.now)
	r := NewRand(hashSeed(w.cfg.Seed, "fx", day.Format("2006-01-02")))
	monday := w.now.Weekday() == time.Monday
	for _, code := range []string{"PLN", "CZK", "SEK", "DKK", "MDL"} {
		c := w.currencies[code]
		if c == nil {
			continue
		}
		c.rate *= math.Exp(c.Drift/365 + c.Vol*r.NormFloat64())
		if c.priceRate == 0 || monday {
			c.priceRate = c.rate
		}
		w.uow.b.FxRates = append(w.uow.b.FxRates, domain.FxRate{Date: day, Currency: code, Rate: math.Round(c.rate*1e6) / 1e6})
	}
}

func (w *World) nightly() {
	w.pricing()
	w.discontinue()
	w.sellerLife()
	w.customerLife()
	if w.now.Day() == 1 {
		w.loyaltyTiers()
	}
	if w.now.Day() == 5 {
		w.winback()
	}
	w.reorder()
	if w.now.Weekday() == time.Sunday {
		for _, m := range w.markets {
			m.fen.Rebuild()
		}
	}
}

func (w *World) pricing() {
	r := w.rng
	monthly := w.now.Day() == 1
	for _, pid := range w.sortedProductIDs() {
		p := w.products[pid]
		for _, v := range p.Variants {
			var offers []*domain.Offer
			for _, o := range v.Offers {
				if o.Status == "active" {
					offers = append(offers, o)
				}
			}
			for _, o := range offers {
				if o.PromoID != 0 {
					continue
				}
				own := o.SellerID == w.ownSeller.ID
				switch {
				case p.Status == "discontinued" && own && o.Price >= o.BasePrice && o.Available() > 0:
					np := domain.PsychPrice(o.BasePrice.Float()*r.Range(0.5, 0.75), domain.RoundCents99)
					w.changePrice(o, np, "clearance")
				case monthly && own && r.Bool(0.25):
					nb := domain.PsychPrice(o.BasePrice.Float()*r.Range(1.01, 1.05), domain.RoundCents99)
					o.BasePrice = nb
					w.changePrice(o, nb, "cost_increase")
				case own && r.Bool(0.015):
					nb := domain.PsychPrice(o.BasePrice.Float()*r.Range(0.9, 1.08), domain.RoundCents99)
					o.BasePrice = nb
					w.changePrice(o, nb, "repricing")
				case !own && len(offers) > 1 && r.Bool(0.05*(0.5+o.Seller.Traits.Aggressiveness)):
					low := o.Price
					for _, x := range offers {
						if x.Price < low {
							low = x.Price
						}
					}
					np := domain.MoneyFromFloat(low.Float() * r.Range(0.97, 0.995))
					if floor := o.Cost.Mul(1.08); np < floor {
						np = floor
					}
					if np < o.Price {
						o.BasePrice = np
						w.changePrice(o, np, "competitor_match")
					}
				case !own && r.Bool(0.01):
					nb := domain.PsychPrice(o.BasePrice.Float()*r.Range(0.92, 1.1), domain.RoundCents99)
					o.BasePrice = nb
					w.changePrice(o, nb, "repricing")
				}
			}
		}
	}
}

var launchSeason = map[string][12]float64{
	"Women's Fashion":    {0.6, 1.8, 2.0, 1.2, 0.8, 0.6, 0.5, 1.6, 1.9, 1.2, 0.7, 0.4},
	"Men's Fashion":      {0.6, 1.8, 2.0, 1.2, 0.8, 0.6, 0.5, 1.6, 1.9, 1.2, 0.7, 0.4},
	"Electronics":        {1.0, 1.1, 1.0, 0.9, 0.9, 0.8, 0.8, 0.9, 1.5, 1.4, 1.0, 0.7},
	"Toys & Games":       {0.6, 0.7, 0.7, 0.7, 0.7, 0.8, 0.9, 1.2, 1.6, 1.8, 1.4, 0.6},
	"Garden & Outdoor":   {1.2, 2.0, 2.0, 1.4, 1.0, 0.8, 0.6, 0.5, 0.5, 0.5, 0.6, 0.7},
	"Books & Stationery": {1.0, 1.0, 1.0, 1.0, 1.0, 0.9, 0.9, 1.0, 1.3, 1.3, 1.1, 0.7},
}

// launches lists new products; share is the fraction of a day's launches.
func (w *World) launches(share float64) {
	r := w.rng
	perDay := float64(w.cfg.InitialProducts) / 365 * 0.6
	n := r.Poisson(perDay * share)
	dw := make([]float64, len(w.depts))
	for i, d := range w.depts {
		f := 1.0
		if ls, ok := launchSeason[d.name]; ok {
			f = ls[int(w.now.Month())-1]
		}
		dens := 1.0
		if len(d.leaves) > 0 {
			dens = d.leaves[0].p.Density
		}
		dw[i] = d.weight * dens * f
	}
	for i := 0; i < n; i++ {
		d := w.depts[r.WeightedIndex(dw)]
		lw := make([]float64, len(d.leaves))
		for j, l := range d.leaves {
			lw[j] = l.p.Weight
		}
		l := d.leaves[r.WeightedIndex(lw)]
		launch := w.now.Add(-time.Duration(r.Range(0, 3600)) * time.Second)
		var pred *domain.Product
		if l.p.Life == "tech" && r.Bool(0.3) {
			for _, cand := range l.products {
				if cand.Status == "active" && cand.Traits.Gen > 0 && cand.Traits.Decline == 0 && w.now.Sub(cand.LaunchedAt) > 200*24*time.Hour && r.Bool(0.3) {
					pred = cand
					break
				}
			}
		}
		var p *domain.Product
		if pred != nil {
			p = w.newProduct(l, launch, pred.Brand, nil, pred)
			pred.Traits.Decline = 0.5
			pred.SuccessorID = p.ID
			pred.UpdatedAt = w.now
			w.uow.product(pred)
			for _, v := range pred.Variants {
				for _, o := range v.Offers {
					if o.Status == "active" && o.PromoID == 0 {
						nb := domain.PsychPrice(o.BasePrice.Float()*r.Range(0.8, 0.9), domain.RoundCents99)
						o.BasePrice = nb
						w.changePrice(o, nb, "successor_launch")
					}
				}
			}
		} else {
			p = w.newProduct(l, launch, nil, nil, nil)
		}
		for _, v := range p.Variants {
			for _, o := range v.Offers {
				w.initStock(o, l, r)
			}
		}
	}
}

func (w *World) discontinue() {
	r := w.rng
	for _, pid := range w.sortedProductIDs() {
		p := w.products[pid]
		l := w.leafByCat[p.CategoryID]
		age := w.now.Sub(p.LaunchedAt).Hours() / 24
		switch p.Status {
		case "active":
			lf := lifeFactor(l.life, p.Traits.LifeScale, age) * (1 - p.Traits.Decline)
			if lf < l.life.Floor*1.3 && age > (l.life.RampDays+l.life.PlateauDays)*p.Traits.LifeScale && r.Bool(0.01) {
				p.Status = "discontinued"
				p.DiscontinuedAt = w.now
				p.UpdatedAt = w.now
				w.uow.product(p)
				for _, v := range p.Variants {
					for _, o := range v.Offers {
						if o.Fulfillment == domain.FulfilSeller && o.Status == "active" {
							o.Status = "deleted"
							o.UpdatedAt = w.now
							w.uow.offer(o)
						}
					}
				}
			}
		case "discontinued":
			left := 0
			for _, v := range p.Variants {
				for _, o := range v.Offers {
					if o.Status != "active" {
						continue
					}
					if o.Available() <= 0 {
						o.Status = "deleted"
						o.UpdatedAt = w.now
						w.uow.offer(o)
					} else {
						left++
					}
				}
			}
			if left == 0 && w.now.Sub(p.DiscontinuedAt) > 30*24*time.Hour {
				p.Status = "deleted"
				p.UpdatedAt = w.now
				w.uow.product(p)
			}
		}
	}
}

func (w *World) rebuildSellerIndex() {
	w.sellersByDept = make([][]*domain.Seller, len(w.depts))
	w.activeSellers = w.activeSellers[:0]
	for _, s := range w.sellers {
		if s.Type != "3p" || s.Status != "active" {
			continue
		}
		w.activeSellers = append(w.activeSellers, s)
		if s.Traits.Dept >= 0 && s.Traits.Dept < len(w.depts) {
			w.sellersByDept[s.Traits.Dept] = append(w.sellersByDept[s.Traits.Dept], s)
		}
	}
}

func (w *World) sellerLife() {
	r := w.rng
	changed := false
	// onboarding of new merchants
	growth := 0.35 * (1 + w.daysSinceStart()/730)
	for i, n := 0, r.Poisson(growth); i < n; i++ {
		s := w.newSeller(w.now, "onboarding")
		s.Traits.ChurnAt = w.now.Add(time.Duration(r.Range(3, 14)*24) * time.Hour).Unix() // activation date
		changed = true
	}
	for _, s := range w.sellers {
		if s.Type != "3p" {
			continue
		}
		switch s.Status {
		case "onboarding":
			if w.now.Unix() >= s.Traits.ChurnAt {
				s.Status = "active"
				s.UpdatedAt = w.now
				s.Traits.ChurnAt = w.now.Add(time.Duration(r.Exp(900)*24) * time.Hour).Unix()
				w.uow.seller(s)
				w.rebuildSellerIndex()
				w.listForSeller(s)
				changed = true
			}
		case "active":
			hazard := 1.0 / 2500
			if s.Traits.Quality < 0.4 {
				hazard *= 3
			}
			if r.Bool(hazard) {
				w.closeSeller(s, "closed")
				changed = true
			}
		case "suspended":
			if w.now.Unix() >= s.Traits.ChurnAt {
				if r.Bool(0.6) {
					s.Status = "active"
					s.UpdatedAt = w.now
					for _, o := range w.offersOf(s) {
						if o.Status == "paused" {
							o.Status = "active"
							o.UpdatedAt = w.now
							w.uow.offer(o)
						}
					}
					w.uow.seller(s)
				} else {
					w.closeSeller(s, "closed")
				}
				changed = true
			}
		}
	}
	// weekly performance review
	if w.now.Weekday() == time.Monday {
		for _, s := range w.sellers {
			if s.Type != "3p" || s.Status != "active" {
				continue
			}
			total := s.Shipped + s.Cancelled
			rate := 0.0
			if total > 0 {
				rate = float64(s.Cancelled) / float64(total)
			}
			rating := r.Clamp(2.2+2.8*s.Traits.Quality-8*rate+r.Norm(0, 0.1), 1, 5)
			s.Rating = math.Round(rating*100) / 100
			s.UpdatedAt = w.now
			w.uow.seller(s)
			if total > 30 && (rate > 0.07 && r.Bool(0.6) || s.Rating < 2.8 && r.Bool(0.2)) {
				s.Status = "suspended"
				s.Traits.ChurnAt = w.now.Add(time.Duration(r.Range(14, 60)*24) * time.Hour).Unix()
				for _, o := range w.offersOf(s) {
					if o.Status == "active" {
						o.Status = "paused"
						o.UpdatedAt = w.now
						w.uow.offer(o)
					}
				}
				w.note("seller suspended: %s (cancel rate %.1f%%, rating %.2f)", s.Name, rate*100, s.Rating)
				changed = true
			}
			s.Shipped, s.Cancelled = s.Shipped/2, s.Cancelled/2
		}
	}
	if changed {
		w.rebuildSellerIndex()
	}
}

func (w *World) offersOf(s *domain.Seller) []*domain.Offer {
	var out []*domain.Offer
	for _, o := range w.offers {
		if o.SellerID == s.ID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (w *World) closeSeller(s *domain.Seller, status string) {
	s.Status = status
	s.ClosedAt = w.now
	s.UpdatedAt = w.now
	w.uow.seller(s)
	for _, o := range w.offersOf(s) {
		if o.Status != "deleted" {
			o.Status = "deleted"
			o.UpdatedAt = w.now
			w.uow.offer(o)
		}
	}
	w.note("seller left the marketplace: %s", s.Name)
}

// listForSeller lists offers on existing products and launches own products.
func (w *World) listForSeller(s *domain.Seller) {
	r := w.rng
	d := w.depts[s.Traits.Dept]
	n := r.IntRange(3, 20)
	for i := 0; i < n; i++ {
		lw := make([]float64, len(d.leaves))
		for j, l := range d.leaves {
			lw[j] = l.p.Weight
		}
		l := d.leaves[r.WeightedIndex(lw)]
		if r.Bool(0.5) && competitiveDept[d.name] {
			p := l.pickProduct(r)
			if p == nil || p.Status != "active" || (p.Brand != nil && p.Brand.PrivateLabel) {
				continue
			}
			for _, v := range p.Variants {
				if len(v.Offers) == 0 {
					continue
				}
				ref := v.Offers[0]
				w.ids.Offer++
				o := &domain.Offer{
					ID: w.ids.Offer, VariantID: v.ID, SellerID: s.ID, Variant: v, Seller: s, Status: "active",
					CreatedAt: w.now, UpdatedAt: w.now, ListPrice: ref.ListPrice, Cost: ref.Cost, HandlingDays: 1,
				}
				o.Price = domain.PsychPrice(ref.BasePrice.Float()*r.Range(0.93, 1.1), domain.RoundCents99)
				o.BasePrice = o.Price
				if r.Bool(s.Traits.PlatformShare) {
					o.Fulfillment = domain.FulfilPlatform
					w.placeStock(o, r)
					w.initStock(o, l, r)
				} else {
					o.Fulfillment = domain.FulfilSeller
					o.StockQty = r.IntRange(3, 50)
					o.HandlingDays = int(math.Max(1, math.Round(s.Traits.HandlingDays)))
				}
				v.Offers = append(v.Offers, o)
				w.offers[o.ID] = o
				w.uow.addOffer(o)
			}
		} else {
			p := w.newProduct(l, w.now, nil, s, nil)
			for _, v := range p.Variants {
				for _, o := range v.Offers {
					w.initStock(o, l, r)
				}
			}
		}
	}
}

func (w *World) customerLife() {
	r := w.rng
	n := len(w.customers)
	if n == 0 {
		return
	}
	pick := func() *domain.Customer {
		for i := 0; i < 5; i++ {
			c := w.customers[r.IntN(n)]
			if c != nil && c.Status == domain.CustActive && !c.Has(domain.FlagTest) {
				return c
			}
		}
		return nil
	}
	// moving house
	for i, k := 0, r.Poisson(float64(n)*0.08/365); i < k; i++ {
		c := pick()
		if c == nil {
			continue
		}
		m := w.marketOf(c)
		pr := w.newProspect(m, w.now)
		w.ids.Address++
		a := pr.Addr
		a.ID, a.CustomerID, a.CreatedAt = w.ids.Address, c.ID, w.now
		a.Recipient = "" // filled by the adapter from the customer row
		w.uow.addAddress(&a)
		c.AddressID = a.ID
		w.touchCustomer(c)
	}
	// email / phone change
	for i, k := 0, r.Poisson(float64(n)*0.03/365); i < k; i++ {
		c := pick()
		if c == nil {
			continue
		}
		m := w.marketOf(c)
		pr := w.newProspect(m, w.now)
		ch := domain.CustomerPII{CustomerID: c.ID, At: w.now}
		if r.Bool(0.6) {
			ch.Email = pr.Profile.Email
		} else {
			ph := w.phone(m, r)
			ch.Phone = &ph
		}
		w.uow.addPII(ch)
		w.touchCustomer(c)
	}
	// newsletter opt-in/out
	for i, k := 0, r.Poisson(float64(n)*0.05/365); i < k; i++ {
		c := pick()
		if c == nil {
			continue
		}
		c.Flags ^= domain.FlagOptIn
		w.touchCustomer(c)
	}
	// GDPR erasure
	for i, k := 0, r.Poisson(float64(n)*0.003/365); i < k; i++ {
		c := pick()
		if c == nil {
			continue
		}
		c.Status = domain.CustDeleted
		w.updateWeight(c)
		w.uow.addPII(domain.CustomerPII{CustomerID: c.ID, Erase: true, At: w.now})
		w.touchCustomer(c)
	}
}

func (w *World) touchCustomer(c *domain.Customer) {
	if w.bump("customers") {
		c.UpdatedAt = w.now.Unix()
	}
	w.uow.cust(c)
}

func (w *World) loyaltyTiers() {
	now := w.now.Unix()
	changed := 0
	for _, c := range w.customers {
		if c == nil || c.Status != domain.CustActive {
			continue
		}
		spent := float64(c.Spent)
		if c.SpentAt > 0 {
			spent *= math.Exp(-float64(now-c.SpentAt) / (180 * 86400))
		}
		var t uint8
		switch {
		case spent >= 3000:
			t = 3
		case spent >= 1200:
			t = 2
		case spent >= 400:
			t = 1
		}
		if t != c.Tier {
			c.Tier = t
			w.touchCustomer(c)
			changed++
		}
	}
	w.note("loyalty tiers recalculated: %d customers changed tier", changed)
}

func (w *World) winback() {
	r := w.rng
	n := 0
	for _, c := range w.customers {
		if c == nil || c.Active || c.Status != domain.CustActive || !c.Has(domain.FlagOptIn) || !r.Bool(0.02) {
			continue
		}
		c.Active = true
		c.ChurnAt = w.now.Add(time.Duration(r.Exp(200)*24) * time.Hour).Unix()
		w.updateWeight(c)
		w.uow.custTraits(c)
		n++
	}
	if n > 0 {
		w.note("win-back campaign reactivated %d customers", n)
	}
}
