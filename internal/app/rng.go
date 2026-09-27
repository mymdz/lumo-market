package app

import (
	"math"
	"math/rand/v2"
	"sort"
)

// Rand wraps a deterministic PRNG with distribution helpers.
type Rand struct{ *rand.Rand }

func NewRand(seed uint64) *Rand {
	return &Rand{rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

func (r *Rand) Bool(p float64) bool { return r.Float64() < p }

func (r *Rand) Range(a, b float64) float64 { return a + (b-a)*r.Float64() }

// IntRange returns an int in [a, b].
func (r *Rand) IntRange(a, b int) int {
	if b <= a {
		return a
	}
	return a + r.IntN(b-a+1)
}

func (r *Rand) Norm(mu, sd float64) float64 { return mu + sd*r.NormFloat64() }

// LogNorm returns a log-normal value with the given median.
func (r *Rand) LogNorm(median, sigma float64) float64 {
	return median * math.Exp(sigma*r.NormFloat64())
}

func (r *Rand) Exp(mean float64) float64 { return r.ExpFloat64() * mean }

func (r *Rand) Poisson(lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	if lambda > 40 {
		v := math.Round(r.Norm(lambda, math.Sqrt(lambda)))
		if v < 0 {
			return 0
		}
		return int(v)
	}
	l := math.Exp(-lambda)
	k := 0
	p := 1.0
	for {
		p *= r.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

// Pareto with minimum xm and shape alpha.
func (r *Rand) Pareto(xm, alpha float64) float64 {
	u := r.Float64()
	if u < 1e-12 {
		u = 1e-12
	}
	return xm / math.Pow(u, 1/alpha)
}

func (r *Rand) Clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// WeightedIndex picks an index proportionally to weights (linear scan).
func (r *Rand) WeightedIndex(w []float64) int {
	total := 0.0
	for _, x := range w {
		if x > 0 {
			total += x
		}
	}
	if total <= 0 {
		return r.IntN(len(w))
	}
	x := r.Float64() * total
	for i, v := range w {
		if v <= 0 {
			continue
		}
		x -= v
		if x < 0 {
			return i
		}
	}
	return len(w) - 1
}

func Pick[T any](r *Rand, s []T) T { return s[r.IntN(len(s))] }

// Picker is an immutable weighted sampler.
type Picker[T any] struct {
	items []T
	cum   []float64
	total float64
}

func NewPicker[T any](items []T, weights []float64) *Picker[T] {
	p := &Picker[T]{items: items, cum: make([]float64, len(items))}
	for i := range items {
		w := 1.0
		if i < len(weights) {
			w = weights[i]
		}
		if w < 0 {
			w = 0
		}
		p.total += w
		p.cum[i] = p.total
	}
	return p
}

// PickerFromMap builds a picker from a map with deterministic key order.
func PickerFromMap(m map[string]float64) *Picker[string] {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w := make([]float64, len(keys))
	for i, k := range keys {
		w[i] = m[k]
	}
	return NewPicker(keys, w)
}

func (p *Picker[T]) Len() int { return len(p.items) }

func (p *Picker[T]) Pick(r *Rand) T {
	if p.total <= 0 {
		return p.items[r.IntN(len(p.items))]
	}
	x := r.Float64() * p.total
	i := sort.SearchFloat64s(p.cum, x)
	if i >= len(p.items) {
		i = len(p.items) - 1
	}
	return p.items[i]
}

func (p *Picker[T]) Items() []T { return p.items }

// Fenwick is a binary indexed tree over float weights supporting appends,
// point updates and sampling proportional to weight.
type Fenwick struct {
	tree []float64 // 1-based
	vals []float64 // 0-based
}

func (f *Fenwick) Len() int { return len(f.vals) }

func (f *Fenwick) prefix(i int) float64 { // sum of vals[0..i-1]
	s := 0.0
	for ; i > 0; i -= i & -i {
		s += f.tree[i]
	}
	return s
}

// Add appends a value and returns its index.
func (f *Fenwick) Add(w float64) int {
	if len(f.tree) == 0 {
		f.tree = append(f.tree, 0)
	}
	i := len(f.vals) + 1
	f.vals = append(f.vals, w)
	lb := i & -i
	f.tree = append(f.tree, f.prefix(i-1)-f.prefix(i-lb)+w)
	return i - 1
}

func (f *Fenwick) Get(i int) float64 { return f.vals[i] }

func (f *Fenwick) Set(i int, w float64) {
	d := w - f.vals[i]
	if d == 0 {
		return
	}
	f.vals[i] = w
	for j := i + 1; j < len(f.tree); j += j & -j {
		f.tree[j] += d
	}
}

func (f *Fenwick) Total() float64 { return f.prefix(len(f.vals)) }

// Find returns the index whose cumulative range contains x (0 <= x < Total).
func (f *Fenwick) Find(x float64) int {
	n := len(f.vals)
	pos := 0
	step := 1
	for step*2 <= n {
		step *= 2
	}
	for ; step > 0; step /= 2 {
		if pos+step <= n && f.tree[pos+step] <= x {
			pos += step
			x -= f.tree[pos]
		}
	}
	if pos >= n {
		pos = n - 1
	}
	return pos
}

// Rebuild recomputes the tree from values to remove float drift.
func (f *Fenwick) Rebuild() {
	n := len(f.vals)
	f.tree = make([]float64, n+1)
	for i := 1; i <= n; i++ {
		f.tree[i] += f.vals[i-1]
		j := i + (i & -i)
		if j <= n {
			f.tree[j] += f.tree[i]
		}
	}
}
