package app

import (
	"math"
	"sort"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

var supplierSuffix = map[string]string{
	"DE": "GmbH", "AT": "GmbH", "NL": "B.V.", "BE": "BV", "FR": "SAS", "IT": "S.r.l.", "ES": "S.L.", "PT": "Lda.",
	"IE": "Ltd", "PL": "Sp. z o.o.", "CZ": "s.r.o.", "SE": "AB", "DK": "A/S", "FI": "Oy", "MD": "SRL",
	"CN": "Co., Ltd.", "GB": "Ltd", "US": "Inc.", "JP": "K.K.", "KR": "Co., Ltd.", "TW": "Co., Ltd.", "NO": "AS", "CH": "AG",
}

// supplierFor returns (creating lazily) the distributor of a brand.
func (w *World) supplierFor(b *domain.Brand) *domain.Supplier {
	if b == nil {
		b = w.brands[0]
	}
	if s, ok := w.suppliers[b.ID]; ok {
		return s
	}
	r := w.rng
	w.ids.Supplier++
	name := b.Name + " Europe " + supplierSuffix["NL"]
	if b.PrivateLabel {
		name = Pick(r, []string{"Shenzhen Hengfeng Manufacturing", "Ningbo Ocean Trading", "Guangzhou Meilong Industrial", "Porto Têxteis", "Łódź Textile Works", "Bursa Tekstil"}) + " " + Pick(r, []string{"Co., Ltd.", "Lda.", "Sp. z o.o.", "A.Ş."})
	} else if b.Country != "" {
		if sfx, ok := supplierSuffix[b.Country]; ok && r.Bool(0.5) {
			name = b.Name + " " + sfx
		}
	}
	lead := r.LogNorm(9, 0.4)
	if b.Country == "CN" || b.PrivateLabel {
		lead = r.LogNorm(24, 0.3)
	}
	s := &domain.Supplier{ID: int32(w.ids.Supplier), BrandID: b.ID, Name: name, Country: b.Country, LeadDays: math.Round(lead*10) / 10, Reliability: r.Range(0.75, 0.98), CreatedAt: w.now}
	w.suppliers[b.ID] = s
	w.uow.b.Suppliers = append(w.uow.b.Suppliers, s)
	return s
}

// updateDemandEMA folds yesterday's sales into per-offer demand estimates.
func (w *World) updateDemandEMA() {
	for _, o := range w.offers {
		if o.Fulfillment != domain.FulfilPlatform {
			continue
		}
		sold := float64(w.st.offerSales[o.ID])
		o.DemandEMA = 0.85*o.DemandEMA + 0.15*sold
	}
	w.st.offerSales = map[int64]int{}
}

// reorder creates purchase orders for low platform stock.
func (w *World) reorder() {
	r := w.rng
	type key struct {
		seller   int32
		supplier int32
		wh       int16
	}
	groups := map[key]*domain.PurchaseOrder{}
	preseason := w.now.Month() == time.October || w.now.Month() == time.November
	// deterministic iteration
	ids := make([]int64, 0, len(w.offers))
	for id, o := range w.offers {
		if o.Fulfillment == domain.FulfilPlatform && o.Status != "deleted" {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		o := w.offers[id]
		p := o.Variant.Product
		if p.Status != "active" {
			continue
		}
		for _, sl := range o.Stock {
			avail := sl.OnHand - sl.Reserved
			if sl.OpenPO != 0 || avail > sl.ReorderPoint {
				continue
			}
			d := o.DemandEMA / float64(len(o.Stock))
			if d < 0.03 && avail > 0 {
				continue
			}
			var sup *domain.Supplier
			k := key{seller: o.SellerID, wh: sl.WarehouseID}
			lead := 7.0
			if o.SellerID == w.ownSeller.ID {
				sup = w.supplierFor(p.Brand)
				k.supplier = sup.ID
				lead = sup.LeadDays
			} else {
				lead = r.Range(4, 12)
			}
			cover := r.Range(21, 35)
			if preseason {
				for _, t := range w.leafByCat[p.CategoryID].p.Tags {
					if t == "gift" {
						cover *= 1.6
						break
					}
				}
			}
			target := math.Max(d, 0.05) * (lead + cover)
			qty := int(math.Ceil(target)) - avail
			if qty <= 0 {
				continue
			}
			pack := 1
			switch pr := o.Price.Float(); {
			case pr < 10:
				pack = 12
			case pr < 40:
				pack = 6
			case pr < 150:
				pack = 2
			}
			qty = (qty + pack - 1) / pack * pack
			po, ok := groups[k]
			if !ok {
				w.ids.PO++
				exp := w.now.Add(time.Duration(lead*24) * time.Hour)
				po = &domain.PurchaseOrder{ID: w.ids.PO, SellerID: k.seller, SupplierID: k.supplier, WarehouseID: k.wh, Status: "placed", OrderedAt: w.now, ExpectedAt: exp, UpdatedAt: w.now}
				po.NextStep, po.NextAt = "confirm", w.now.Add(time.Duration(r.Range(4, 40))*time.Hour)
				groups[k] = po
			}
			w.ids.POItem++
			po.Items = append(po.Items, &domain.POItem{ID: w.ids.POItem, POID: po.ID, OfferID: o.ID, QtyOrdered: qty, UnitCost: o.Cost})
			sl.OpenPO = po.ID
			sl.ReorderPoint = int(math.Max(1, math.Ceil(d*(lead+7))))
		}
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return groups[keys[i]].ID < groups[keys[j]].ID })
	for _, k := range keys {
		po := groups[k]
		w.pos[po.ID] = po
		w.q.Push(po.NextAt, evPO, po, po.NextAt.UnixNano())
		w.uow.po(po)
	}
}

func (w *World) onPO(ev event) {
	po := ev.ref.(*domain.PurchaseOrder)
	if po.NextAt.UnixNano() != ev.aux || po.NextAt.IsZero() {
		return
	}
	r := w.rng
	step := po.NextStep
	po.NextStep, po.NextAt = "", time.Time{}
	po.UpdatedAt = w.now
	rel := 0.9
	if s := w.supplierByID(po.SupplierID); s != nil {
		rel = s.Reliability
	}
	switch step {
	case "confirm":
		if r.Bool(0.01) {
			po.Status = "cancelled"
			w.clearOpenPO(po)
			break
		}
		po.Status = "confirmed"
		shipAt := po.ExpectedAt.Add(-time.Duration(r.Range(2, 5)*24) * time.Hour)
		if !shipAt.After(w.now) {
			shipAt = w.now.Add(time.Duration(r.Range(6, 30)) * time.Hour)
		}
		po.NextStep, po.NextAt = "ship", shipAt
	case "ship":
		po.Status = "shipped"
		arrive := po.ExpectedAt
		if !r.Bool(rel) {
			arrive = arrive.Add(time.Duration(r.Range(1, 10)*24) * time.Hour)
		}
		if !arrive.After(w.now) {
			arrive = w.now.Add(time.Duration(r.Range(12, 48)) * time.Hour)
		}
		po.NextStep, po.NextAt = "receive", arrive
	case "receive":
		po.Status = "received"
		po.ReceivedAt = w.now
		for _, it := range po.Items {
			q := it.QtyOrdered
			if r.Bool(0.05) {
				q = int(math.Max(1, math.Round(float64(q)*r.Range(0.6, 0.95))))
			}
			it.QtyReceived = q
			o := w.offers[it.OfferID]
			if o == nil {
				continue
			}
			for _, sl := range o.Stock {
				if sl.WarehouseID == po.WarehouseID {
					sl.OnHand += q
					sl.OpenPO = 0
					sl.UpdatedAt = w.now
					w.uow.stockLevel(sl)
					w.ids.StockMove++
					w.uow.addMove(&domain.StockMovement{ID: w.ids.StockMove, WarehouseID: sl.WarehouseID, OfferID: o.ID, Delta: q, Reason: "inbound", RefType: "purchase_order", RefID: po.ID, CreatedAt: w.now})
				}
			}
		}
	}
	if !po.NextAt.IsZero() {
		w.q.Push(po.NextAt, evPO, po, po.NextAt.UnixNano())
	} else {
		delete(w.pos, po.ID)
	}
	w.uow.po(po)
}

func (w *World) clearOpenPO(po *domain.PurchaseOrder) {
	for _, it := range po.Items {
		if o := w.offers[it.OfferID]; o != nil {
			for _, sl := range o.Stock {
				if sl.OpenPO == po.ID {
					sl.OpenPO = 0
				}
			}
		}
	}
}

func (w *World) supplierByID(id int32) *domain.Supplier {
	if id == 0 {
		return nil
	}
	for _, s := range w.suppliers {
		if s.ID == id {
			return s
		}
	}
	return nil
}
