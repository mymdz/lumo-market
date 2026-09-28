package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadLayers(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	write(t, base, "history_days: 100\norders_per_day_start: 4000\nseed: 7\n")

	cfg, err := Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HistoryDays != 100 || cfg.OrdersStart != 4000 || cfg.InitialProducts != 20000 {
		t.Fatalf("base only: %+v", cfg)
	}

	write(t, filepath.Join(dir, "config.local.yaml"), "orders_per_day_start: 2000\n")
	t.Setenv("LUMO_SEED", "9")
	cfg, err = Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OrdersStart != 2000 {
		t.Errorf("local should override base: orders_per_day_start = %v", cfg.OrdersStart)
	}
	if cfg.HistoryDays != 100 {
		t.Errorf("keys absent from local must keep base value: history_days = %v", cfg.HistoryDays)
	}
	if cfg.Seed != 9 {
		t.Errorf("env should override both files: seed = %v", cfg.Seed)
	}
}

func TestLocalPath(t *testing.T) {
	for in, want := range map[string]string{
		"config.yaml":          "config.local.yaml",
		"/etc/lumo/config.yml": "/etc/lumo/config.local.yml",
		"cfg":                  "cfg.local",
	} {
		if got := LocalPath(in); got != want {
			t.Errorf("LocalPath(%q) = %q, want %q", in, got, want)
		}
	}
}
