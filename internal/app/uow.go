package app

import (
	"encoding/json"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
	"github.com/mymdz/lumo-market/internal/ports"
)

// uow accumulates changes between checkpoints.
//
// Write policy:
//   - inserts of facts are streamed at every checkpoint;
//   - updates of mutable entities are coalesced; in live mode they are flushed
//     at every checkpoint, during backfill only at the final checkpoint;
//   - aggregates with a lifecycle (orders, carts, purchase orders) are written
//     as they change in live mode, but only once settled during backfill.
type uow struct {
	w *World
	b *ports.Batch

	orders  map[int64]*domain.Order
	carts   map[int64]*cartState
	pos     map[int64]*domain.PurchaseOrder
	offers  map[int64]*domain.Offer
	stock   map[*domain.StockLevel]struct{}
	custs   map[int64]*domain.Customer
	traits  map[int64]*domain.Customer
	prods   map[int64]*domain.Product
	ptraits map[int64]*domain.Product
	sellers map[int32]*domain.Seller
	coupons map[int32]*domain.Coupon
	otraits map[int64]*domain.Offer

	newOffers   map[int64]bool
	newProducts map[int64]bool
	newCusts    map[int64]bool
	newSellers  map[int32]bool
	newCoupons  map[int32]bool
	newStock    map[*domain.StockLevel]bool
}

func newUOW(w *World) *uow {
	u := &uow{w: w}
	u.reset()
	u.orders = map[int64]*domain.Order{}
	u.carts = map[int64]*cartState{}
	u.pos = map[int64]*domain.PurchaseOrder{}
	u.resetDirty()
	return u
}

func (u *uow) reset() {
	u.b = &ports.Batch{}
	u.newOffers = map[int64]bool{}
	u.newProducts = map[int64]bool{}
	u.newCusts = map[int64]bool{}
	u.newSellers = map[int32]bool{}
	u.newCoupons = map[int32]bool{}
	u.newStock = map[*domain.StockLevel]bool{}
}

func (u *uow) resetDirty() {
	u.offers = map[int64]*domain.Offer{}
	u.stock = map[*domain.StockLevel]struct{}{}
	u.custs = map[int64]*domain.Customer{}
	u.traits = map[int64]*domain.Customer{}
	u.prods = map[int64]*domain.Product{}
	u.ptraits = map[int64]*domain.Product{}
	u.sellers = map[int32]*domain.Seller{}
	u.coupons = map[int32]*domain.Coupon{}
	u.otraits = map[int64]*domain.Offer{}
}

// ---- inserts ----

func (u *uow) addProduct(p *domain.Product) {
	u.b.NewProducts = append(u.b.NewProducts, p)
	u.newProducts[p.ID] = true
	u.ptraits[p.ID] = p
	for _, v := range p.Variants {
		u.b.NewVariants = append(u.b.NewVariants, v)
	}
}

func (u *uow) addOffer(o *domain.Offer) {
	u.b.NewOffers = append(u.b.NewOffers, o)
	u.newOffers[o.ID] = true
	u.otraits[o.ID] = o
	for _, s := range o.Stock {
		u.b.NewStock = append(u.b.NewStock, s)
		u.newStock[s] = true
	}
}

func (u *uow) addStock(s *domain.StockLevel) {
	u.b.NewStock = append(u.b.NewStock, s)
	u.newStock[s] = true
}

func (u *uow) addSeller(s *domain.Seller) {
	u.b.NewSellers = append(u.b.NewSellers, s)
	u.newSellers[s.ID] = true
}

func (u *uow) addCustomer(p *domain.CustomerProfile) {
	u.b.NewCustomers = append(u.b.NewCustomers, p)
	u.newCusts[p.C.ID] = true
	u.traits[p.C.ID] = p.C
}

func (u *uow) addAddress(a *domain.Address) { u.b.NewAddresses = append(u.b.NewAddresses, a) }

func (u *uow) addCoupon(c *domain.Coupon) {
	u.b.NewCoupons = append(u.b.NewCoupons, c)
	u.newCoupons[c.ID] = true
}

func (u *uow) addPriceChange(pc *domain.PriceChange) { u.b.PriceChanges = append(u.b.PriceChanges, pc) }

func (u *uow) addMove(m *domain.StockMovement) { u.b.StockMoves = append(u.b.StockMoves, m) }

func (u *uow) addReview(r *domain.Review) { u.b.Reviews = append(u.b.Reviews, r) }

func (u *uow) addPII(p domain.CustomerPII) { u.b.CustomerPII = append(u.b.CustomerPII, p) }

// ---- updates ----

func (u *uow) offer(o *domain.Offer) {
	u.offers[o.ID] = o
	u.otraits[o.ID] = o
}

func (u *uow) stockLevel(s *domain.StockLevel) { u.stock[s] = struct{}{} }

func (u *uow) cust(c *domain.Customer) {
	u.custs[c.ID] = c
	u.traits[c.ID] = c
}

func (u *uow) custTraits(c *domain.Customer) { u.traits[c.ID] = c }

func (u *uow) product(p *domain.Product) {
	u.prods[p.ID] = p
	u.ptraits[p.ID] = p
}

func (u *uow) productTraits(p *domain.Product) { u.ptraits[p.ID] = p }

func (u *uow) seller(s *domain.Seller) { u.sellers[s.ID] = s }

func (u *uow) coupon(c *domain.Coupon) { u.coupons[c.ID] = c }

// ---- aggregates ----

func (u *uow) order(o *domain.Order) { u.orders[o.ID] = o }

func (u *uow) cart(c *cartState) { u.carts[c.Cart.ID] = c }

func (u *uow) po(p *domain.PurchaseOrder) { u.pos[p.ID] = p }

// take builds the batch for a checkpoint. final=true at the end of backfill.
func (u *uow) take(final bool) *ports.Batch {
	w := u.w
	b := u.b
	b.At = w.now
	b.Phase = w.phase
	live := w.live()
	horizonCarts := w.horizon.Add(-30 * 24 * time.Hour)

	// orders
	for id, o := range u.orders {
		settled := o.Sim.Settled
		if !live && !final && !settled {
			continue
		}
		delete(u.orders, id)
		if !o.Sim.Persisted {
			b.Orders = append(b.Orders, o)
		} else {
			ch := &ports.OrderChange{Order: o, Root: o.Sim.Dirty}
			hasNew := false
			for _, it := range o.Items {
				if it.Dirty {
					ch.Items = append(ch.Items, it)
				}
			}
			for _, p := range o.Payments {
				if !p.Persisted {
					hasNew = true
				} else if p.Dirty {
					ch.Payments = append(ch.Payments, p)
				}
			}
			for _, s := range o.Shipments {
				if !s.Persisted {
					hasNew = true
				} else if s.Dirty {
					ch.Shipments = append(ch.Shipments, s)
				}
			}
			for _, r := range o.Returns {
				if !r.Persisted {
					hasNew = true
				} else if r.Dirty {
					ch.Returns = append(ch.Returns, r)
				}
			}
			for _, r := range o.Refunds {
				if !r.Persisted {
					hasNew = true
				}
			}
			for _, h := range o.History {
				if !h.Persisted {
					hasNew = true
				}
			}
			if hasNew {
				b.Orders = append(b.Orders, o)
			}
			if ch.Root || len(ch.Items)+len(ch.Payments)+len(ch.Shipments)+len(ch.Returns) > 0 {
				b.OrderChanges = append(b.OrderChanges, ch)
			}
		}
		if live || final {
			if settled {
				b.PendingDeletes = append(b.PendingDeletes, ports.PendingKey{Kind: "order", ID: o.ID})
			} else if blob, err := json.Marshal(o); err == nil {
				b.PendingUpserts = append(b.PendingUpserts, ports.PendingBlob{Kind: "order", ID: o.ID, DueAt: o.Sim.NextAt, Data: blob})
			}
		}
	}

	// carts
	for id, cs := range u.carts {
		c := cs.Cart
		pending := !c.NextAt.IsZero()
		if !live && !final && pending {
			continue
		}
		delete(u.carts, id)
		if !live && c.CreatedAt.Before(horizonCarts) {
			continue // would have been purged already
		}
		if !c.Persisted {
			b.NewCarts = append(b.NewCarts, c)
			b.NewCartItems = append(b.NewCartItems, c.Items...)
		} else {
			b.CartUpdates = append(b.CartUpdates, c)
		}
		if live || final {
			if !pending {
				b.PendingDeletes = append(b.PendingDeletes, ports.PendingKey{Kind: "cart", ID: c.ID})
			} else if blob, err := json.Marshal(cs); err == nil {
				b.PendingUpserts = append(b.PendingUpserts, ports.PendingBlob{Kind: "cart", ID: c.ID, DueAt: c.NextAt, Data: blob})
			}
		}
	}

	// purchase orders
	for id, p := range u.pos {
		done := p.NextAt.IsZero()
		if !live && !final && !done {
			continue
		}
		delete(u.pos, id)
		if !u.poPersisted(p) {
			b.NewPOs = append(b.NewPOs, p)
		} else {
			b.POUpdates = append(b.POUpdates, p)
		}
		if live || final {
			if done {
				b.PendingDeletes = append(b.PendingDeletes, ports.PendingKey{Kind: "po", ID: p.ID})
			} else if blob, err := json.Marshal(p); err == nil {
				b.PendingUpserts = append(b.PendingUpserts, ports.PendingBlob{Kind: "po", ID: p.ID, DueAt: p.NextAt, Data: blob})
			}
		}
	}

	if live || final {
		for id, o := range u.offers {
			if !u.newOffers[id] {
				b.OfferUpdates = append(b.OfferUpdates, o)
			}
		}
		for s := range u.stock {
			if !u.newStock[s] {
				b.StockUpdates = append(b.StockUpdates, s)
			}
		}
		for id, c := range u.custs {
			if !u.newCusts[id] {
				b.CustomerUpdates = append(b.CustomerUpdates, c)
			}
		}
		for id, p := range u.prods {
			if !u.newProducts[id] {
				b.ProductUpdates = append(b.ProductUpdates, p)
			}
		}
		for id, s := range u.sellers {
			if !u.newSellers[id] {
				b.SellerUpdates = append(b.SellerUpdates, s)
			}
		}
		for id, c := range u.coupons {
			if !u.newCoupons[id] {
				b.CouponUpdates = append(b.CouponUpdates, c)
			}
		}
		for _, c := range u.traits {
			b.CustomerTraits = append(b.CustomerTraits, c)
		}
		for _, p := range u.ptraits {
			b.ProductTraits = append(b.ProductTraits, p)
		}
		for _, o := range u.otraits {
			b.OfferTraits = append(b.OfferTraits, o)
		}
		if final {
			b.SellerTraits = append(b.SellerTraits, w.sellers...)
		} else {
			for _, s := range u.sellers {
				b.SellerTraits = append(b.SellerTraits, s)
			}
		}
		u.resetDirty()
	}

	b.SimTime = w.now
	b.Horizon = w.horizon
	b.Start = w.start
	b.Seed = w.cfg.Seed
	b.StateJSON = w.stateJSON()
	u.reset()
	return b
}

// poPersisted: purchase orders are inserted on their first write; we keep the
// set of written ids in the world to decide between insert and update.
func (u *uow) poPersisted(p *domain.PurchaseOrder) bool { return u.w.poWritten[p.ID] }

// afterApply marks everything in the batch as persisted.
func (u *uow) afterApply(b *ports.Batch) {
	for _, o := range b.Orders {
		o.Sim.Persisted = true
		o.Sim.Dirty = false
		for _, it := range o.Items {
			it.Dirty = false
		}
		for _, p := range o.Payments {
			if !p.VisibleAt.After(b.At) {
				p.Persisted = true
			}
			p.Dirty = false
		}
		for _, s := range o.Shipments {
			s.Persisted, s.Dirty = true, false
		}
		for _, r := range o.Returns {
			r.Persisted, r.Dirty = true, false
		}
		for _, r := range o.Refunds {
			r.Persisted = true
		}
		for _, h := range o.History {
			h.Persisted = true
		}
	}
	for _, ch := range b.OrderChanges {
		ch.Order.Sim.Dirty = false
		for _, it := range ch.Items {
			it.Dirty = false
		}
		for _, p := range ch.Payments {
			p.Dirty = false
		}
		for _, s := range ch.Shipments {
			s.Dirty = false
		}
		for _, r := range ch.Returns {
			r.Dirty = false
		}
	}
	for _, c := range b.NewCarts {
		c.Persisted = true
	}
	for _, p := range b.NewPOs {
		if !p.NextAt.IsZero() {
			u.w.poWritten[p.ID] = true
		}
	}
	for _, p := range b.POUpdates {
		if p.NextAt.IsZero() {
			delete(u.w.poWritten, p.ID)
		}
	}
}
