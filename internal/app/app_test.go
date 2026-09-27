package app

import (
	"math"
	"testing"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

func TestEaster(t *testing.T) {
	cases := map[int][2]string{
		2024: {"2024-03-31", "2024-05-05"},
		2025: {"2025-04-20", "2025-04-20"},
		2026: {"2026-04-05", "2026-04-12"},
		2027: {"2027-03-28", "2027-05-02"},
	}
	for y, want := range cases {
		if got := westernEaster(y).Format("2006-01-02"); got != want[0] {
			t.Errorf("western easter %d = %s, want %s", y, got, want[0])
		}
		if got := orthodoxEaster(y).Format("2006-01-02"); got != want[1] {
			t.Errorf("orthodox easter %d = %s, want %s", y, got, want[1])
		}
	}
	if got := blackFriday(2026).Format("2006-01-02"); got != "2026-11-27" {
		t.Errorf("black friday 2026 = %s", got)
	}
}

func TestPsychPrice(t *testing.T) {
	cases := []struct {
		raw   float64
		style domain.RoundingStyle
		want  string
	}{
		{23.4, domain.RoundCents99, "23.99"},
		{1.23, domain.RoundCents99, "1.29"},
		{243, domain.RoundCents99, "239.99"},
		{1287, domain.RoundCents99, "1299.00"},
		{123, domain.RoundInteger9, "129.00"},
		{1612, domain.RoundInteger9, "1599.00"},
	}
	for _, c := range cases {
		if got := domain.PsychPrice(c.raw, c.style).String(); got != c.want {
			t.Errorf("PsychPrice(%v, %s) = %s, want %s", c.raw, c.style, got, c.want)
		}
	}
}

func TestFenwick(t *testing.T) {
	var f Fenwick
	ws := []float64{1, 0, 3, 2, 0.5}
	for _, w := range ws {
		f.Add(w)
	}
	if math.Abs(f.Total()-6.5) > 1e-9 {
		t.Fatalf("total = %v", f.Total())
	}
	if got := f.Find(0.5); got != 0 {
		t.Errorf("Find(0.5) = %d", got)
	}
	if got := f.Find(1.5); got != 2 {
		t.Errorf("Find(1.5) = %d", got)
	}
	if got := f.Find(4.2); got != 3 {
		t.Errorf("Find(4.2) = %d", got)
	}
	f.Set(2, 0)
	if got := f.Find(1.5); got != 3 {
		t.Errorf("after Set, Find(1.5) = %d", got)
	}
	f.Rebuild()
	if math.Abs(f.Total()-3.5) > 1e-9 {
		t.Errorf("after rebuild total = %v", f.Total())
	}
	// sampling frequencies follow weights
	r := NewRand(1)
	counts := make([]int, len(ws))
	for i := 0; i < 100000; i++ {
		counts[f.Find(r.Float64()*f.Total())]++
	}
	if counts[1] != 0 || counts[2] != 0 {
		t.Errorf("zero-weight items sampled: %v", counts)
	}
	if ratio := float64(counts[3]) / float64(counts[0]); ratio < 1.8 || ratio > 2.2 {
		t.Errorf("unexpected ratio %v (%v)", ratio, counts)
	}
}

func TestFeminine(t *testing.T) {
	cases := map[[2]string]string{
		{"pl", "Kowalski"}:  "Kowalska",
		{"pl", "Nowak"}:     "Nowak",
		{"cs", "Novák"}:     "Nováková",
		{"cs", "Černý"}:     "Černá",
		{"cs", "Procházka"}: "Procházková",
		{"cs", "Hájek"}:     "Hájková",
		{"ru", "Ivanov"}:    "Ivanova",
		{"de", "Müller"}:    "Müller",
	}
	for in, want := range cases {
		if got := feminine(in[0], in[1]); got != want {
			t.Errorf("feminine(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestLifeFactor(t *testing.T) {
	ls := domain.LifecycleSpec{RampDays: 10, PlateauDays: 100, DecayDays: 50, Floor: 0.05}
	if v := lifeFactor(ls, 1, -1); v != 0 {
		t.Errorf("before launch = %v", v)
	}
	if v := lifeFactor(ls, 1, 50); v != 1 {
		t.Errorf("plateau = %v", v)
	}
	if v := lifeFactor(ls, 1, 10000); v != 0.05 {
		t.Errorf("floor = %v", v)
	}
}

func TestClockScaling(t *testing.T) {
	c := NewClock(60, time.Minute, true)
	wall := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	sim := wall
	c.Anchor(sim, wall)
	target, fast := c.Target(sim, wall.Add(10*time.Second))
	if fast || !target.Equal(sim.Add(10*time.Minute)) {
		t.Errorf("target = %v fast=%v", target, fast)
	}
	c.SetSpeed(1, sim, wall)
	// far behind wall time -> catch up
	if _, fast := c.Target(sim, wall.Add(time.Hour)); !fast {
		t.Errorf("expected catch-up")
	}
}
