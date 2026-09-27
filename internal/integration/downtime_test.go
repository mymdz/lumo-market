//go:build integration

// Integration tests run against a real PostgreSQL:
//
//	LUMO_TEST_DSN=postgres://lumo:lumo@localhost:5433/lumo?sslmode=disable \
//	  go test -tags integration -v -timeout 60m ./internal/integration/
//
// They create (and drop) a separate database, the DSN only needs to point to
// any database on the server with permission to create databases.
package integration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mymdz/lumo-market/internal/adapters/postgres"
	"github.com/mymdz/lumo-market/internal/adapters/refdata"
	"github.com/mymdz/lumo-market/internal/app"
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

func testDB(t *testing.T) string {
	dsn := os.Getenv("LUMO_TEST_DSN")
	if dsn == "" {
		t.Skip("LUMO_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	const name = "lumo_it"
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if os.Getenv("LUMO_TEST_KEEP") != "" {
			return
		}
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	})
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", cfg.User, cfg.Password, cfg.Host, cfg.Port, name)
}

// runUntil runs an engine until cond holds (plus a little extra), then stops it.
func runUntil(t *testing.T, dsn string, cfg app.Config, cond func(e *app.Engine) bool, extra time.Duration) time.Duration {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	store, err := postgres.New(ctx, dsn, log)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ref, err := refdata.New().Load()
	if err != nil {
		t.Fatal(err)
	}
	engine := app.NewEngine(cfg, ref, store, log)
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- engine.Run(ctx) }()
	deadline := time.After(45 * time.Minute)
	for !cond(engine) {
		select {
		case err := <-done:
			t.Fatalf("engine stopped early: %v", err)
		case <-deadline:
			t.Fatal("timeout waiting for condition")
		case <-time.After(200 * time.Millisecond):
		}
	}
	took := time.Since(start)
	time.Sleep(extra)
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("engine: %v", err)
	}
	return took
}

func realTime(e *app.Engine) bool {
	st := e.Status()
	return st.Phase == app.PhaseLive && !st.CatchingUp && st.LagSeconds < 10 && !st.SimTime.IsZero()
}

// TestDowntimeCatchUp stops the simulator for a week and checks that on the
// next start it simulates the missing week, then continues in real time,
// without ever dating anything in the future.
func TestDowntimeCatchUp(t *testing.T) {
	dsn := testDB(t)
	gap := time.Duration(envInt("IT_GAP_HOURS", 7*24)) * time.Hour
	cfg := app.Config{
		Seed: 7, HistoryDays: 2, Dirt: 0.5,
		OrdersStart:     float64(envInt("IT_ORDERS", 2000)),
		OrdersNow:       float64(envInt("IT_ORDERS", 2000)),
		InitialProducts: envInt("IT_PRODUCTS", 2000),
		InitialSellers:  40,
	}

	// phase 1: the world lives a week in the past, then stops
	offset := -gap
	cfg.Now = func() time.Time { return time.Now().Add(offset) }
	runUntil(t, dsn, cfg, realTime, 3*time.Second)
	stoppedAt := time.Now().Add(offset)

	// phase 2: started again "a week later"
	cfg.Now = time.Now
	took := runUntil(t, dsn, cfg, realTime, 3*time.Second)
	t.Logf("caught up on %s of downtime in %s", gap, took.Round(time.Second))

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	// orders exist for every day of the downtime
	rows, err := conn.Query(ctx, `SELECT d::date, (SELECT count(*) FROM shop.orders WHERE placed_at >= d AND placed_at < d + interval '1 day')
		FROM generate_series(date_trunc('day', $1::timestamptz) + interval '1 day', date_trunc('day', now()) - interval '1 day', interval '1 day') d`, stoppedAt)
	if err != nil {
		t.Fatal(err)
	}
	days := 0
	for rows.Next() {
		var d time.Time
		var n int64
		if err := rows.Scan(&d, &n); err != nil {
			t.Fatal(err)
		}
		days++
		t.Logf("%s: %d orders", d.Format("2006-01-02 Mon"), n)
		if float64(n) < cfg.OrdersNow*0.3 {
			t.Errorf("too few orders on %s: %d", d.Format("2006-01-02"), n)
		}
	}
	rows.Close()
	if gap >= 48*time.Hour && days == 0 {
		t.Error("no full days inside the downtime window")
	}

	// orders placed before the downtime kept progressing during it
	var progressed int64
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM shop.orders WHERE placed_at < $1 AND updated_at > $1 AND status IN ('delivered', 'returned', 'partially_returned')`, stoppedAt).Scan(&progressed); err != nil {
		t.Fatal(err)
	}
	t.Logf("orders from before the downtime that progressed during it: %d", progressed)
	if progressed == 0 {
		t.Error("no pre-downtime order progressed during the downtime")
	}

	// nothing is dated in the future
	store, err := postgres.New(ctx, dsn, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	findings, err := store.FutureDated(ctx, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Errorf("future-dated rows: %s rows=%d max=%s", f.Column, f.Rows, f.Max)
	}
}
