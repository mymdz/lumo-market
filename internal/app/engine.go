package app

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
	"github.com/mymdz/lumo-market/internal/ports"
)

// Engine drives the simulation loop and implements ports.Control.
type Engine struct {
	w     *World
	store ports.Store
	clock *Clock
	log   *slog.Logger

	mu     sync.Mutex
	status ports.Status
	simNow time.Time

	lastCheckpointSim  time.Time
	lastCheckpointWall time.Time
	lastStatus         time.Time

	catchUpFrom  time.Time // simulated time when the current catch-up started
	catchUpWall  time.Time
	catchUpNoted time.Time
	waitingNoted bool
}

func newWorld(cfg Config, ref *domain.ReferenceData, log *slog.Logger) *World {
	w := &World{
		cfg: cfg, ref: ref, log: log, rng: NewRand(cfg.Seed),
		products: map[int64]*domain.Product{}, variants: map[int64]*domain.Variant{}, offers: map[int64]*domain.Offer{},
		orders: map[int64]*domain.Order{}, carts: map[int64]*cartState{}, pos: map[int64]*domain.PurchaseOrder{},
		couponByCode: map[string]*domain.Coupon{}, suppliers: map[int32]*domain.Supplier{}, applied: map[string]bool{},
		poWritten: map[int64]bool{}, gains: map[string]float64{}, orderCount: map[string]int{},
	}
	w.st.offerSales = map[int64]int{}
	w.uow = newUOW(w)
	return w
}

func NewEngine(cfg Config, ref *domain.ReferenceData, store ports.Store, log *slog.Logger) *Engine {
	cfg.defaults()
	return &Engine{w: newWorld(cfg, ref, log), store: store, clock: NewClock(cfg.Speed, cfg.CatchUpLag, cfg.AllowFuture), log: log}
}

// Run initialises or restores the world and simulates until ctx is done.
func (e *Engine) Run(ctx context.Context) error {
	w := e.w
	if err := e.store.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	meta, err := e.store.Meta(ctx)
	if err != nil {
		return err
	}
	if meta != nil && meta.Phase == PhaseBackfill {
		e.log.Warn("previous backfill did not finish; starting over")
		if err := e.store.Reset(ctx); err != nil {
			return err
		}
		if err := e.store.EnsureSchema(ctx); err != nil {
			return err
		}
		meta = nil
	}
	if meta == nil {
		if err := e.fresh(ctx); err != nil {
			return err
		}
	} else {
		e.log.Info("restoring world from database", "sim_time", meta.SimTime.Format(time.RFC3339))
		snap, err := e.store.Load(ctx)
		if err != nil {
			return fmt.Errorf("load: %w", err)
		}
		w.rng = NewRand(hashSeed(w.cfg.Seed, "restart", meta.SimTime.String()))
		if err := w.restore(snap); err != nil {
			return fmt.Errorf("restore: %w", err)
		}
		snap = nil
		debug.FreeOSMemory()
		if err := e.store.FinalizeBulkLoad(ctx); err != nil {
			return err
		}
		e.log.Info("world restored", "customers", len(w.customers), "products", len(w.products), "offers", len(w.offers), "open_orders", len(w.orders))
	}
	wall := w.cfg.Now()
	e.clock.Anchor(w.now, wall)
	e.lastCheckpointSim = w.now
	e.lastCheckpointWall = wall
	return e.loop(ctx)
}

func (e *Engine) fresh(ctx context.Context) error {
	w := e.w
	wall := w.cfg.Now().UTC()
	w.horizon = wall.Truncate(time.Minute)
	w.start = dayOf(w.horizon.Add(-time.Duration(w.cfg.HistoryDays) * 24 * time.Hour))
	if w.cfg.HistoryDays == 0 {
		w.start = w.horizon
	}
	w.phase = PhaseBackfill
	if !w.start.Before(w.horizon) {
		w.phase = PhaseLive
	}
	if err := w.initReference(); err != nil {
		return err
	}
	if err := w.initWarehouses(); err != nil {
		return err
	}
	e.log.Info("creating a new world", "history_start", w.start.Format("2006-01-02"), "horizon", w.horizon.Format(time.RFC3339), "seed", w.cfg.Seed)
	if err := w.bootstrap(); err != nil {
		return err
	}
	if err := e.checkpoint(ctx, w.phase == PhaseLive); err != nil {
		return err
	}
	if w.phase == PhaseLive {
		return e.store.FinalizeBulkLoad(ctx)
	}
	return nil
}

func (e *Engine) checkpoint(ctx context.Context, final bool) error {
	w := e.w
	b := w.uow.take(final)
	if err := e.store.Apply(ctx, b); err != nil {
		return err
	}
	w.uow.afterApply(b)
	e.lastCheckpointSim = w.now
	e.lastCheckpointWall = w.cfg.Now()
	w.needCheckpoint = false
	return nil
}

func (e *Engine) loop(ctx context.Context) error {
	w := e.w
	backfillStarted := time.Now()
	for {
		select {
		case <-ctx.Done():
			if w.live() {
				e.log.Info("shutting down, writing last checkpoint")
				cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := e.checkpoint(cctx, false); err != nil {
					return err
				}
				return e.store.Flush(cctx)
			}
			return ctx.Err()
		default:
		}
		wall := w.cfg.Now()
		var target time.Time
		if w.phase == PhaseBackfill {
			target = w.horizon
		} else {
			var fast bool
			target, fast = e.clock.Target(w.now, wall)
			e.trackCatchUp(fast, wall)
		}
		if !w.now.Before(target) {
			if w.live() && wall.Sub(e.lastCheckpointWall) >= w.cfg.LiveFlush {
				if err := e.checkpoint(ctx, false); err != nil {
					return err
				}
			}
			e.publishStatus(wall, true)
			time.Sleep(50 * time.Millisecond)
			continue
		}
		stepEnd := w.now.Truncate(w.cfg.Tick).Add(w.cfg.Tick)
		if stepEnd.After(target) {
			stepEnd = target
		}
		w.generateArrivals(w.now, stepEnd)
		e.processUntil(stepEnd)
		w.now = stepEnd
		w.runJobs()

		if w.phase == PhaseBackfill {
			if w.now.Sub(e.lastCheckpointSim) >= w.cfg.BackfillCheckpoint || w.uow.b.Size() > 400_000 {
				if err := e.checkpoint(ctx, false); err != nil {
					return err
				}
			}
			if !w.now.Before(w.horizon) {
				if err := e.finishBackfill(ctx, backfillStarted); err != nil {
					return err
				}
			}
		} else if wall.Sub(e.lastCheckpointWall) >= w.cfg.LiveFlush || w.uow.b.Size() > 50_000 {
			if err := e.checkpoint(ctx, false); err != nil {
				return err
			}
		}
		e.publishStatus(wall, false)
	}
}

func (e *Engine) processUntil(end time.Time) {
	w := e.w
	endNs := end.UnixNano()
	for {
		at, ok := w.q.PeekAt()
		if !ok || at >= endNs {
			return
		}
		ev := w.q.Pop()
		t := time.Unix(0, ev.at).UTC()
		if t.After(w.now) {
			w.now = t
		}
		w.runJobs()
		switch ev.kind {
		case evSession:
			w.onSession(ev)
		case evCart:
			w.onCart(ev)
		case evOrder:
			w.onOrder(ev)
		case evPO:
			w.onPO(ev)
		case evReplenish:
			w.onReplenish(ev)
		}
	}
}

func (e *Engine) finishBackfill(ctx context.Context, started time.Time) error {
	w := e.w
	e.log.Info("backfill simulated, writing final state", "took", time.Since(started).Round(time.Second).String())
	if err := e.dumpState(ctx); err != nil {
		return err
	}
	w.phase = PhaseLive
	if err := e.checkpoint(ctx, true); err != nil {
		return err
	}
	if err := e.store.Flush(ctx); err != nil {
		return err
	}
	// give the encoding buffers back to the OS before Postgres builds indexes
	debug.FreeOSMemory()
	e.log.Info("creating indexes and constraints")
	if err := e.store.FinalizeBulkLoad(ctx); err != nil {
		return err
	}
	e.clock.Anchor(w.now, w.cfg.Now())
	e.log.Info("backfill complete, switching to live mode", "took", time.Since(started).Round(time.Second).String(), "customers", len(w.customers), "products", len(w.products))
	return nil
}

// dumpState writes the simulator-internal state of every entity (nothing of
// it is written during the backfill) and coalesced customer updates in
// bounded chunks, still flagged as backfill so that a crash restarts cleanly.
func (e *Engine) dumpState(ctx context.Context) error {
	w := e.w
	const chunk = 100_000
	flush := func(b *ports.Batch) error {
		b.At, b.Phase, b.SimTime, b.Horizon, b.Start, b.Seed = w.now, PhaseBackfill, w.now, w.horizon, w.start, w.cfg.Seed
		b.StateJSON = w.stateJSON()
		return e.store.Apply(ctx, b)
	}
	b := &ports.Batch{}
	for _, c := range w.customers {
		if c == nil {
			continue
		}
		b.CustomerTraits = append(b.CustomerTraits, c)
		if _, ok := w.uow.custs[c.ID]; ok && !w.uow.newCusts[c.ID] {
			b.CustomerUpdates = append(b.CustomerUpdates, c)
		}
		if len(b.CustomerTraits) >= chunk {
			if err := flush(b); err != nil {
				return err
			}
			b = &ports.Batch{}
		}
	}
	for _, p := range w.products {
		b.ProductTraits = append(b.ProductTraits, p)
		if len(b.ProductTraits) >= chunk {
			if err := flush(b); err != nil {
				return err
			}
			b = &ports.Batch{}
		}
	}
	for _, o := range w.offers {
		b.OfferTraits = append(b.OfferTraits, o)
		if len(b.OfferTraits) >= chunk {
			if err := flush(b); err != nil {
				return err
			}
			b = &ports.Batch{}
		}
	}
	if err := flush(b); err != nil {
		return err
	}
	// already written
	w.uow.custs = map[int64]*domain.Customer{}
	w.uow.traits = map[int64]*domain.Customer{}
	w.uow.ptraits = map[int64]*domain.Product{}
	w.uow.otraits = map[int64]*domain.Offer{}
	return e.store.Flush(ctx)
}

// trackCatchUp logs the start, progress and end of catching up on downtime.
func (e *Engine) trackCatchUp(fast bool, wall time.Time) {
	w := e.w
	switch {
	case fast && e.catchUpFrom.IsZero():
		e.catchUpFrom, e.catchUpWall, e.catchUpNoted = w.now, wall, wall
		e.log.Info("simulation is behind wall time, catching up", "from", w.now.Format(time.RFC3339), "gap", wall.Sub(w.now).Round(time.Second).String())
	case fast && wall.Sub(e.catchUpNoted) >= 15*time.Second:
		e.catchUpNoted = wall
		done := w.now.Sub(e.catchUpFrom).Seconds()
		total := wall.Sub(e.catchUpFrom).Seconds()
		e.log.Info("catching up", "sim_time", w.now.Format("2006-01-02 15:04"), "progress_pct", math.Round(done/math.Max(total, 1)*1000)/10, "open_orders", len(w.orders))
	case !fast && !e.catchUpFrom.IsZero():
		e.log.Info("caught up, running in real time", "simulated", w.now.Sub(e.catchUpFrom).Round(time.Second).String(), "took", wall.Sub(e.catchUpWall).Round(time.Second).String())
		e.catchUpFrom = time.Time{}
	}
	if !fast && w.now.After(wall.Add(time.Second)) && !e.clock.AllowFuture() && !e.waitingNoted {
		e.waitingNoted = true
		e.log.Warn("simulated time is ahead of wall time (a previous run used speed > 1); waiting for wall time to catch up", "sim_time", w.now.Format(time.RFC3339))
	}
}

// ---------- control port ----------

func (e *Engine) publishStatus(wall time.Time, idle bool) {
	if wall.Sub(e.lastStatus) < 250*time.Millisecond {
		return
	}
	e.lastStatus = wall
	w := e.w
	speed, paused, catching := e.clock.State()
	st := ports.Status{
		Phase: w.phase, SimTime: w.now, WallTime: wall, LagSeconds: math.Round(wall.Sub(w.now).Seconds()*10) / 10,
		Speed: speed, Paused: paused, CatchingUp: catching || w.phase == PhaseBackfill,
		Customers: len(w.customers), Products: len(w.products), Offers: len(w.offers), Sellers: len(w.sellers),
		OpenOrders: len(w.orders), QueueSize: w.q.Len(), OrdersToday: w.st.today.Orders,
		Migrations: append([]string{}, w.appliedIDs...), DirtLevel: w.cfg.Dirt, AllowFuture: w.cfg.AllowFuture,
	}
	sort.Strings(st.Migrations)
	for _, d := range w.st.days {
		st.OrdersPerDay = append(st.OrdersPerDay, ports.DayStat{Day: d.Day.Format("2006-01-02"), Orders: d.Orders, Revenue: math.Round(d.Revenue)})
	}
	for _, cp := range w.cal.activeCampaigns(w.now) {
		st.ActiveEvents = append(st.ActiveEvents, "campaign: "+cp.Name)
	}
	for _, kind := range []string{"payment_outage", "site_outage", "site_degraded", "warehouse_delay", "carrier_strike", "pricing_bug", "bot_attack", "android_crash", "updated_at_bug", "duplicate_events", "late_webhooks"} {
		for _, in := range w.cal.activeIncidents(w.now, kind) {
			label := "incident: " + kind
			if in.target != "" {
				label += " (" + in.target + ")"
			}
			st.ActiveEvents = append(st.ActiveEvents, label)
		}
	}
	for _, m := range w.markets {
		for _, we := range w.weatherAt(w.now, m.Code) {
			st.ActiveEvents = append(st.ActiveEvents, fmt.Sprintf("weather: %s wave in %s", we.kind, m.Code))
		}
	}
	st.ActiveEvents = append(st.ActiveEvents, w.events...)
	g := 0.0
	for _, v := range w.gains {
		g += v
	}
	if len(w.gains) > 0 {
		st.AcqGain = math.Round(g/float64(len(w.gains))*100) / 100
	}
	if w.phase == PhaseBackfill {
		total := w.horizon.Sub(w.start).Seconds()
		if total > 0 {
			st.BackfillPct = math.Round(w.now.Sub(w.start).Seconds()/total*1000) / 10
		}
	}
	e.mu.Lock()
	e.status = st
	e.simNow = w.now
	e.mu.Unlock()
}

func (e *Engine) Status() ports.Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *Engine) sim() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.simNow
}

func (e *Engine) SetSpeed(x float64) error {
	if !e.clock.SetSpeed(x, e.sim(), e.w.cfg.Now()) {
		if x > 1 {
			return fmt.Errorf("speed %.2f would date events in the future; set allow_future: true (LUMO_ALLOW_FUTURE=true) to allow it", x)
		}
		return fmt.Errorf("speed must be positive")
	}
	return nil
}
func (e *Engine) Pause()  { e.clock.Pause() }
func (e *Engine) Resume() { e.clock.Resume(e.sim(), e.w.cfg.Now()) }
func (e *Engine) Resync() { e.clock.Resync(e.w.cfg.Now()) }
