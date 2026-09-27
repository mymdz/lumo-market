package postgres

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mymdz/lumo-market/internal/domain"
	"github.com/mymdz/lumo-market/internal/ports"
)

type opKind int

const (
	opExec opKind = iota
	opCopy
	opUpdate
	opUpsert
)

type op struct {
	kind  opKind
	table string
	cols  []string
	keys  []string // update/upsert keys
	rows  [][]any
	sql   string
	args  []any
}

// col is a column that may depend on a schema migration.
type col struct {
	name string
	mig  string
}

type tableDef struct {
	name string
	cols []col
}

func c(names ...string) []col {
	out := make([]col, len(names))
	for i, n := range names {
		out[i] = col{name: n}
		if j := strings.IndexByte(n, '@'); j > 0 {
			out[i] = col{name: n[:j], mig: n[j+1:]}
		}
	}
	return out
}

var (
	tCurrencies = tableDef{"shop.currencies", c("code", "name", "minor_units")}
	tCountries  = tableDef{"shop.countries", c("code", "name", "currency", "vat_rate", "vat_reduced", "timezone", "is_eu", "launched_at")}
	tCategories = tableDef{"shop.categories", c("id", "parent_id", "name", "slug", "path", "level", "is_active", "created_at", "updated_at")}
	tBrands     = tableDef{"shop.brands", c("id", "name", "tier", "country", "is_private_label", "created_at")}
	tWarehouses = tableDef{"shop.warehouses", c("id", "code", "name", "country", "city", "timezone", "opened_at")}
	tSellers    = tableDef{"shop.sellers", c("id", "name", "legal_name", "country", "seller_type", "status", "rating", "joined_at", "updated_at", "closed_at")}
	tSuppliers  = tableDef{"shop.suppliers", c("id", "brand_id", "name", "country", "lead_time_days", "created_at")}
	tCampaigns  = tableDef{"shop.campaigns", c("id", "name", "campaign_type", "starts_at", "ends_at", "discount_pct", "countries", "created_at")}
	tCoupons    = tableDef{"shop.coupons", c("id", "code", "campaign_id", "discount_type", "discount_value", "min_order_value", "valid_from", "valid_to", "max_uses", "times_used", "created_at", "updated_at")}
	tProducts   = tableDef{"shop.products", c("id", "category_id", "brand_id", "title", "description", "status", "weight_g", "attributes", "successor_id", "launched_at", "discontinued_at", "created_at", "updated_at", "eco_score@m003_products_eco_score")}
	tVariants   = tableDef{"shop.product_variants", c("id", "product_id", "sku", "ean", "variant_name", "attributes", "status", "created_at", "updated_at")}
	tOffers     = tableDef{"shop.offers", c("id", "variant_id", "seller_id", "price", "list_price", "currency", "fulfillment", "stock_qty", "handling_days", "status", "created_at", "updated_at")}
	tStock      = tableDef{"shop.stock_levels", c("warehouse_id", "offer_id", "qty_on_hand", "qty_reserved", "reorder_point", "updated_at")}
	tPrices     = tableDef{"shop.price_history", c("id", "offer_id", "old_price", "new_price", "reason", "changed_at")}
	tMoves      = tableDef{"shop.stock_movements", c("id", "warehouse_id", "offer_id", "qty_delta", "reason", "ref_type", "ref_id", "created_at")}
	tPOs        = tableDef{"shop.purchase_orders", c("id", "seller_id", "supplier_id", "warehouse_id", "status", "ordered_at", "expected_at", "received_at", "updated_at")}
	tPOItems    = tableDef{"shop.purchase_order_items", c("id", "purchase_order_id", "offer_id", "qty_ordered", "qty_received", "unit_cost")}
	tCustomers  = tableDef{"shop.customers", c("id", "email", "first_name", "last_name", "phone", "birth_date", "gender", "country", "language", "marketing_opt_in", "loyalty_tier", "acquisition_channel", "signup_device", "default_address_id", "status", "created_at", "updated_at", "deleted_at", "phone_verified_at@m005_customers_phone_verified")}
	tAddresses  = tableDef{"shop.addresses", c("id", "customer_id", "recipient_name", "line1", "line2", "postal_code", "city", "country", "phone", "created_at")}
	tCarts      = tableDef{"shop.carts", c("id", "customer_id", "session_id", "status", "country", "currency", "converted_order_id", "created_at", "updated_at")}
	tCartItems  = tableDef{"shop.cart_items", c("id", "cart_id", "offer_id", "qty", "unit_price", "added_at")}
	tOrders     = tableDef{"shop.orders", c("id", "order_number", "customer_id", "guest_email", "cart_id", "status", "currency", "fx_rate", "country", "items_subtotal", "discount_total", "shipping_fee", "tax_total", "grand_total", "coupon_id", "shipping_address_id", "billing_address_id", "channel", "shipping_method", "placed_at", "paid_at", "cancelled_at", "cancel_reason", "created_at", "updated_at", "utm_source@m002_orders_utm", "utm_campaign@m002_orders_utm")}
	tItems      = tableDef{"shop.order_items", c("id", "order_id", "line_no", "offer_id", "variant_id", "product_id", "seller_id", "title", "qty", "unit_price", "discount", "tax_rate", "tax_amount", "line_total", "commission_rate", "status", "returned_qty", "gift_wrap@m004_order_items_gift_wrap")}
	tHistory    = tableDef{"shop.order_status_history", c("id", "order_id", "from_status", "to_status", "actor", "changed_at")}
	tPayments   = tableDef{"shop.payments", c("id", "order_id", "method", "provider", "status", "amount", "currency", "refunded_amount", "failure_reason", "created_at", "captured_at", "updated_at")}
	tShipments  = tableDef{"shop.shipments", c("id", "order_id", "seller_id", "warehouse_id", "carrier", "tracking_number", "status", "shipping_method", "promised_at", "shipped_at", "delivered_at", "created_at", "updated_at", "co2_grams@m001_shipments_co2")}
	tReturns    = tableDef{"shop.returns", c("id", "order_id", "order_item_id", "qty", "reason", "status", "requested_at", "received_at", "refund_amount", "updated_at")}
	tRefunds    = tableDef{"shop.refunds", c("id", "payment_id", "order_id", "return_id", "amount", "reason", "created_at")}
	tReviews    = tableDef{"shop.reviews", c("id", "product_id", "customer_id", "order_item_id", "rating", "title", "body", "language", "is_verified", "helpful_votes", "status", "created_at", "updated_at", "media_count@m006_reviews_media")}

	tCustTraits   = tableDef{"sim.customer_traits", c("customer_id", "country", "address_id", "segment", "gender", "age", "device", "channel", "pay_pref", "tier", "status", "flags", "active", "rate", "price_sens", "return_prop", "coupon_aff", "satisfaction", "spent", "orders", "created_at", "updated_at", "churn_at", "last_order_at", "spent_at", "phone_verified_at", "affinity", "repl")}
	tProdTraits   = tableDef{"sim.product_traits", c("product_id", "traits")}
	tOfferTraits  = tableDef{"sim.offer_traits", c("offer_id", "base_price", "cost", "demand_ema", "promo_id")}
	tSellerTraits = tableDef{"sim.seller_traits", c("seller_id", "traits", "shipped", "cancelled")}
	tPending      = tableDef{"sim.pending", c("kind", "id", "due_at", "data")}
)

// ---------- value helpers ----------

func money(m domain.Money) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(int64(m)), Exp: -2, Valid: true}
}

func numf(v float64, scale int) pgtype.Numeric {
	p := math.Pow10(scale)
	return pgtype.Numeric{Int: big.NewInt(int64(math.Round(v * p))), Exp: int32(-scale), Valid: true}
}

func ts(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func unix(v int64) any {
	if v == 0 {
		return nil
	}
	return time.Unix(v, 0).UTC()
}

func str(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nz64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nz32(v int32) any {
	if v == 0 {
		return nil
	}
	return v
}

func nzInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func jsonb(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

func uuidVal(s string) any {
	h := strings.ReplaceAll(s, "-", "")
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 16 {
		return nil
	}
	var u pgtype.UUID
	copy(u.Bytes[:], b)
	u.Valid = true
	return u
}

var genders = []string{"", "female", "male"}

// ---------- encoder ----------

type encoder struct {
	applied map[string]bool
	ops     []op
	at      time.Time
}

func (e *encoder) columns(t tableDef) ([]string, []int) {
	var names []string
	var idx []int
	for i, cl := range t.cols {
		if cl.mig != "" && !e.applied[cl.mig] {
			continue
		}
		names = append(names, cl.name)
		idx = append(idx, i)
	}
	return names, idx
}

func project(row []any, idx []int) []any {
	if len(idx) == len(row) {
		return row
	}
	out := make([]any, len(idx))
	for i, j := range idx {
		out[i] = row[j]
	}
	return out
}

func (e *encoder) copyRows(t tableDef, rows [][]any) {
	if len(rows) == 0 {
		return
	}
	names, idx := e.columns(t)
	for i := range rows {
		rows[i] = project(rows[i], idx)
	}
	e.ops = append(e.ops, op{kind: opCopy, table: t.name, cols: names, rows: rows})
}

// update: keys first then value columns, all given as a full row of the
// named columns (which may include migration columns).
func (e *encoder) update(table string, keys []string, cols []col, rows [][]any) {
	if len(rows) == 0 {
		return
	}
	var names []string
	var idx []int
	for i, k := range keys {
		names = append(names, k)
		idx = append(idx, i)
	}
	for i, cl := range cols {
		if cl.mig != "" && !e.applied[cl.mig] {
			continue
		}
		names = append(names, cl.name)
		idx = append(idx, len(keys)+i)
	}
	for i := range rows {
		rows[i] = project(rows[i], idx)
	}
	e.ops = append(e.ops, op{kind: opUpdate, table: table, cols: names, keys: keys, rows: rows})
}

func (e *encoder) upsert(t tableDef, keys []string, rows [][]any) {
	if len(rows) == 0 {
		return
	}
	names, idx := e.columns(t)
	for i := range rows {
		rows[i] = project(rows[i], idx)
	}
	e.ops = append(e.ops, op{kind: opUpsert, table: t.name, cols: names, keys: keys, rows: rows})
}

func (e *encoder) exec(sql string, args ...any) {
	e.ops = append(e.ops, op{kind: opExec, sql: sql, args: args})
}

func encodeBatch(b *ports.Batch, applied map[string]bool) []op {
	e := &encoder{applied: applied, at: b.At}
	for _, m := range b.Migrations {
		e.exec(m.SQL)
		e.exec("INSERT INTO sim.migrations (id, sim_time) VALUES ($1, $2) ON CONFLICT DO NOTHING", m.ID, b.At)
		applied[m.ID] = true
	}

	// ---- reference ----
	var rows [][]any
	for _, cu := range b.Currencies {
		rows = append(rows, []any{cu.Code, cu.Name, 2})
	}
	e.copyRows(tCurrencies, rows)
	rows = nil
	for _, co := range b.Countries {
		rows = append(rows, []any{co.Code, co.Name, co.Currency, numf(co.VAT, 2), numf(co.VATReduced, 2), co.Spec.TZ, co.EU, co.LaunchedAt})
	}
	e.copyRows(tCountries, rows)
	rows = nil
	for _, ca := range b.Categories {
		rows = append(rows, []any{ca.ID, nz32(ca.ParentID), ca.Name, ca.Slug, ca.Path, ca.Level, ca.Active, ca.CreatedAt, ca.UpdatedAt})
	}
	e.copyRows(tCategories, rows)
	rows = nil
	for _, br := range b.Brands {
		rows = append(rows, []any{br.ID, br.Name, br.Tier.String(), str(br.Country), br.PrivateLabel, br.CreatedAt})
	}
	e.copyRows(tBrands, rows)
	rows = nil
	for _, w := range b.Warehouses {
		rows = append(rows, []any{w.ID, w.Code, w.Name, w.Country, w.City, w.TZName, w.OpenedAt})
	}
	e.copyRows(tWarehouses, rows)
	rows = nil
	for _, s := range b.NewSellers {
		rows = append(rows, sellerRow(s))
	}
	e.copyRows(tSellers, rows)
	rows = nil
	for _, s := range b.Suppliers {
		rows = append(rows, []any{s.ID, nz32(s.BrandID), s.Name, str(s.Country), numf(s.LeadDays, 1), s.CreatedAt})
	}
	e.copyRows(tSuppliers, rows)
	rows = nil
	for _, cp := range b.Campaigns {
		var countries any
		if len(cp.Countries) > 0 {
			countries = cp.Countries
		}
		rows = append(rows, []any{cp.ID, cp.Name, cp.Type, cp.StartsAt, cp.EndsAt, numf(cp.DiscountPct, 2), countries, cp.CreatedAt})
	}
	e.copyRows(tCampaigns, rows)
	rows = nil
	for _, cu := range b.NewCoupons {
		rows = append(rows, couponRow(cu))
	}
	e.copyRows(tCoupons, rows)

	// ---- catalog ----
	rows = nil
	for _, p := range b.NewProducts {
		rows = append(rows, productRow(p))
	}
	e.copyRows(tProducts, rows)
	rows = nil
	for _, v := range b.NewVariants {
		var attrs any
		if len(v.Attrs) > 0 {
			attrs = jsonb(v.Attrs)
		}
		var ean any
		if len(v.EAN) == 13 {
			ean = v.EAN
		}
		rows = append(rows, []any{v.ID, v.ProductID, v.SKU, ean, str(v.Name), attrs, v.Status, v.CreatedAt, v.UpdatedAt})
	}
	e.copyRows(tVariants, rows)
	rows = nil
	for _, o := range b.NewOffers {
		rows = append(rows, offerRow(o))
	}
	e.copyRows(tOffers, rows)
	rows = nil
	for _, s := range b.NewStock {
		rows = append(rows, stockRow(s))
	}
	e.copyRows(tStock, rows)
	rows = nil
	for _, pc := range b.PriceChanges {
		rows = append(rows, []any{pc.ID, pc.OfferID, money(pc.OldPrice), money(pc.NewPrice), pc.Reason, pc.ChangedAt})
	}
	e.copyRows(tPrices, rows)
	rows = nil
	for _, m := range b.StockMoves {
		rows = append(rows, []any{m.ID, m.WarehouseID, m.OfferID, m.Delta, m.Reason, str(m.RefType), nz64(m.RefID), m.CreatedAt})
	}
	e.copyRows(tMoves, rows)
	rows = nil
	var poItems [][]any
	for _, po := range b.NewPOs {
		rows = append(rows, poRow(po))
		for _, it := range po.Items {
			poItems = append(poItems, []any{it.ID, it.POID, it.OfferID, it.QtyOrdered, nzInt(it.QtyReceived), money(it.UnitCost)})
		}
	}
	e.copyRows(tPOs, rows)
	e.copyRows(tPOItems, poItems)

	// ---- customers ----
	rows = nil
	for _, p := range b.NewCustomers {
		rows = append(rows, customerRow(p))
	}
	e.copyRows(tCustomers, rows)
	rows = nil
	for _, a := range b.NewAddresses {
		rows = append(rows, []any{a.ID, nz64(a.CustomerID), str(a.Recipient), a.Line1, str(a.Line2), str(a.PostalCode), a.City, a.Country, str(a.Phone), a.CreatedAt})
	}
	e.copyRows(tAddresses, rows)

	// ---- carts ----
	rows = nil
	for _, ct := range b.NewCarts {
		rows = append(rows, []any{ct.ID, nz64(ct.CustomerID), uuidVal(ct.SessionID), ct.Status, ct.Country, ct.Currency, nz64(ct.ConvertedOrderID), ct.CreatedAt, ct.UpdatedAt})
	}
	e.copyRows(tCarts, rows)
	rows = nil
	for _, it := range b.NewCartItems {
		rows = append(rows, []any{it.ID, it.CartID, it.OfferID, it.Qty, money(it.UnitPrice), it.AddedAt})
	}
	e.copyRows(tCartItems, rows)

	// ---- orders ----
	var oRows, iRows, hRows, pRows, sRows, rRows, fRows [][]any
	for _, o := range b.Orders {
		if !o.Sim.Persisted {
			oRows = append(oRows, orderRow(o))
			for _, it := range o.Items {
				iRows = append(iRows, itemRow(it))
			}
		}
		visible := map[int64]bool{}
		for _, p := range o.Payments {
			if p.Persisted {
				visible[p.ID] = true
				continue
			}
			if p.VisibleAt.After(b.At) {
				continue
			}
			visible[p.ID] = true
			pRows = append(pRows, paymentRow(p))
		}
		for _, s := range o.Shipments {
			if !s.Persisted {
				sRows = append(sRows, shipmentRow(s))
			}
		}
		for _, r := range o.Returns {
			if !r.Persisted {
				rRows = append(rRows, returnRow(r))
			}
		}
		for _, r := range o.Refunds {
			if !r.Persisted && visible[r.PaymentID] {
				fRows = append(fRows, []any{r.ID, r.PaymentID, r.OrderID, nz64(r.ReturnID), money(r.Amount), r.Reason, r.CreatedAt})
			}
		}
		for _, h := range o.History {
			if !h.Persisted {
				hRows = append(hRows, []any{h.ID, h.OrderID, str(h.From), h.To, h.Actor, h.ChangedAt})
			}
		}
	}
	e.copyRows(tOrders, oRows)
	e.copyRows(tItems, iRows)
	e.copyRows(tHistory, hRows)
	e.copyRows(tPayments, pRows)
	e.copyRows(tShipments, sRows)
	e.copyRows(tReturns, rRows)
	e.copyRows(tRefunds, fRows)

	rows = nil
	for _, r := range b.Reviews {
		rows = append(rows, []any{r.ID, r.ProductID, nz64(r.CustomerID), nz64(r.OrderItemID), r.Rating, str(r.Title), str(r.Body), str(r.Language), r.Verified, r.Helpful, r.Status, r.CreatedAt, r.UpdatedAt, nzInt(r.MediaCount)})
	}
	e.copyRows(tReviews, rows)
	for _, fx := range b.FxRates {
		e.exec("INSERT INTO shop.fx_rates (rate_date, currency, rate_per_eur) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING", fx.Date, fx.Currency, numf(fx.Rate, 6))
	}

	// ---- updates ----
	e.encodeUpdates(b)

	// ---- deletes ----
	if !b.CartsDeleteBefore.IsZero() {
		e.exec("DELETE FROM shop.cart_items WHERE cart_id IN (SELECT id FROM shop.carts WHERE updated_at < $1)", b.CartsDeleteBefore)
		e.exec("DELETE FROM shop.carts WHERE updated_at < $1", b.CartsDeleteBefore)
	}

	// ---- simulator internals ----
	rows = nil
	for _, cu := range b.CustomerTraits {
		rows = append(rows, traitRow(cu))
	}
	e.upsert(tCustTraits, []string{"customer_id"}, rows)
	rows = nil
	for _, p := range b.ProductTraits {
		rows = append(rows, []any{p.ID, jsonb(p.Traits)})
	}
	e.upsert(tProdTraits, []string{"product_id"}, rows)
	rows = nil
	for _, o := range b.OfferTraits {
		rows = append(rows, []any{o.ID, money(o.BasePrice), money(o.Cost), float32(o.DemandEMA), int32(o.PromoID)})
	}
	e.upsert(tOfferTraits, []string{"offer_id"}, rows)
	rows = nil
	for _, s := range b.SellerTraits {
		rows = append(rows, []any{s.ID, jsonb(s.Traits), s.Shipped, s.Cancelled})
	}
	e.upsert(tSellerTraits, []string{"seller_id"}, rows)
	if len(b.PendingDeletes) > 0 {
		kinds := make([]string, len(b.PendingDeletes))
		ids := make([]int64, len(b.PendingDeletes))
		for i, k := range b.PendingDeletes {
			kinds[i], ids[i] = k.Kind, k.ID
		}
		e.exec("DELETE FROM sim.pending p USING (SELECT unnest($1::text[]) AS kind, unnest($2::bigint[]) AS id) d WHERE p.kind = d.kind AND p.id = d.id", kinds, ids)
	}
	rows = nil
	for _, pb := range b.PendingUpserts {
		rows = append(rows, []any{pb.Kind, pb.ID, ts(pb.DueAt), string(pb.Data)})
	}
	e.upsert(tPending, []string{"kind", "id"}, rows)
	e.exec(`INSERT INTO sim.meta (id, phase, sim_time, horizon, start_time, seed, state, updated_at) VALUES (1, $1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (id) DO UPDATE SET phase = EXCLUDED.phase, sim_time = EXCLUDED.sim_time, state = EXCLUDED.state, updated_at = now()`,
		b.Phase, b.SimTime, b.Horizon, b.Start, int64(b.Seed), string(b.StateJSON))
	return e.ops
}

func (e *encoder) encodeUpdates(b *ports.Batch) {
	var rows [][]any
	var items, pays, ships, rets [][]any
	for _, ch := range b.OrderChanges {
		o := ch.Order
		if ch.Root {
			rows = append(rows, []any{o.ID, o.Status, ts(o.PaidAt), ts(o.CancelledAt), str(o.CancelReason), o.UpdatedAt})
		}
		for _, it := range ch.Items {
			items = append(items, []any{it.ID, it.Status, it.ReturnedQty})
		}
		for _, p := range ch.Payments {
			pays = append(pays, []any{p.ID, p.Status, money(p.Amount), money(p.RefundedAmount), str(p.FailureReason), ts(p.CapturedAt), p.UpdatedAt})
		}
		for _, s := range ch.Shipments {
			ships = append(ships, []any{s.ID, s.Status, str(s.Tracking), ts(s.ShippedAt), ts(s.DeliveredAt), s.UpdatedAt})
		}
		for _, r := range ch.Returns {
			rets = append(rets, []any{r.ID, r.Status, ts(r.ReceivedAt), refundAmt(r), r.UpdatedAt})
		}
	}
	e.update("shop.orders", []string{"id"}, c("status", "paid_at", "cancelled_at", "cancel_reason", "updated_at"), rows)
	e.update("shop.order_items", []string{"id"}, c("status", "returned_qty"), items)
	e.update("shop.payments", []string{"id"}, c("status", "amount", "refunded_amount", "failure_reason", "captured_at", "updated_at"), pays)
	e.update("shop.shipments", []string{"id"}, c("status", "tracking_number", "shipped_at", "delivered_at", "updated_at"), ships)
	e.update("shop.returns", []string{"id"}, c("status", "received_at", "refund_amount", "updated_at"), rets)

	rows = nil
	for _, ct := range b.CartUpdates {
		rows = append(rows, []any{ct.ID, ct.Status, nz64(ct.CustomerID), nz64(ct.ConvertedOrderID), ct.UpdatedAt})
	}
	e.update("shop.carts", []string{"id"}, c("status", "customer_id", "converted_order_id", "updated_at"), rows)
	rows = nil
	for _, o := range b.OfferUpdates {
		var stock any
		if o.Fulfillment == domain.FulfilSeller {
			stock = o.StockQty
		}
		rows = append(rows, []any{o.ID, money(o.Price), money(o.ListPrice), stock, o.Status, o.UpdatedAt})
	}
	e.update("shop.offers", []string{"id"}, c("price", "list_price", "stock_qty", "status", "updated_at"), rows)
	rows = nil
	for _, s := range b.StockUpdates {
		rows = append(rows, []any{s.WarehouseID, s.OfferID, s.OnHand, s.Reserved, s.ReorderPoint, s.UpdatedAt})
	}
	e.update("shop.stock_levels", []string{"warehouse_id", "offer_id"}, c("qty_on_hand", "qty_reserved", "reorder_point", "updated_at"), rows)
	rows = nil
	for _, cu := range b.CustomerUpdates {
		status := "active"
		var deleted any
		switch cu.Status {
		case domain.CustDeleted:
			status = "deleted"
			deleted = time.Unix(cu.UpdatedAt, 0).UTC()
		case domain.CustBlocked:
			status = "blocked"
		}
		rows = append(rows, []any{cu.ID, cu.Has(domain.FlagOptIn), domain.LoyaltyTiers[cu.Tier], nz64(cu.AddressID), status, time.Unix(cu.UpdatedAt, 0).UTC(), deleted, unix(cu.PhoneVerifiedAt)})
	}
	e.update("shop.customers", []string{"id"}, c("marketing_opt_in", "loyalty_tier", "default_address_id", "status", "updated_at", "deleted_at", "phone_verified_at@m005_customers_phone_verified"), rows)
	rows = nil
	for _, p := range b.ProductUpdates {
		rows = append(rows, []any{p.ID, p.Status, ts(p.DiscontinuedAt), nz64(p.SuccessorID), p.UpdatedAt, str(p.EcoScore)})
	}
	e.update("shop.products", []string{"id"}, c("status", "discontinued_at", "successor_id", "updated_at", "eco_score@m003_products_eco_score"), rows)
	rows = nil
	for _, s := range b.SellerUpdates {
		rows = append(rows, []any{s.ID, s.Status, numf(s.Rating, 2), s.UpdatedAt, ts(s.ClosedAt)})
	}
	e.update("shop.sellers", []string{"id"}, c("status", "rating", "updated_at", "closed_at"), rows)
	rows = nil
	for _, cu := range b.CouponUpdates {
		rows = append(rows, []any{cu.ID, cu.TimesUsed, cu.UpdatedAt})
	}
	e.update("shop.coupons", []string{"id"}, c("times_used", "updated_at"), rows)
	rows = nil
	var poi [][]any
	for _, po := range b.POUpdates {
		rows = append(rows, []any{po.ID, po.Status, ts(po.ReceivedAt), po.UpdatedAt})
		if po.Status == "received" {
			for _, it := range po.Items {
				poi = append(poi, []any{it.ID, it.QtyReceived})
			}
		}
	}
	e.update("shop.purchase_orders", []string{"id"}, c("status", "received_at", "updated_at"), rows)
	e.update("shop.purchase_order_items", []string{"id"}, c("qty_received"), poi)

	// personal data changes
	var erase []int64
	for _, p := range b.CustomerPII {
		if p.Erase {
			erase = append(erase, p.CustomerID)
			continue
		}
		e.exec("UPDATE shop.customers SET email = COALESCE($2, email), phone = COALESCE($3, phone) WHERE id = $1", p.CustomerID, str(p.Email), strp(p.Phone))
	}
	if len(erase) > 0 {
		e.exec(`UPDATE shop.customers SET email = 'erased-' || id || '@erased.invalid', first_name = NULL, last_name = NULL, phone = NULL, birth_date = NULL WHERE id = ANY($1)`, erase)
		e.exec(`UPDATE shop.addresses SET recipient_name = '[erased]', line1 = '[erased]', line2 = NULL, phone = NULL WHERE customer_id = ANY($1)`, erase)
	}
}

func strp(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func refundAmt(r *domain.Return) any {
	if r.RefundAmt == 0 {
		return nil
	}
	return money(r.RefundAmt)
}

func sellerRow(s *domain.Seller) []any {
	var rating any
	if s.Rating > 0 {
		rating = numf(s.Rating, 2)
	}
	return []any{s.ID, s.Name, s.LegalName, s.Country, s.Type, s.Status, rating, s.JoinedAt, s.UpdatedAt, ts(s.ClosedAt)}
}

func couponRow(cu *domain.Coupon) []any {
	return []any{cu.ID, cu.Code, nz32(cu.CampaignID), cu.DiscountType, numf(cu.DiscountValue, 2), money(cu.MinOrder), cu.ValidFrom, ts(cu.ValidTo), nzInt(cu.MaxUses), cu.TimesUsed, cu.CreatedAt, cu.UpdatedAt}
}

func productRow(p *domain.Product) []any {
	var attrs any
	if len(p.Attributes) > 0 {
		attrs = jsonb(p.Attributes)
	}
	return []any{p.ID, p.CategoryID, nz32(p.BrandID), p.Title, str(p.Description), p.Status, nzInt(p.WeightG), attrs, nz64(p.SuccessorID), p.LaunchedAt, ts(p.DiscontinuedAt), p.CreatedAt, p.UpdatedAt, str(p.EcoScore)}
}

func offerRow(o *domain.Offer) []any {
	var stock, handling any
	if o.Fulfillment == domain.FulfilSeller {
		stock = o.StockQty
		handling = o.HandlingDays
	}
	return []any{o.ID, o.VariantID, o.SellerID, money(o.Price), money(o.ListPrice), "EUR", o.Fulfillment, stock, handling, o.Status, o.CreatedAt, o.UpdatedAt}
}

func stockRow(s *domain.StockLevel) []any {
	return []any{s.WarehouseID, s.OfferID, s.OnHand, s.Reserved, s.ReorderPoint, s.UpdatedAt}
}

func poRow(po *domain.PurchaseOrder) []any {
	return []any{po.ID, po.SellerID, nz32(po.SupplierID), po.WarehouseID, po.Status, po.OrderedAt, po.ExpectedAt, ts(po.ReceivedAt), po.UpdatedAt}
}

func customerRow(p *domain.CustomerProfile) []any {
	cu := p.C
	status := "active"
	switch cu.Status {
	case domain.CustDeleted:
		status = "deleted"
	case domain.CustBlocked:
		status = "blocked"
	}
	var birth any
	if !p.BirthDate.IsZero() {
		birth = p.BirthDate
	}
	return []any{cu.ID, p.Email, str(p.FirstName), str(p.LastName), str(p.Phone), birth, str(genders[cu.Gender]), p.Country, str(p.Language),
		cu.Has(domain.FlagOptIn), domain.LoyaltyTiers[cu.Tier], domain.Channels[cu.Channel], domain.Devices[cu.Device], nz64(cu.AddressID), status,
		time.Unix(cu.CreatedAt, 0).UTC(), time.Unix(cu.UpdatedAt, 0).UTC(), nil, unix(cu.PhoneVerifiedAt)}
}

func orderRow(o *domain.Order) []any {
	return []any{o.ID, o.Number, nz64(o.CustomerID), str(o.GuestEmail), nz64(o.CartID), o.Status, o.Currency, numf(o.FX, 6), o.Country,
		money(o.Subtotal), money(o.Discount), money(o.ShippingFee), money(o.Tax), money(o.Total), nz32(o.CouponID), nz64(o.ShipAddrID), nz64(o.BillAddrID),
		o.Channel, o.ShipMethod, o.PlacedAt, ts(o.PaidAt), ts(o.CancelledAt), str(o.CancelReason), o.CreatedAt, o.UpdatedAt, str(o.UTMSource), str(o.UTMCampaign)}
}

func itemRow(it *domain.OrderItem) []any {
	return []any{it.ID, it.OrderID, it.LineNo, it.OfferID, it.VariantID, it.ProductID, it.SellerID, it.Title, it.Qty, money(it.UnitPrice), money(it.Discount),
		numf(it.TaxRate, 2), money(it.Tax), money(it.LineTotal), numf(it.Commission, 4), it.Status, it.ReturnedQty, it.GiftWrap}
}

func paymentRow(p *domain.Payment) []any {
	return []any{p.ID, p.OrderID, p.Method, str(p.Provider), p.Status, money(p.Amount), p.Currency, money(p.RefundedAmount), str(p.FailureReason), p.CreatedAt, ts(p.CapturedAt), p.UpdatedAt}
}

func shipmentRow(s *domain.Shipment) []any {
	var wh any
	if s.WarehouseID != 0 {
		wh = s.WarehouseID
	}
	return []any{s.ID, s.OrderID, s.SellerID, wh, s.Carrier, str(s.Tracking), s.Status, s.Method, ts(s.PromisedAt), ts(s.ShippedAt), ts(s.DeliveredAt), s.CreatedAt, s.UpdatedAt, nzInt(s.CO2Grams)}
}

func returnRow(r *domain.Return) []any {
	return []any{r.ID, r.OrderID, r.OrderItemID, r.Qty, r.Reason, r.Status, r.RequestedAt, ts(r.ReceivedAt), refundAmt(r), r.UpdatedAt}
}

func traitRow(cu *domain.Customer) []any {
	var repl any
	if len(cu.Repl) > 0 {
		repl = jsonb(cu.Repl)
	}
	aff := make([]byte, len(cu.Affinity))
	copy(aff, cu.Affinity[:])
	return []any{cu.ID, int16(cu.Country), nz64(cu.AddressID), int16(cu.Segment), int16(cu.Gender), int16(cu.Age), int16(cu.Device), int16(cu.Channel), int16(cu.PayPref),
		int16(cu.Tier), int16(cu.Status), int16(cu.Flags), cu.Active, cu.Rate, cu.PriceSens, cu.ReturnProp, cu.CouponAff, cu.Satisfaction, cu.Spent, int32(cu.Orders),
		cu.CreatedAt, cu.UpdatedAt, cu.ChurnAt, cu.LastOrderAt, cu.SpentAt, cu.PhoneVerifiedAt, aff, repl}
}
