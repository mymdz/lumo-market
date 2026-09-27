// Package config loads settings from a YAML file with environment overrides.
package config

import (
	"fmt"
	"os"
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

// Load reads the YAML file (optional) and applies LUMO_* env overrides.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return cfg, err
		}
		if err == nil {
			if err := yaml.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("%s: %w", path, err)
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
