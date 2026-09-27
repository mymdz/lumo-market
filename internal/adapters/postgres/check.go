package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// planColumns legitimately hold future values: they describe plans, not events.
var planColumns = map[string]bool{
	"shipments.promised_at":       true,
	"purchase_orders.expected_at": true,
	"campaigns.starts_at":         true,
	"campaigns.ends_at":           true,
	"coupons.valid_from":          true,
	"coupons.valid_to":            true,
}

// FutureFinding is a timestamp column holding values after "now".
type FutureFinding struct {
	Column string
	Rows   int64
	Max    time.Time
}

// FutureDated scans every event-time column of the shop schema for values
// later than now + tolerance. returns.received_at is local warehouse time
// without a time zone (European time zones are at most UTC+3).
func (s *Store) FutureDated(ctx context.Context, tolerance time.Duration) ([]FutureFinding, error) {
	rows, err := s.pool.Query(ctx, `SELECT table_name, column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'shop' AND data_type IN ('timestamp with time zone', 'timestamp without time zone', 'date')
		ORDER BY table_name, column_name`)
	if err != nil {
		return nil, err
	}
	type colRef struct{ table, column, typ string }
	var cols []colRef
	for rows.Next() {
		var c colRef
		if err := rows.Scan(&c.table, &c.column, &c.typ); err != nil {
			rows.Close()
			return nil, err
		}
		cols = append(cols, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []FutureFinding
	tol := fmt.Sprintf("%d seconds", int(tolerance.Seconds()))
	for _, c := range cols {
		name := c.table + "." + c.column
		if planColumns[name] {
			continue
		}
		limit := fmt.Sprintf("now() + interval '%s'", tol)
		switch c.typ {
		case "timestamp without time zone":
			limit = fmt.Sprintf("(now() AT TIME ZONE 'UTC') + interval '3 hours' + interval '%s'", tol)
		case "date":
			limit = "current_date"
		}
		q := fmt.Sprintf("SELECT count(*), max(%s)::timestamp FROM shop.%s WHERE %s > %s",
			pgx.Identifier{c.column}.Sanitize(), pgx.Identifier{c.table}.Sanitize(), pgx.Identifier{c.column}.Sanitize(), limit)
		var f FutureFinding
		var max *time.Time
		if err := s.pool.QueryRow(ctx, q).Scan(&f.Rows, &max); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if f.Rows > 0 {
			f.Column = name
			if max != nil {
				f.Max = *max
			}
			out = append(out, f)
		}
	}
	return out, nil
}
