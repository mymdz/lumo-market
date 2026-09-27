package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
	"github.com/mymdz/lumo-market/internal/ports"
)

// restore rebuilds the in-memory world from a database snapshot.
func (w *World) restore(s *ports.Snapshot) error {
	w.start, w.horizon = s.Meta.Start, s.Meta.Horizon
	w.now = s.Meta.SimTime
	w.phase = PhaseLive
	if err := w.initReference(); err != nil {
		return err
	}
	// warehouses
	w.whByID = map[int16]*domain.Warehouse{}
	specByCode := map[string]domain.WarehouseSpec{}
	for _, ws := range w.ref.Geo.Warehouses {
		specByCode[ws.Code] = ws
	}
	sort.Slice(s.Warehouses, func(i, j int) bool { return s.Warehouses[i].ID < s.Warehouses[j].ID })
	for _, wh := range s.Warehouses {
		loc, err := time.LoadLocation(wh.TZName)
		if err != nil {
			return err
		}
		wh.TZ = loc
		if sp, ok := specByCode[wh.Code]; ok {
			wh.Lat, wh.Lon = sp.Lat, sp.Lon
		}
		w.warehouses = append(w.warehouses, wh)
		w.whByID[wh.ID] = wh
	}
	if err := w.loadState(s.Meta.StateJSON); err != nil {
		return fmt.Errorf("state: %w", err)
	}
	w.initCatalogTree(s.Categories)
	w.initBrands(s.Brands)

	// sellers
	w.sellerByID = map[int32]*domain.Seller{}
	sort.Slice(s.Sellers, func(i, j int) bool { return s.Sellers[i].ID < s.Sellers[j].ID })
	for _, sl := range s.Sellers {
		w.sellers = append(w.sellers, sl)
		w.sellerByID[sl.ID] = sl
		if sl.Type == "1p" && w.ownSeller == nil {
			w.ownSeller = sl
		}
	}
	w.rebuildSellerIndex()
	for _, sup := range s.Suppliers {
		w.suppliers[sup.BrandID] = sup
	}

	// catalog
	sort.Slice(s.Products, func(i, j int) bool { return s.Products[i].ID < s.Products[j].ID })
	for _, p := range s.Products {
		p.Cat = w.catByID[p.CategoryID]
		p.Brand = w.brandByID[p.BrandID]
		l := w.leafByCat[p.CategoryID]
		if p.Cat == nil || l == nil {
			continue
		}
		w.products[p.ID] = p
		if p.Status != "deleted" {
			l.products = append(l.products, p)
		}
	}
	sort.Slice(s.Variants, func(i, j int) bool { return s.Variants[i].ID < s.Variants[j].ID })
	for _, v := range s.Variants {
		p := w.products[v.ProductID]
		if p == nil {
			continue
		}
		v.Product = p
		v.PriceFactor, v.Demand = 1, 1
		for axis, val := range v.Attrs {
			ax, ok := w.ref.Catalog.Axes[axis]
			if !ok {
				continue
			}
			for i, x := range ax.Values {
				if x == val {
					if len(ax.Weights) > i {
						v.Demand *= ax.Weights[i]
					}
					if len(ax.Price) > i {
						v.PriceFactor *= ax.Price[i]
					}
				}
			}
		}
		p.Variants = append(p.Variants, v)
		w.variants[v.ID] = v
	}
	sort.Slice(s.Offers, func(i, j int) bool { return s.Offers[i].ID < s.Offers[j].ID })
	for _, o := range s.Offers {
		v := w.variants[o.VariantID]
		if v == nil {
			continue
		}
		o.Variant = v
		o.Seller = w.sellerByID[o.SellerID]
		v.Offers = append(v.Offers, o)
		w.offers[o.ID] = o
	}
	for _, sl := range s.Stock {
		if o := w.offers[sl.OfferID]; o != nil {
			o.Stock = append(o.Stock, sl)
		}
	}

	// customers
	sort.Slice(s.Customers, func(i, j int) bool { return s.Customers[i].ID < s.Customers[j].ID })
	for _, c := range s.Customers {
		w.addCustomer(c)
	}

	// marketing
	w.campaigns = s.Campaigns
	w.coupons = s.Coupons
	for _, c := range w.coupons {
		w.couponByCode[c.Code] = c
	}
	w.cal = newCalendar(w)
	years := map[int]bool{}
	for _, c := range w.campaigns {
		years[int(c.ID)/100] = true
		c.Started = !w.now.Before(c.StartsAt)
		c.Ended = !w.now.Before(c.EndsAt)
	}
	for y := range years {
		yc := w.cal.year(y)
		// restore runtime attributes of campaigns from the deterministic plan
		for _, plan := range yc.campaigns {
			for _, c := range w.campaigns {
				if c.ID == plan.ID {
					c.Traffic, c.Tags, c.Depts, c.Countries, c.Share, c.BrandID, c.AcqBoost = plan.Traffic, plan.Tags, plan.Depts, plan.Countries, plan.Share, plan.BrandID, plan.AcqBoost
				}
			}
		}
		for _, plan := range yc.coupons {
			if c := w.couponByCode[plan.Code]; c != nil && c.ID == plan.ID {
				c.Kind, c.Countries = plan.Kind, plan.Countries
			}
		}
	}
	for _, c := range w.coupons {
		if c.Kind == "" {
			switch c.Code {
			case "WELCOME10":
				c.Kind = "welcome"
			case "GOLD10":
				c.Kind = "loyalty"
			case "COMEBACK15":
				c.Kind = "winback"
			default:
				c.Kind = "newsletter"
			}
		}
	}
	for _, id := range s.Migrations {
		w.applied[id] = true
		w.appliedIDs = append(w.appliedIDs, id)
	}

	// in-flight aggregates
	for _, pb := range s.Pending {
		switch pb.Kind {
		case "order":
			var o domain.Order
			if err := json.Unmarshal(pb.Data, &o); err != nil {
				return fmt.Errorf("pending order %d: %w", pb.ID, err)
			}
			op := &o
			op.Sim.Persisted = true
			markPersisted(op)
			w.orders[op.ID] = op
			if !op.Sim.NextAt.IsZero() {
				w.q.Push(op.Sim.NextAt, evOrder, op, op.Sim.NextAt.UnixNano())
			}
			for _, p := range op.Payments {
				if p.VisibleAt.After(w.now) {
					p.Persisted = false
					w.late = append(w.late, lateTouch{at: p.VisibleAt, o: op})
				}
			}
		case "cart":
			var cs cartState
			if err := json.Unmarshal(pb.Data, &cs); err != nil {
				return fmt.Errorf("pending cart %d: %w", pb.ID, err)
			}
			cs.Cart.Persisted = true
			w.carts[cs.Cart.ID] = &cs
			if !cs.Cart.NextAt.IsZero() {
				w.q.Push(cs.Cart.NextAt, evCart, &cs, cs.Cart.NextAt.UnixNano())
			}
		case "po":
			var po domain.PurchaseOrder
			if err := json.Unmarshal(pb.Data, &po); err != nil {
				return fmt.Errorf("pending po %d: %w", pb.ID, err)
			}
			pp := &po
			w.pos[pp.ID] = pp
			w.poWritten[pp.ID] = true
			for _, it := range pp.Items {
				if o := w.offers[it.OfferID]; o != nil {
					for _, sl := range o.Stock {
						if sl.WarehouseID == pp.WarehouseID {
							sl.OpenPO = pp.ID
						}
					}
				}
			}
			if !pp.NextAt.IsZero() {
				w.q.Push(pp.NextAt, evPO, pp, pp.NextAt.UnixNano())
			}
		}
	}
	// consumable re-purchases
	for _, c := range w.customers {
		if c == nil {
			continue
		}
		for _, rp := range c.Repl {
			at := time.Unix(rp.DueAt, 0)
			if at.Before(w.now) {
				at = w.now.Add(time.Duration(w.rng.Range(0, 48)) * time.Hour)
			}
			w.q.Push(at, evReplenish, c, rp.OfferID)
		}
	}
	w.refreshDemand()
	return nil
}

func markPersisted(o *domain.Order) {
	for _, p := range o.Payments {
		p.Persisted = true
	}
	for _, s := range o.Shipments {
		s.Persisted = true
	}
	for _, r := range o.Returns {
		r.Persisted = true
	}
	for _, r := range o.Refunds {
		r.Persisted = true
	}
	for _, h := range o.History {
		h.Persisted = true
	}
}
