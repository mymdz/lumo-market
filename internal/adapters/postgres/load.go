package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mymdz/lumo-market/internal/domain"
	"github.com/mymdz/lumo-market/internal/ports"
)

func toMoney(n pgtype.Numeric) domain.Money {
	if !n.Valid {
		return 0
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return domain.MoneyFromFloat(f.Float64)
}

func tt(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

func sv(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func each(ctx context.Context, db *pgx.Conn, sql string, scan func(pgx.Rows) error) error {
	rows, err := db.Query(ctx, sql)
	if err != nil {
		return fmt.Errorf("%s: %w", short(sql), err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return fmt.Errorf("%s: %w", short(sql), err)
		}
	}
	return rows.Err()
}

// Load reads the full simulator world back from the database.
func (s *Store) Load(ctx context.Context) (*ports.Snapshot, error) {
	meta, err := s.Meta(ctx)
	if err != nil || meta == nil {
		return nil, fmt.Errorf("no simulator metadata: %v", err)
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	db := conn.Conn()
	snap := &ports.Snapshot{Meta: *meta}
	for id := range s.applied {
		snap.Migrations = append(snap.Migrations, id)
	}

	err = each(ctx, db, "SELECT id, COALESCE(parent_id, 0), name, slug, path, level, is_active, created_at, updated_at FROM shop.categories", func(r pgx.Rows) error {
		c := &domain.Category{}
		var lvl int16
		if err := r.Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Path, &lvl, &c.Active, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return err
		}
		c.Level = int(lvl)
		snap.Categories = append(snap.Categories, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, "SELECT id, name, tier, COALESCE(country, ''), is_private_label, created_at FROM shop.brands", func(r pgx.Rows) error {
		b := &domain.Brand{}
		var tier string
		if err := r.Scan(&b.ID, &b.Name, &tier, &b.Country, &b.PrivateLabel, &b.CreatedAt); err != nil {
			return err
		}
		b.Tier = domain.ParseTier(tier)
		snap.Brands = append(snap.Brands, b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, `SELECT s.id, s.name, s.legal_name, s.country, s.seller_type, s.status, COALESCE(s.rating, 0)::float8, s.joined_at, s.updated_at, s.closed_at,
		t.traits, COALESCE(t.shipped, 0), COALESCE(t.cancelled, 0) FROM shop.sellers s LEFT JOIN sim.seller_traits t ON t.seller_id = s.id`, func(r pgx.Rows) error {
		x := &domain.Seller{}
		var closed *time.Time
		var traits []byte
		if err := r.Scan(&x.ID, &x.Name, &x.LegalName, &x.Country, &x.Type, &x.Status, &x.Rating, &x.JoinedAt, &x.UpdatedAt, &closed, &traits, &x.Shipped, &x.Cancelled); err != nil {
			return err
		}
		x.ClosedAt = tt(closed)
		if len(traits) > 0 {
			_ = json.Unmarshal(traits, &x.Traits)
		}
		snap.Sellers = append(snap.Sellers, x)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, "SELECT id, code, name, country, city, timezone, opened_at FROM shop.warehouses", func(r pgx.Rows) error {
		w := &domain.Warehouse{}
		if err := r.Scan(&w.ID, &w.Code, &w.Name, &w.Country, &w.City, &w.TZName, &w.OpenedAt); err != nil {
			return err
		}
		w.OpenedAt = w.OpenedAt.UTC()
		snap.Warehouses = append(snap.Warehouses, w)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, "SELECT id, COALESCE(brand_id, 0), name, COALESCE(country, ''), lead_time_days::float8, created_at FROM shop.suppliers", func(r pgx.Rows) error {
		x := &domain.Supplier{}
		if err := r.Scan(&x.ID, &x.BrandID, &x.Name, &x.Country, &x.LeadDays, &x.CreatedAt); err != nil {
			return err
		}
		x.Reliability = 0.9
		snap.Suppliers = append(snap.Suppliers, x)
		return nil
	})
	if err != nil {
		return nil, err
	}

	eco := "NULL::text"
	if s.applied["m003_products_eco_score"] {
		eco = "p.eco_score"
	}
	err = each(ctx, db, `SELECT p.id, p.category_id, COALESCE(p.brand_id, 0), p.title, COALESCE(p.description, ''), p.status, COALESCE(p.weight_g, 0), p.attributes,
		COALESCE(p.successor_id, 0), p.launched_at, p.discontinued_at, p.created_at, p.updated_at, `+eco+`, t.traits
		FROM shop.products p LEFT JOIN sim.product_traits t ON t.product_id = p.id`, func(r pgx.Rows) error {
		p := &domain.Product{}
		var disc *time.Time
		var ecoS *string
		var attrs, traits []byte
		if err := r.Scan(&p.ID, &p.CategoryID, &p.BrandID, &p.Title, &p.Description, &p.Status, &p.WeightG, &attrs, &p.SuccessorID, &p.LaunchedAt, &disc, &p.CreatedAt, &p.UpdatedAt, &ecoS, &traits); err != nil {
			return err
		}
		p.DiscontinuedAt = tt(disc)
		p.LaunchedAt, p.CreatedAt, p.UpdatedAt = p.LaunchedAt.UTC(), p.CreatedAt.UTC(), p.UpdatedAt.UTC()
		p.EcoScore = sv(ecoS)
		if len(attrs) > 0 {
			_ = json.Unmarshal(attrs, &p.Attributes)
		}
		if len(traits) > 0 {
			_ = json.Unmarshal(traits, &p.Traits)
		}
		snap.Products = append(snap.Products, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, "SELECT id, product_id, sku, COALESCE(ean, ''), COALESCE(variant_name, ''), attributes, status, created_at, updated_at FROM shop.product_variants", func(r pgx.Rows) error {
		v := &domain.Variant{PriceFactor: 1, Demand: 1}
		var attrs []byte
		if err := r.Scan(&v.ID, &v.ProductID, &v.SKU, &v.EAN, &v.Name, &attrs, &v.Status, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return err
		}
		if len(attrs) > 0 {
			_ = json.Unmarshal(attrs, &v.Attrs)
		}
		snap.Variants = append(snap.Variants, v)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, `SELECT o.id, o.variant_id, o.seller_id, o.price, o.list_price, o.fulfillment, COALESCE(o.stock_qty, 0), COALESCE(o.handling_days, 1), o.status, o.created_at, o.updated_at,
		t.base_price, t.cost, COALESCE(t.demand_ema, 0), COALESCE(t.promo_id, 0)
		FROM shop.offers o LEFT JOIN sim.offer_traits t ON t.offer_id = o.id`, func(r pgx.Rows) error {
		o := &domain.Offer{}
		var price, list, base, cost pgtype.Numeric
		var hd int16
		var ema float32
		if err := r.Scan(&o.ID, &o.VariantID, &o.SellerID, &price, &list, &o.Fulfillment, &o.StockQty, &hd, &o.Status, &o.CreatedAt, &o.UpdatedAt, &base, &cost, &ema, &o.PromoID); err != nil {
			return err
		}
		o.Price, o.ListPrice, o.BasePrice, o.Cost = toMoney(price), toMoney(list), toMoney(base), toMoney(cost)
		if o.BasePrice == 0 {
			o.BasePrice = o.Price
		}
		o.HandlingDays = int(hd)
		o.DemandEMA = float64(ema)
		snap.Offers = append(snap.Offers, o)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, "SELECT warehouse_id, offer_id, qty_on_hand, qty_reserved, reorder_point, updated_at FROM shop.stock_levels", func(r pgx.Rows) error {
		sl := &domain.StockLevel{}
		if err := r.Scan(&sl.WarehouseID, &sl.OfferID, &sl.OnHand, &sl.Reserved, &sl.ReorderPoint, &sl.UpdatedAt); err != nil {
			return err
		}
		snap.Stock = append(snap.Stock, sl)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, `SELECT customer_id, country, COALESCE(address_id, 0), segment, gender, age, device, channel, pay_pref, tier, status, flags, active, rate, price_sens,
		return_prop, coupon_aff, satisfaction, spent, orders, created_at, updated_at, churn_at, last_order_at, spent_at, phone_verified_at, affinity, repl FROM sim.customer_traits`, func(r pgx.Rows) error {
		c := &domain.Customer{}
		var country, seg, gender, age, device, channel, pay, tier, status, flags int16
		var orders int32
		var aff, repl []byte
		if err := r.Scan(&c.ID, &country, &c.AddressID, &seg, &gender, &age, &device, &channel, &pay, &tier, &status, &flags, &c.Active, &c.Rate, &c.PriceSens,
			&c.ReturnProp, &c.CouponAff, &c.Satisfaction, &c.Spent, &orders, &c.CreatedAt, &c.UpdatedAt, &c.ChurnAt, &c.LastOrderAt, &c.SpentAt, &c.PhoneVerifiedAt, &aff, &repl); err != nil {
			return err
		}
		c.Country, c.Segment, c.Gender, c.Age, c.Device, c.Channel = uint8(country), domain.Segment(seg), uint8(gender), uint8(age), uint8(device), uint8(channel)
		c.PayPref, c.Tier, c.Status, c.Flags, c.Orders = uint8(pay), uint8(tier), uint8(status), uint8(flags), uint16(orders)
		copy(c.Affinity[:], aff)
		if len(repl) > 0 {
			_ = json.Unmarshal(repl, &c.Repl)
		}
		snap.Customers = append(snap.Customers, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, "SELECT id, name, campaign_type, starts_at, ends_at, COALESCE(discount_pct, 0)::float8, COALESCE(countries, '{}'), created_at FROM shop.campaigns", func(r pgx.Rows) error {
		cp := &domain.Campaign{}
		if err := r.Scan(&cp.ID, &cp.Name, &cp.Type, &cp.StartsAt, &cp.EndsAt, &cp.DiscountPct, &cp.Countries, &cp.CreatedAt); err != nil {
			return err
		}
		cp.StartsAt, cp.EndsAt = cp.StartsAt.UTC(), cp.EndsAt.UTC()
		snap.Campaigns = append(snap.Campaigns, cp)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, `SELECT id, code, COALESCE(campaign_id, 0), discount_type, discount_value::float8, min_order_value, valid_from, valid_to, COALESCE(max_uses, 0), times_used, created_at, updated_at FROM shop.coupons`, func(r pgx.Rows) error {
		cu := &domain.Coupon{}
		var min pgtype.Numeric
		var to *time.Time
		if err := r.Scan(&cu.ID, &cu.Code, &cu.CampaignID, &cu.DiscountType, &cu.DiscountValue, &min, &cu.ValidFrom, &to, &cu.MaxUses, &cu.TimesUsed, &cu.CreatedAt, &cu.UpdatedAt); err != nil {
			return err
		}
		cu.MinOrder = toMoney(min)
		cu.ValidTo = tt(to)
		cu.ValidFrom = cu.ValidFrom.UTC()
		snap.Coupons = append(snap.Coupons, cu)
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = each(ctx, db, "SELECT kind, id, due_at, data FROM sim.pending", func(r pgx.Rows) error {
		var pb ports.PendingBlob
		var due *time.Time
		if err := r.Scan(&pb.Kind, &pb.ID, &due, &pb.Data); err != nil {
			return err
		}
		pb.DueAt = tt(due)
		snap.Pending = append(snap.Pending, pb)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}
