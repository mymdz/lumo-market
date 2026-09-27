-- A few queries to get a feel for the data.

-- Daily orders and revenue in EUR (seasonality, campaigns, growth)
SELECT placed_at::date AS day, count(*) AS orders,
       round(sum(grand_total / fx_rate)) AS revenue_eur,
       round(avg(grand_total / fx_rate), 2) AS aov_eur
FROM shop.orders
WHERE status <> 'cancelled'
GROUP BY 1 ORDER BY 1;

-- Revenue concentration (Pareto)
WITH s AS (
    SELECT i.product_id, sum(i.line_total / o.fx_rate) AS rev
    FROM shop.order_items i JOIN shop.orders o ON o.id = i.order_id
    GROUP BY 1
), r AS (
    SELECT rev, row_number() OVER (ORDER BY rev DESC) rn, count(*) OVER () n, sum(rev) OVER () tot FROM s
)
SELECT round(100 * sum(rev) FILTER (WHERE rn <= n * 0.01) / max(tot), 1) AS top_1pct_share,
       round(100 * sum(rev) FILTER (WHERE rn <= n * 0.2) / max(tot), 1) AS top_20pct_share
FROM r;

-- Monthly acquisition cohorts: share of customers ordering again N months later
WITH first AS (
    SELECT customer_id, date_trunc('month', min(placed_at)) AS cohort
    FROM shop.orders WHERE customer_id IS NOT NULL GROUP BY 1
), act AS (
    SELECT DISTINCT o.customer_id, f.cohort,
           (extract(year FROM age(date_trunc('month', o.placed_at), f.cohort)) * 12
            + extract(month FROM age(date_trunc('month', o.placed_at), f.cohort)))::int AS m
    FROM shop.orders o JOIN first f USING (customer_id)
)
SELECT cohort::date, m, count(*) AS customers
FROM act GROUP BY 1, 2 ORDER BY 1, 2;

-- Return rate by department
SELECT c0.name AS department,
       round(100.0 * sum(i.returned_qty) / nullif(sum(i.qty), 0), 1) AS return_rate_pct
FROM shop.order_items i
JOIN shop.products p ON p.id = i.product_id
JOIN shop.categories c ON c.id = p.category_id
JOIN shop.categories c1 ON c1.id = c.parent_id
JOIN shop.categories c0 ON c0.id = c1.parent_id
GROUP BY 1 ORDER BY 2 DESC;

-- Delivery time by country and carrier (warehouse openings are visible over time)
SELECT o.country, s.carrier, count(*) AS shipments,
       round(avg(extract(epoch FROM s.delivered_at - s.shipped_at) / 86400)::numeric, 1) AS transit_days,
       round(100.0 * avg((s.delivered_at > s.promised_at + interval '1 day')::int), 1) AS late_pct
FROM shop.shipments s JOIN shop.orders o ON o.id = s.order_id
WHERE s.status = 'delivered'
GROUP BY 1, 2 HAVING count(*) > 500 ORDER BY 1, 3 DESC;

-- Payment failures by method per day (payment provider outages stand out)
SELECT created_at::date AS day, method, count(*) AS attempts,
       round(100.0 * avg((status = 'failed')::int), 1) AS fail_pct
FROM shop.payments GROUP BY 1, 2 HAVING count(*) > 50 ORDER BY fail_pct DESC LIMIT 20;

-- Traps for incremental loads: rows whose updated_at did not move although they changed
-- (compare two snapshots, or use CDC). Late-arriving payments: created_at far behind commit time
SELECT id, created_at, pg_xact_commit_timestamp(xmin) AS committed_at
FROM shop.payments
WHERE pg_xact_commit_timestamp(xmin) - created_at > interval '10 minutes'
ORDER BY id DESC LIMIT 20;

-- Likely duplicate customers
SELECT lower(btrim(first_name)) AS first_name, lower(btrim(last_name)) AS last_name, count(*), array_agg(email)
FROM shop.customers
WHERE first_name IS NOT NULL
GROUP BY 1, 2 HAVING count(*) > 1 ORDER BY 3 DESC LIMIT 20;
