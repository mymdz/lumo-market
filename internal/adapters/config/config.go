// Package config loads settings from a YAML file with environment overrides.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	DatabaseURL        string        `yaml:"database_url" env:"DATABASE_URL"`
	HTTPAddr           string        `yaml:"http_addr" env:"HTTP_ADDR"`
	Seed               uint64        `yaml:"seed" env:"SEED"`
	HistoryDays        int           `yaml:"history_days" env:"HISTORY_DAYS"`
	OrdersStart        float64       `yaml:"orders_per_day_start" env:"ORDERS_PER_DAY_START"`
	OrdersNow          float64       `yaml:"orders_per_day_now" env:"ORDERS_PER_DAY_NOW"`
	GrowthAfter        float64       `yaml:"growth_per_year_after_now" env:"GROWTH_PER_YEAR"`
	InitialProducts    int           `yaml:"initial_products" env:"INITIAL_PRODUCTS"`
	InitialSellers     int           `yaml:"initial_sellers" env:"INITIAL_SELLERS"`
	Dirt               float64       `yaml:"dirt_level" env:"DIRT_LEVEL"`
	Speed              float64       `yaml:"speed" env:"SPEED"`
	LiveFlush          time.Duration `yaml:"live_flush" env:"LIVE_FLUSH"`
	BackfillCheckpoint time.Duration `yaml:"backfill_checkpoint" env:"BACKFILL_CHECKPOINT"`
	LogLevel           string        `yaml:"log_level" env:"LOG_LEVEL"`
	AllowFuture        bool          `yaml:"allow_future" env:"ALLOW_FUTURE"`
}

func Default() Config {
	return Config{
		DatabaseURL:        "postgres://lumo:lumo@localhost:5433/lumo?sslmode=disable",
		HTTPAddr:           ":8080",
		Seed:               42,
		HistoryDays:        730,
		OrdersStart:        5000,
		OrdersNow:          13000,
		GrowthAfter:        0.25,
		InitialProducts:    20000,
		InitialSellers:     250,
		Dirt:               0.5,
		Speed:              1,
		LiveFlush:          time.Second,
		BackfillCheckpoint: 24 * time.Hour,
		LogLevel:           "info",
	}
}

// Load reads the YAML file (optional), then its local sibling
// (config.yaml -> config.local.yaml, optional, not committed) on top of it,
// and finally applies LUMO_* env overrides. The local file only needs the keys
// that differ, so shared defaults keep flowing in from the committed file.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		for _, p := range []string{path, LocalPath(path)} {
			if err := mergeFile(&cfg, p); err != nil {
				return cfg, err
			}
		}
	}
	v := reflect.ValueOf(&cfg).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		key := t.Field(i).Tag.Get("env")
		raw, ok := os.LookupEnv("LUMO_" + key)
		if !ok || key == "" {
			continue
		}
		f := v.Field(i)
		switch f.Interface().(type) {
		case time.Duration:
			d, err := time.ParseDuration(raw)
			if err != nil {
				return cfg, fmt.Errorf("LUMO_%s: %w", key, err)
			}
			f.SetInt(int64(d))
			continue
		}
		switch f.Kind() {
		case reflect.String:
			f.SetString(raw)
		case reflect.Bool:
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return cfg, fmt.Errorf("LUMO_%s: %w", key, err)
			}
			f.SetBool(b)
		case reflect.Int:
			n, err := strconv.Atoi(raw)
			if err != nil {
				return cfg, fmt.Errorf("LUMO_%s: %w", key, err)
			}
			f.SetInt(int64(n))
		case reflect.Uint64:
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return cfg, fmt.Errorf("LUMO_%s: %w", key, err)
			}
			f.SetUint(n)
		case reflect.Float64:
			n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil {
				return cfg, fmt.Errorf("LUMO_%s: %w", key, err)
			}
			f.SetFloat(n)
		}
	}
	return cfg, nil
}

// LocalPath returns the machine-local override file for a config path:
// config.yaml -> config.local.yaml.
func LocalPath(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + ".local" + ext
}

// mergeFile overlays the keys present in a YAML file onto cfg; a missing file
// is not an error.
func mergeFile(cfg *Config, path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
