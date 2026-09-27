// Command lumo simulates a European online marketplace and continuously
// writes its activity into PostgreSQL.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/mymdz/lumo-market/internal/adapters/config"
	"github.com/mymdz/lumo-market/internal/adapters/httpapi"
	"github.com/mymdz/lumo-market/internal/adapters/postgres"
	"github.com/mymdz/lumo-market/internal/adapters/refdata"
	"github.com/mymdz/lumo-market/internal/app"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to the config file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: lumo [-config config.yaml] [run|reset|check]\n\n  run    create or resume the simulation (default)\n  reset  drop all simulator data\n  check  report rows dated in the future (plan columns like promised_at are ignored)\n")
	}
	flag.Parse()
	cmd := "run"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	level := slog.LevelInfo
	if strings.EqualFold(cfg.LogLevel, "debug") {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cmd, cfg, log); err != nil && err != context.Canceled {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, cfg config.Config, log *slog.Logger) error {
	store, err := postgres.New(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer store.Close()

	switch cmd {
	case "reset":
		if err := store.Reset(ctx); err != nil {
			return err
		}
		log.Info("simulator data dropped")
		return nil
	case "check":
		findings, err := store.FutureDated(ctx, 5*time.Second)
		if err != nil {
			return err
		}
		if len(findings) == 0 {
			log.Info("no future-dated rows")
			return nil
		}
		for _, f := range findings {
			log.Warn("future-dated rows", "column", f.Column, "rows", f.Rows, "max", f.Max.Format(time.RFC3339))
		}
		return fmt.Errorf("%d columns contain future-dated rows", len(findings))
	case "run":
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}

	ref, err := refdata.New().Load()
	if err != nil {
		return err
	}
	engine := app.NewEngine(app.Config{
		Seed:               cfg.Seed,
		HistoryDays:        cfg.HistoryDays,
		OrdersStart:        cfg.OrdersStart,
		OrdersNow:          cfg.OrdersNow,
		GrowthAfter:        cfg.GrowthAfter,
		InitialProducts:    cfg.InitialProducts,
		InitialSellers:     cfg.InitialSellers,
		Dirt:               cfg.Dirt,
		Speed:              cfg.Speed,
		LiveFlush:          cfg.LiveFlush,
		BackfillCheckpoint: cfg.BackfillCheckpoint,
		AllowFuture:        cfg.AllowFuture,
	}, ref, store, log)

	api := httpapi.New(cfg.HTTPAddr, engine, store, log)
	api.Start()
	log.Info("control API listening", "addr", cfg.HTTPAddr)
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = api.Shutdown(sctx)
	}()
	return engine.Run(ctx)
}
