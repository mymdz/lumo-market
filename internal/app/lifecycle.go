package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

var countryCoords = map[string][2]float64{
	"CN": {22.5, 114.1}, "GB": {52.5, -1.5}, "US": {40.7, -74}, "JP": {35.7, 139.7}, "KR": {37.5, 127},
	"TW": {25, 121.5}, "NO": {59.9, 10.7}, "CH": {47.2, 8.3},
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	toRad := math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLon := (lon2 - lon1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * R * math.Asin(math.Sqrt(a))
}

func (w *World) coords(country string) (float64, float64) {
	if m, ok := w.marketByCode[country]; ok {
		return m.spec.Lat, m.spec.Lon
	}
	if c, ok := countryCoords[country]; ok {
		return c[0], c[1]
	}
	return 50, 10
}

// transitDays estimates carrier transit time between an origin and a market.
func (w *World) transitDays(lat, lon float64, origin string, m *market, carrier string) float64 {
	d := haversine(lat, lon, m.spec.Lat, m.spec.Lon)
	days := 1 + d/650
	if origin == m.Code {
		days = 1
	}
	if !m.EU {
		days += 2 // customs
	}
	if origin == "CN" {
		days += 7
	}
	if cs, ok := w.carriers[carrier]; ok && cs.Speed > 0 {
		days *= cs.Speed
	}
	return days
}

// ---------- building ----------

func (w *World) buildShipments(o *domain.Order, m *market) {
	r := w.rng
	type key struct {
		seller int32
		wh     int16
	}
	groups := map[key]*domain.Shipment{}
	var order []key
	for _, it := range o.Items {
		off := w.offers[it.OfferID]
		wh := w.reserve(off, it.Qty, m)
		k := key{seller: 0, wh: wh}
		if wh == 0 {
			k.seller = off.SellerID
		}
		s, ok := groups[k]
		if !ok {
			w.ids.Shipment++
			s = &domain.Shipment{ID: w.ids.Shipment, OrderID: o.ID, SellerID: off.SellerID, WarehouseID: wh, Status: domain.ShipPending, Method: o.ShipMethod, CreatedAt: w.now, UpdatedAt: w.now}
			if wh != 0 {
				s.SellerID = w.ownSeller.ID
				if off.SellerID != w.ownSeller.ID {
					s.SellerID = off.SellerID
				}
			}
			groups[k] = s
			order = append(order, k)
		}
		it.ShipmentIdx = len(order) - 1
		for i, kk := range order {
			if kk == k {
				it.ShipmentIdx = i
			}
		}
	}
	for _, k := range order {
		s := groups[k]
		if s.Method == "locker" {
			s.Carrier = m.lockers.Pick(r)
		} else {
			s.Carrier = m.carriers.Pick(r)
		}
		var lat, lon float64
		origin := ""
		handling := 1.0
		if s.WarehouseID != 0 {
			wh := w.whByID[s.WarehouseID]
			lat, lon, origin = wh.Lat, wh.Lon, wh.Country
		} else {
			sel := w.sellerByID[s.SellerID]
			lat, lon = w.coords(sel.Country)
			origin = sel.Country
			handling = math.Max(1, sel.Traits.HandlingDays)
		}
		s.TransitD = w.transitDays(lat, lon, origin, m, s.Carrier)
		promise := handling + math.Ceil(s.TransitD) + 1
		if s.Method == "express" {
			promise = math.Max(1, promise-1)
		}
		s.PromisedAt = dayOf(w.now.Add(time.Duration(promise*24) * time.Hour)).Add(20 * time.Hour)
		if w.applied["m001_shipments_co2"] {
			s.CO2Grams = int(80 + haversine(lat, lon, m.spec.Lat, m.spec.Lon)*0.35*r.Range(0.8, 1.2))
		}
		o.Shipments = append(o.Shipments, s)
	}
}

func (w *World) setStatus(o *domain.Order, from, to, actor string) {
	if from == "" && len(o.History) > 0 {
		from = o.Status
	}
	o.Status = to
	if w.bump("orders") {
		o.UpdatedAt = w.now
	}
	o.Sim.Dirty = true
	w.ids.History++
	h := &domain.StatusChange{ID: w.ids.History, OrderID: o.ID, From: from, To: to, Actor: actor, ChangedAt: w.now}
	o.History = append(o.History, h)
	if v, ok := w.cal.incidentFactor(w.now, "duplicate_events", ""); ok && w.rng.Bool(v) || w.dirty(0.002) {
		w.ids.History++
		d := *h
		d.ID = w.ids.History
		o.History = append(o.History, &d)
	}
}

func (w *World) newPayment(o *domain.Order, method, status string) *domain.Payment {
	w.ids.Payment++
	p := &domain.Payment{
		ID: w.ids.Payment, OrderID: o.ID, Method: method, Provider: providerFor(method, w.rng), Status: status,
		Amount: o.Total, Currency: o.Currency, CreatedAt: w.now, UpdatedAt: w.now, VisibleAt: w.now,
	}
	if v, ok := w.cal.incidentFactor(w.now, "late_webhooks", ""); ok {
		p.VisibleAt = w.now.Add(time.Duration(w.rng.Range(10, v)) * time.Minute)
	} else if w.dirty(0.01) {
		p.VisibleAt = w.now.Add(time.Duration(w.rng.Range(5, 120)) * time.Minute)
	}
	if p.VisibleAt.After(w.now) {
		w.late = append(w.late, lateTouch{at: p.VisibleAt, o: o})
	}
	o.Payments = append(o.Payments, p)
	return p
}

func (w *World) lastPayment(o *domain.Order) *domain.Payment {
	if len(o.Payments) == 0 {
		return nil
	}
	return o.Payments[len(o.Payments)-1]
}

func (w *World) touchPayment(p *domain.Payment) {
	if w.bump("payments") {
		p.UpdatedAt = w.now
	}
	p.Dirty = true
}

func (w *World) touchShipment(s *domain.Shipment) {
	if w.bump("shipments") {
		s.UpdatedAt = w.now
	}
	s.Dirty = true
}

// ---------- scheduling ----------

func (w *World) scheduleOrder(o *domain.Order) {
	var next time.Time
	consider := func(t time.Time) {
		if !t.IsZero() && (next.IsZero() || t.Before(next)) {
			next = t
		}
	}
	if o.Sim.Step != "" {
		consider(o.Sim.StepAt)
	}
	for _, s := range o.Shipments {
		if s.NextStep != "" {
			consider(s.NextAt)
		}
	}
	for _, r := range o.Returns {
		if r.NextStep != "" {
			consider(r.NextAt)
		}
	}
	for _, it := range o.Items {
		consider(it.ReturnAt)
		consider(it.ReviewAt)
	}
	w.uow.order(o)
	if next.IsZero() {
		o.Sim.Settled = true
		o.Sim.NextAt = time.Time{}
		delete(w.orders, o.ID)
		return
	}
	if !next.Equal(o.Sim.NextAt) {
		o.Sim.NextAt = next
		w.q.Push(next, evOrder, o, next.UnixNano())
	}
}

func (w *World) onOrder(ev event) {
	o := ev.ref.(*domain.Order)
	if o.Sim.Settled || o.Sim.NextAt.UnixNano() != ev.aux {
		return
	}
	now := w.now
	for guard := 0; guard < 20; guard++ {
		progressed := false
		if o.Sim.Step != "" && !o.Sim.StepAt.After(now) {
			step := o.Sim.Step
			o.Sim.Step, o.Sim.StepAt = "", time.Time{}
			w.orderStep(o, step)
			progressed = true
		}
		for _, s := range o.Shipments {
			if s.NextStep != "" && !s.NextAt.After(now) {
				step := s.NextStep
				s.NextStep, s.NextAt = "", time.Time{}
				w.shipmentStep(o, s, step)
				progressed = true
			}
		}
		for _, r := range o.Returns {
			if r.NextStep != "" && !r.NextAt.After(now) {
				step := r.NextStep
				r.NextStep, r.NextAt = "", time.Time{}
				w.returnStep(o, r, step)
				progressed = true
			}
		}
		for _, it := range o.Items {
			if !it.ReturnAt.IsZero() && !it.ReturnAt.After(now) {
				it.ReturnAt = time.Time{}
				w.requestReturn(o, it)
				progressed = true
			}
			if !it.ReviewAt.IsZero() && !it.ReviewAt.After(now) {
				it.ReviewAt = time.Time{}
				w.writeReview(o, it)
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	o.Sim.NextAt = time.Time{}
	w.scheduleOrder(o)
}

// ---------- order-level steps ----------

var payFailRate = map[string]float64{
	"card": 0.05, "klarna": 0.08, "paypal": 0.015, "apple_pay": 0.02, "google_pay": 0.02, "ideal": 0.02,
	"blik": 0.025, "sepa_debit": 0.03, "bancontact": 0.02,
}

func failureReason(method string, outage bool, r *Rand) string {
	if outage {
		return "provider_unavailable"
	}
	switch method {
	case "card", "apple_pay", "google_pay":
		return Pick(r, []string{"insufficient_funds", "card_declined", "card_declined", "3ds_authentication_failed", "expired_card", "do_not_honor"})
	case "klarna":
		return Pick(r, []string{"credit_check_failed", "risk_rejected"})
	default:
		return Pick(r, []string{"timeout", "cancelled_by_user", "cancelled_by_user", "provider_error"})
	}
}

func (w *World) orderStep(o *domain.Order, step string) {
	r := w.rng
	m := w.marketByCode[o.Country]
	switch step {
	case "pay", "pay_retry":
		if o.Status != domain.OrderPendingPayment {
			return
		}
		p := w.lastPayment(o)
		if step == "pay_retry" {
			method := p.Method
			if r.Bool(0.5) {
				method = m.payments.Pick(r)
			}
			p = w.newPayment(o, method, domain.PayPending)
		}
		fail, ok := payFailRate[p.Method]
		if !ok {
			fail = 0.03
		}
		outage := false
		if v, on := w.cal.incidentFactor(w.now, "payment_outage", p.Method); on {
			fail, outage = v, true
		}
		if o.Sim.Fraud && r.Bool(0.3) {
			fail = 0.6
		}
		o.Sim.PayTries++
		if r.Bool(fail) {
			p.Status = domain.PayFailed
			p.FailureReason = failureReason(p.Method, outage, r)
			w.touchPayment(p)
			if o.Sim.PayTries < 3 && r.Bool(0.55) {
				o.Sim.Step, o.Sim.StepAt = "pay_retry", w.now.Add(time.Duration(r.Range(1, 8))*time.Minute)
			} else {
				o.Sim.Step, o.Sim.StepAt = "cancel_unpaid", w.now.Add(30*time.Minute)
			}
			return
		}
		if redirectMethods[p.Method] || p.Method == "bank_transfer" {
			p.Status = domain.PayCaptured
			p.CapturedAt = w.now
		} else {
			p.Status = domain.PayAuthorized
		}
		w.touchPayment(p)
		o.PaidAt = w.now
		w.setStatus(o, domain.OrderPendingPayment, domain.OrderPaid, "system")
		o.Sim.Step, o.Sim.StepAt = "fraud_check", w.now.Add(time.Duration(r.Range(1, 12))*time.Minute)
	case "cancel_unpaid", "bank_timeout":
		if o.Status != domain.OrderPendingPayment {
			return
		}
		reason := "payment_failed"
		if step == "bank_timeout" {
			reason = "payment_timeout"
			if p := w.lastPayment(o); p != nil && p.Status == domain.PayPending {
				p.Status = domain.PayFailed
				p.FailureReason = "expired"
				w.touchPayment(p)
			}
		}
		w.cancelOrder(o, reason, "system")
	case "fraud_check":
		if o.Status != domain.OrderPaid {
			return
		}
		if o.Sim.Fraud && r.Bool(0.55) {
			w.cancelOrder(o, "fraud_suspected", "system")
			if c := w.customer(o.CustomerID); c != nil {
				c.Status = domain.CustBlocked
				c.UpdatedAt = w.now.Unix()
				w.updateWeight(c)
				w.uow.cust(c)
			}
			return
		}
		w.setStatus(o, domain.OrderPaid, domain.OrderProcessing, "system")
		w.startFulfilment(o)
		if r.Bool(0.025) {
			o.Sim.Step, o.Sim.StepAt = "customer_cancel", w.now.Add(time.Duration(r.Range(10, 14*60))*time.Minute)
		}
	case "customer_cancel":
		if o.Status != domain.OrderProcessing {
			return
		}
		for _, s := range o.Shipments {
			if s.Status != domain.ShipPending && s.Status != domain.ShipPacked {
				return // too late
			}
		}
		w.cancelOrder(o, "customer_request", "customer")
	case "chargeback":
		for _, p := range o.Payments {
			if p.Status == domain.PayCaptured || p.Status == domain.PayPartiallyRefunded {
				amt := p.Amount - p.RefundedAmount
				p.Status = domain.PayChargeback
				w.touchPayment(p)
				w.addRefund(o, p, 0, amt, "chargeback")
				break
			}
		}
	}
}

func (w *World) startFulfilment(o *domain.Order) {
	r := w.rng
	for _, s := range o.Shipments {
		if s.Status != domain.ShipPending {
			continue
		}
		if s.WarehouseID != 0 {
			wh := w.whByID[s.WarehouseID]
			at := w.warehouseSlot(w.now, wh)
			if v, ok := w.cal.incidentFactor(w.now, "warehouse_delay", wh.Code); ok {
				at = at.Add(time.Duration(v*24) * time.Hour)
			}
			if w.peak(w.now) && r.Bool(0.4) {
				at = at.Add(24 * time.Hour)
			}
			s.NextStep, s.NextAt = "pack", at
		} else {
			sel := w.sellerByID[s.SellerID]
			days := math.Max(0.3, sel.Traits.HandlingDays*r.LogNorm(1, 0.4))
			if r.Bool(sel.Traits.LateRate) {
				days += r.Range(1, 4)
			}
			at := addBusinessDays(w.now, days, time.UTC)
			// the seller may not be able to fulfil
			cancel := sel.Traits.CancelRate
			for _, it := range o.Items {
				if o.Shipments[it.ShipmentIdx] == s {
					if off := w.offers[it.OfferID]; off != nil && off.StockQty < 0 {
						cancel = math.Max(cancel, 0.7)
					}
				}
			}
			if r.Bool(cancel) {
				s.NextStep, s.NextAt = "seller_cancel", w.now.Add(time.Duration(r.Range(2, 36))*time.Hour)
			} else {
				s.NextStep, s.NextAt = "pack", at
			}
		}
	}
	// pricing errors get cancelled by the merchant
	for _, it := range o.Items {
		if off := w.offers[it.OfferID]; off != nil && off.BasePrice > 0 && float64(off.Price) < 0.2*float64(off.BasePrice) && r.Bool(0.75) {
			for _, s := range o.Shipments {
				if o.Shipments[it.ShipmentIdx] == s {
					s.NextStep, s.NextAt = "pricing_cancel", w.now.Add(time.Duration(r.Range(1, 20))*time.Hour)
				}
			}
		}
	}
}

func (w *World) peak(t time.Time) bool {
	if t.Month() == time.December && t.Day() <= 22 {
		return true
	}
	bf := blackFriday(t.Year())
	return !t.Before(bf.AddDate(0, 0, -4)) && t.Before(bf.AddDate(0, 0, 5))
}

// warehouseSlot returns when an order paid at t gets packed.
func (w *World) warehouseSlot(t time.Time, wh *domain.Warehouse) time.Time {
	r := w.rng
	lt := t.In(wh.TZ)
	cutoff := 14
	if lt.Weekday() == time.Saturday {
		cutoff = 10
	}
	sameDay := lt.Weekday() != time.Sunday && lt.Hour() < cutoff && !w.cal.isHoliday(wh.Country, lt)
	day := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, wh.TZ)
	if !sameDay {
		day = day.AddDate(0, 0, 1)
		for day.Weekday() == time.Sunday || w.cal.isHoliday(wh.Country, day) {
			day = day.AddDate(0, 0, 1)
		}
		return day.Add(time.Duration(r.Range(8, 16)*60) * time.Minute)
	}
	start := math.Max(float64(lt.Hour())+0.5, 11)
	return day.Add(time.Duration(r.Range(start, float64(cutoff)+4)*60) * time.Minute)
}

// addBusinessDays adds fractional days skipping Sundays.
func addBusinessDays(t time.Time, days float64, loc *time.Location) time.Time {
	whole := int(days)
	frac := days - float64(whole)
	d := t
	for i := 0; i < whole; i++ {
		d = d.AddDate(0, 0, 1)
		if d.In(loc).Weekday() == time.Sunday {
			d = d.AddDate(0, 0, 1)
		}
	}
	return d.Add(time.Duration(frac * 24 * float64(time.Hour)))
}

func (w *World) cancelOrder(o *domain.Order, reason, actor string) {
	if o.Status == domain.OrderCancelled {
		return
	}
	prev := o.Status
	o.CancelledAt = w.now
	o.CancelReason = reason
	for _, s := range o.Shipments {
		if s.Status == domain.ShipPending || s.Status == domain.ShipPacked {
			s.Status = domain.ShipCancelled
			s.NextStep, s.NextAt = "", time.Time{}
			w.touchShipment(s)
		}
	}
	for _, it := range o.Items {
		if it.Status == "ordered" {
			it.Status = "cancelled"
			it.Dirty = true
			w.release(o, it)
		}
	}
	for _, p := range o.Payments {
		switch p.Status {
		case domain.PayAuthorized:
			p.Status = domain.PayVoided
			w.touchPayment(p)
		case domain.PayCaptured:
			w.refundPayment(o, p, p.Amount-p.RefundedAmount, 0, "order_cancelled")
		case domain.PayPending:
			if p.Method == "cod" {
				p.Status = domain.PayVoided
				w.touchPayment(p)
			}
		}
	}
	w.setStatus(o, prev, domain.OrderCancelled, actor)
	o.Sim.Step = ""
	if c := w.customer(o.CustomerID); c != nil && (reason == "seller_cancelled" || reason == "pricing_error") {
		w.hurt(c, 0.35)
	}
}

// release returns reserved stock of a cancelled item.
func (w *World) release(o *domain.Order, it *domain.OrderItem) {
	off := w.offers[it.OfferID]
	if off == nil {
		return
	}
	if off.Fulfillment == domain.FulfilSeller {
		off.StockQty += it.Qty
		w.uow.offer(off)
		return
	}
	s := o.Shipments[it.ShipmentIdx]
	for _, sl := range off.Stock {
		if sl.WarehouseID == s.WarehouseID {
			sl.Reserved -= it.Qty
			if sl.Reserved < 0 {
				sl.Reserved = 0
			}
			sl.UpdatedAt = w.now
			w.uow.stockLevel(sl)
		}
	}
}

func (w *World) addRefund(o *domain.Order, p *domain.Payment, returnID int64, amt domain.Money, reason string) {
	w.ids.Refund++
	o.Refunds = append(o.Refunds, &domain.Refund{ID: w.ids.Refund, PaymentID: p.ID, OrderID: o.ID, ReturnID: returnID, Amount: amt, Reason: reason, CreatedAt: w.now})
}

func (w *World) refundPayment(o *domain.Order, p *domain.Payment, amt domain.Money, returnID int64, reason string) {
	if amt <= 0 {
		return
	}
	if p.RefundedAmount+amt > p.Amount {
		amt = p.Amount - p.RefundedAmount
	}
	p.RefundedAmount += amt
	if p.RefundedAmount >= p.Amount {
		p.Status = domain.PayRefunded
	} else {
		p.Status = domain.PayPartiallyRefunded
	}
	w.touchPayment(p)
	w.addRefund(o, p, returnID, amt, reason)
}

func (w *World) capturedPayment(o *domain.Order) *domain.Payment {
	for i := len(o.Payments) - 1; i >= 0; i-- {
		p := o.Payments[i]
		switch p.Status {
		case domain.PayCaptured, domain.PayPartiallyRefunded, domain.PayAuthorized:
			return p
		}
	}
	return nil
}

// hurt makes a customer more likely to churn after a bad experience.
func (w *World) hurt(c *domain.Customer, p float64) {
	c.Satisfaction = float32(math.Max(-1, float64(c.Satisfaction)-0.3))
	if w.rng.Bool(p) {
		alt := w.now.Add(time.Duration(w.rng.Exp(90)*24) * time.Hour).Unix()
		if alt < c.ChurnAt {
			c.ChurnAt = alt
		}
	}
	w.uow.custTraits(c)
}

// ---------- shipments ----------

func (w *World) shipmentStep(o *domain.Order, s *domain.Shipment, step string) {
	r := w.rng
	m := w.marketByCode[o.Country]
	if o.Status == domain.OrderCancelled && step != "rts_done" {
		return
	}
	switch step {
	case "pack":
		s.Status = domain.ShipPacked
		w.touchShipment(s)
		s.NextStep, s.NextAt = "ship", w.now.Add(time.Duration(r.Range(1, 5)*60)*time.Minute)
	case "ship":
		s.Status = domain.ShipShipped
		s.ShippedAt = w.now
		cs := w.carriers[s.Carrier]
		s.Tracking = fmt.Sprintf("%s%d", cs.Prefix, 100000000+r.Int64N(899999999))
		w.touchShipment(s)
		for _, it := range o.Items {
			if o.Shipments[it.ShipmentIdx] == s && it.Status == "ordered" {
				it.Status = "shipped"
				it.Dirty = true
				w.consumeStock(o, it, s)
			}
		}
		if p := w.capturedPayment(o); p != nil && p.Status == domain.PayAuthorized {
			p.Status = domain.PayCaptured
			p.CapturedAt = w.now
			w.touchPayment(p)
		}
		if sel := w.sellerByID[s.SellerID]; sel != nil {
			sel.Shipped++
		}
		s.NextStep, s.NextAt = "transit", w.now.Add(time.Duration(r.Range(6, 14))*time.Hour)
		w.recompute(o)
	case "transit":
		s.Status = domain.ShipInTransit
		w.touchShipment(s)
		cs := w.carriers[s.Carrier]
		days := s.TransitD * r.LogNorm(1, 0.25)
		late := cs.Late
		if w.peak(w.now) {
			late *= 1.8
		}
		if r.Bool(late) {
			days += r.Range(1, 4)
		}
		if v, ok := w.cal.incidentFactor(w.now, "carrier_strike", s.Carrier); ok {
			days += v
		}
		if s.Method == "express" {
			days = math.Max(0.6, days*0.6)
		}
		deliver := addBusinessDays(s.ShippedAt, days, m.TZ)
		lt := deliver.In(m.TZ)
		deliver = time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, m.TZ).Add(time.Duration(r.Range(9, 20)*60) * time.Minute)
		if !deliver.After(w.now) {
			deliver = w.now.Add(time.Duration(r.Range(2, 10)) * time.Hour)
		}
		pay := w.lastPayment(o)
		switch {
		case r.Bool(cs.Lost):
			s.NextStep, s.NextAt = "lost", deliver.Add(time.Duration(r.Range(5, 10)*24)*time.Hour)
		case pay != nil && pay.Method == "cod" && r.Bool(0.05):
			s.NextStep, s.NextAt = "rts", deliver.Add(time.Duration(r.Range(3, 8)*24)*time.Hour)
		default:
			s.NextStep, s.NextAt = "deliver", deliver
		}
	case "deliver":
		s.Status = domain.ShipDelivered
		s.DeliveredAt = w.now
		w.touchShipment(s)
		late := w.now.Sub(s.PromisedAt) > 24*time.Hour
		if late {
			o.Sim.Late = true
		}
		if pay := w.lastPayment(o); pay != nil && pay.Method == "cod" && pay.Status == domain.PayPending {
			pay.Status = domain.PayCaptured
			pay.CapturedAt = w.now
			w.touchPayment(pay)
			if !o.PaidAt.After(time.Time{}) {
				o.PaidAt = w.now
			}
		}
		c := w.customer(o.CustomerID)
		for _, it := range o.Items {
			if o.Shipments[it.ShipmentIdx] != s || it.Status != "shipped" {
				continue
			}
			it.Status = "delivered"
			it.Dirty = true
			w.planAfterDelivery(o, it, c, late)
		}
		if c != nil && late {
			w.hurt(c, 0.25)
		}
		w.recompute(o)
		if o.Sim.Fraud && o.Status == domain.OrderDelivered && r.Bool(0.8) {
			o.Sim.Step, o.Sim.StepAt = "chargeback", w.now.Add(time.Duration(r.Range(20, 60)*24)*time.Hour)
		}
	case "lost":
		s.Status = domain.ShipLost
		w.touchShipment(s)
		var amt domain.Money
		for _, it := range o.Items {
			if o.Shipments[it.ShipmentIdx] == s && it.Status == "shipped" {
				it.Status = "lost"
				it.Dirty = true
				amt += it.LineTotal
			}
		}
		if p := w.capturedPayment(o); p != nil {
			w.refundPayment(o, p, amt, 0, "parcel_lost")
		}
		if c := w.customer(o.CustomerID); c != nil {
			w.hurt(c, 0.5)
		}
		w.recompute(o)
	case "rts":
		s.Status = domain.ShipReturned
		w.touchShipment(s)
		for _, it := range o.Items {
			if o.Shipments[it.ShipmentIdx] == s && it.Status == "shipped" {
				it.Status = "cancelled"
				it.Dirty = true
				w.restock(o, it, s, it.Qty, "return")
			}
		}
		if p := w.lastPayment(o); p != nil && p.Status == domain.PayPending {
			p.Status = domain.PayFailed
			p.FailureReason = "cod_refused"
			w.touchPayment(p)
		}
		w.cancelOrder(o, "cod_refused", "carrier")
	case "seller_cancel", "pricing_cancel":
		if s.Status != domain.ShipPending && s.Status != domain.ShipPacked {
			return
		}
		s.Status = domain.ShipCancelled
		w.touchShipment(s)
		var amt domain.Money
		for _, it := range o.Items {
			if o.Shipments[it.ShipmentIdx] == s && it.Status == "ordered" {
				it.Status = "cancelled"
				it.Dirty = true
				amt += it.LineTotal
				w.release(o, it)
			}
		}
		if sel := w.sellerByID[s.SellerID]; sel != nil {
			sel.Cancelled++
		}
		reason := "seller_cancelled"
		if step == "pricing_cancel" {
			reason = "pricing_error"
		}
		allCancelled := true
		for _, x := range o.Shipments {
			if x.Status != domain.ShipCancelled {
				allCancelled = false
			}
		}
		if allCancelled {
			w.cancelOrder(o, reason, "seller")
			return
		}
		if p := w.capturedPayment(o); p != nil {
			if p.Status == domain.PayAuthorized {
				p.Amount -= amt
				w.touchPayment(p)
			} else {
				w.refundPayment(o, p, amt, 0, reason)
			}
		}
		if c := w.customer(o.CustomerID); c != nil {
			w.hurt(c, 0.3)
		}
		w.recompute(o)
	}
}

func (w *World) consumeStock(o *domain.Order, it *domain.OrderItem, s *domain.Shipment) {
	off := w.offers[it.OfferID]
	if off == nil || off.Fulfillment != domain.FulfilPlatform {
		return
	}
	for _, sl := range off.Stock {
		if sl.WarehouseID == s.WarehouseID {
			sl.OnHand -= it.Qty
			sl.Reserved -= it.Qty
			if sl.Reserved < 0 {
				sl.Reserved = 0
			}
			if sl.OnHand < 0 && !w.dirty(0.5) {
				sl.OnHand = 0
			}
			sl.UpdatedAt = w.now
			w.uow.stockLevel(sl)
			w.ids.StockMove++
			w.uow.addMove(&domain.StockMovement{ID: w.ids.StockMove, WarehouseID: sl.WarehouseID, OfferID: off.ID, Delta: -it.Qty, Reason: "sale", RefType: "order", RefID: o.ID, CreatedAt: w.now})
			return
		}
	}
}

func (w *World) restock(o *domain.Order, it *domain.OrderItem, s *domain.Shipment, qty int, reason string) {
	off := w.offers[it.OfferID]
	if off == nil {
		return
	}
	if off.Fulfillment == domain.FulfilSeller {
		if reason == "return" {
			off.StockQty += qty
			w.uow.offer(off)
		}
		return
	}
	whID := s.WarehouseID
	for _, sl := range off.Stock {
		if sl.WarehouseID == whID {
			if reason == "return" {
				sl.OnHand += qty
			}
			sl.UpdatedAt = w.now
			w.uow.stockLevel(sl)
			w.ids.StockMove++
			delta := qty
			if reason == "damaged" {
				delta = 0
			}
			w.uow.addMove(&domain.StockMovement{ID: w.ids.StockMove, WarehouseID: whID, OfferID: off.ID, Delta: delta, Reason: reason, RefType: "order", RefID: o.ID, CreatedAt: w.now})
			return
		}
	}
}

// recompute derives the order status from its shipments and items.
func (w *World) recompute(o *domain.Order) {
	if o.Status == domain.OrderCancelled || o.Status == domain.OrderPendingPayment || o.Status == domain.OrderPaid {
		return
	}
	var pending, moving, done, total int
	for _, s := range o.Shipments {
		switch s.Status {
		case domain.ShipCancelled:
			continue
		case domain.ShipPending, domain.ShipPacked:
			pending++
		case domain.ShipShipped, domain.ShipInTransit:
			moving++
		default:
			done++
		}
		total++
	}
	status := domain.OrderProcessing
	switch {
	case total == 0:
		return
	case done == total:
		status = domain.OrderDelivered
	case pending > 0 && (moving > 0 || done > 0):
		status = domain.OrderPartiallyShipped
	case pending == 0:
		status = domain.OrderShipped
	}
	if status == domain.OrderDelivered {
		delivered, returned := 0, 0
		for _, it := range o.Items {
			if it.Status == "delivered" || it.Status == "returned" {
				delivered += it.Qty
				returned += it.ReturnedQty
			}
		}
		if returned > 0 {
			status = domain.OrderPartiallyReturned
			if returned >= delivered {
				status = domain.OrderReturned
			}
		}
	}
	if status != o.Status {
		w.setStatus(o, o.Status, status, "system")
	}
}

// ---------- after delivery: returns and reviews ----------

var returnReasons = map[string][]string{
	"fashion": {"wrong_size", "wrong_size", "wrong_size", "not_as_described", "changed_mind", "changed_mind", "quality_issue", "damaged"},
	"tech":    {"defective", "defective", "changed_mind", "not_as_described", "damaged", "better_price"},
	"default": {"changed_mind", "changed_mind", "not_as_described", "damaged", "defective", "late_delivery", "better_price"},
}

func (w *World) planAfterDelivery(o *domain.Order, it *domain.OrderItem, c *domain.Customer, late bool) {
	r := w.rng
	off := w.offers[it.OfferID]
	if off == nil {
		return
	}
	p := off.Variant.Product
	l := w.leafByCat[p.CategoryID]
	pr := l.p.Returns * (1.6 - p.Traits.Quality)
	if c != nil {
		pr *= float64(c.ReturnProp)
	}
	if late {
		pr *= 1.4
	}
	if it.Bracket {
		pr = 0.92
	}
	if o.Sim.Fraud || o.Sim.Test {
		pr = 0
	}
	if r.Bool(math.Min(0.95, pr)) {
		days := math.Min(29, r.LogNorm(4, 0.7))
		it.ReturnAt = w.now.Add(time.Duration(days*24) * time.Hour)
		return
	}
	if c == nil {
		return
	}
	prob := 0.07
	switch c.Segment {
	case domain.SegLoyal, domain.SegTech:
		prob = 0.11
	case domain.SegOccasional:
		prob = 0.04
	}
	if p.Traits.Quality < 0.3 || late {
		prob *= 1.5
	}
	if r.Bool(prob) {
		it.ReviewAt = w.now.Add(time.Duration(r.Range(2, 21)*24) * time.Hour)
	}
}

func (w *World) requestReturn(o *domain.Order, it *domain.OrderItem) {
	r := w.rng
	if it.Status != "delivered" {
		return
	}
	off := w.offers[it.OfferID]
	style := "default"
	if off != nil {
		l := w.leafByCat[off.Variant.Product.CategoryID]
		switch {
		case strings.Contains(l.dept.name, "Fashion") || l.p.Style == "shoes":
			style = "fashion"
		case l.p.Style == "tech" || l.p.Style == "appliance":
			style = "tech"
		}
	}
	reason := Pick(r, returnReasons[style])
	if o.Sim.Late && r.Bool(0.3) {
		reason = "late_delivery"
	}
	w.ids.Return++
	s := o.Shipments[it.ShipmentIdx]
	ret := &domain.Return{
		ID: w.ids.Return, OrderID: o.ID, OrderItemID: it.ID, Qty: it.Qty, Reason: reason, Status: domain.RetRequested,
		RequestedAt: w.now, UpdatedAt: w.now, WarehouseID: s.WarehouseID,
	}
	if it.Qty > 1 && r.Bool(0.4) {
		ret.Qty = r.IntRange(1, it.Qty-1)
	}
	ret.NextStep, ret.NextAt = "send", w.now.Add(time.Duration(r.LogNorm(1.5, 0.6)*24*float64(time.Hour)))
	o.Returns = append(o.Returns, ret)
}

func (w *World) returnStep(o *domain.Order, ret *domain.Return, step string) {
	r := w.rng
	var it *domain.OrderItem
	for _, x := range o.Items {
		if x.ID == ret.OrderItemID {
			it = x
		}
	}
	if it == nil {
		return
	}
	touch := func() {
		if w.bump("returns") {
			ret.UpdatedAt = w.now
		}
		ret.Dirty = true
	}
	switch step {
	case "send":
		ret.Status = domain.RetInTransit
		touch()
		ret.NextStep, ret.NextAt = "receive", w.now.Add(time.Duration(r.Range(2, 6)*24)*time.Hour)
	case "receive":
		touch()
		loc := time.UTC
		if wh := w.whByID[ret.WarehouseID]; wh != nil {
			loc = wh.TZ
		}
		ret.ReceivedAt = w.now.In(loc)
		if r.Bool(0.03) {
			ret.Status = domain.RetRejected
			return
		}
		ret.Status = domain.RetReceived
		s := o.Shipments[it.ShipmentIdx]
		if r.Bool(0.85) {
			w.restock(o, it, s, ret.Qty, "return")
		} else {
			w.restock(o, it, s, ret.Qty, "damaged")
		}
		ret.NextStep, ret.NextAt = "refund", w.now.Add(time.Duration(r.Range(2, 72))*time.Hour)
	case "refund":
		amt := domain.Money(math.Round(float64(it.LineTotal) * float64(ret.Qty) / float64(it.Qty)))
		ret.RefundAmt = amt
		ret.Status = domain.RetRefunded
		touch()
		if p := w.capturedPayment(o); p != nil {
			w.refundPayment(o, p, amt, ret.ID, "return")
		}
		it.ReturnedQty += ret.Qty
		if it.ReturnedQty >= it.Qty {
			it.Status = "returned"
		}
		it.Dirty = true
		w.recompute(o)
	}
}

func (w *World) writeReview(o *domain.Order, it *domain.OrderItem) {
	r := w.rng
	off := w.offers[it.OfferID]
	c := w.customer(o.CustomerID)
	if off == nil || c == nil {
		return
	}
	p := off.Variant.Product
	l := w.leafByCat[p.CategoryID]
	// J-shaped distribution: most reviews are 5 stars, angry 1-star reviews
	// form a second hump, the middle is thin.
	score := 4.35 + 2.0*(p.Traits.Quality-0.55) + r.Norm(0, 1.0)
	if o.Sim.Late {
		score -= 0.9
	}
	if it.ReturnedQty > 0 {
		score -= 1.5
	}
	rating := int(math.Round(r.Clamp(score, 1, 5)))
	angry := 0.06 * (1.4 - p.Traits.Quality)
	if o.Sim.Late || it.ReturnedQty > 0 {
		angry *= 2.5
	}
	if r.Bool(angry) {
		rating = 1
	}
	m := w.marketOf(c)
	lang := "en"
	body := ""
	title := Pick(r, w.ref.Texts.ReviewTitles[rating])
	if r.Bool(0.4) {
		lang = m.langs.Pick(r)
		if loc, ok := w.ref.Texts.ReviewLocal[lang]; ok {
			key := "neu"
			if rating >= 4 {
				key = "pos"
			} else if rating <= 2 {
				key = "neg"
			}
			body = Pick(r, loc[key])
			title = ""
		} else {
			lang = "en"
		}
	}
	if body == "" {
		body = Pick(r, w.ref.Texts.ReviewBodies[rating])
		if st, ok := w.ref.Texts.ReviewStyle[l.p.Style]; ok && r.Bool(0.6) {
			key := "pos"
			if rating <= 2 {
				key = "neg"
			}
			if rating != 3 {
				body += " " + Pick(r, st[key])
			}
		}
		if o.Sim.Late && r.Bool(0.5) {
			body += " " + Pick(r, w.ref.Texts.ReviewLate)
		}
	}
	w.ids.Review++
	rv := &domain.Review{
		ID: w.ids.Review, ProductID: p.ID, CustomerID: c.ID, OrderItemID: it.ID, Rating: rating,
		Title: title, Body: body, Language: lang, Verified: true, Status: "published",
		CreatedAt: w.now, UpdatedAt: w.now,
	}
	if r.Bool(0.03) {
		rv.Status = "rejected"
	}
	if w.applied["m006_reviews_media"] && r.Bool(0.15) {
		rv.MediaCount = r.IntRange(1, 4)
	}
	w.uow.addReview(rv)
	p.Traits.RatingSum += rating
	p.Traits.RatingCnt++
	w.uow.productTraits(p)
	if rating <= 2 {
		w.hurt(c, 0.2)
	}
}
