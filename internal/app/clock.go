package app

import (
	"math"
	"sync"
	"time"
)

// Clock maps wall time to simulation time with a controllable speed.
// The engine owns the simulation time; the clock only says how far it may go.
//
// Invariants:
//   - at speed 1 the simulation follows wall time; if it is behind (downtime,
//     pause, slow-down) it catches up as fast as possible;
//   - speed < 1 deliberately lags behind wall time (no catch-up);
//   - unless allowFuture is set, simulated time never passes wall time, so no
//     event can be dated in the future.
type Clock struct {
	mu          sync.Mutex
	speed       float64
	paused      bool
	allowFuture bool
	anchorWall  time.Time
	anchorSim   time.Time
	catchUpLag  time.Duration
	catchingUp  bool
}

func NewClock(speed float64, catchUpLag time.Duration, allowFuture bool) *Clock {
	if speed <= 0 {
		speed = 1
	}
	if speed > 1 && !allowFuture {
		speed = 1
	}
	return &Clock{speed: speed, catchUpLag: catchUpLag, allowFuture: allowFuture}
}

func isOne(x float64) bool { return math.Abs(x-1) < 1e-9 }

// Anchor aligns the scaled clock at the given simulation time.
func (c *Clock) Anchor(sim, wall time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.anchorSim, c.anchorWall = sim, wall
}

// Target returns the simulation time the engine may advance to and whether
// it should run as fast as possible (catch-up).
func (c *Clock) Target(sim, wall time.Time) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused {
		c.anchorSim, c.anchorWall = sim, wall
		return sim, false
	}
	if isOne(c.speed) && sim.Before(wall.Add(-c.catchUpLag)) {
		c.catchingUp = true
		return wall, true
	}
	if c.catchingUp {
		// caught up: continue in scaled mode from here
		c.catchingUp = false
		c.anchorSim, c.anchorWall = sim, wall
	}
	t := c.anchorSim.Add(time.Duration(float64(wall.Sub(c.anchorWall)) * c.speed))
	if !c.allowFuture && t.After(wall) {
		t = wall
	}
	return t, false
}

// SetSpeed changes the speed; speeds above 1 require allowFuture.
func (c *Clock) SetSpeed(x float64, sim, wall time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if x <= 0 || (x > 1 && !isOne(x) && !c.allowFuture) {
		return false
	}
	c.speed = x
	c.anchorSim, c.anchorWall = sim, wall
	return true
}

func (c *Clock) Pause() {
	c.mu.Lock()
	c.paused = true
	c.mu.Unlock()
}

func (c *Clock) Resume(sim, wall time.Time) {
	c.mu.Lock()
	c.paused = false
	c.anchorSim, c.anchorWall = sim, wall
	c.mu.Unlock()
}

// Resync sets speed to 1 and aligns simulation time with wall time: if the
// simulation is ahead it waits, if behind it catches up.
func (c *Clock) Resync(wall time.Time) {
	c.mu.Lock()
	c.speed = 1
	c.paused = false
	c.anchorSim, c.anchorWall = wall, wall
	c.mu.Unlock()
}

func (c *Clock) State() (speed float64, paused, catchingUp bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.speed, c.paused, c.catchingUp
}

func (c *Clock) AllowFuture() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.allowFuture
}
