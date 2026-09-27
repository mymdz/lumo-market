package app

import (
	"encoding/json"
	"time"
)

// engineState is the part of the world that is not derivable from table rows.
type engineState struct {
	IDs           idGen
	Gains         map[string]float64
	LastHourly    time.Time
	LastDaily     time.Time
	NightlyDone   time.Time
	Days          []dayStatJSON
	Today         dayStatJSON
	Markets       map[string]marketState
	FX            map[string][2]float64
	EcoBackfilled bool
	Events        []string
	POWritten     []int64
	OrderCount    map[string]int
	TestCustomers []int64
}

type dayStatJSON struct {
	Day     time.Time
	Orders  int
	Revenue float64
	Target  float64
}

type marketState struct {
	Noise   float64
	SAcc    float64
	Targets []float64
	Actuals []float64
}

func (w *World) stateJSON() []byte {
	st := engineState{
		IDs: w.ids, Gains: w.gains, LastHourly: w.lastHourly, LastDaily: w.lastDaily, NightlyDone: w.nightlyDone,
		Markets: map[string]marketState{}, FX: map[string][2]float64{}, EcoBackfilled: w.ecoBackfilled,
		Events: w.events, OrderCount: w.orderCount, TestCustomers: w.testCusts,
	}
	for _, d := range w.st.days {
		st.Days = append(st.Days, dayStatJSON{Day: d.Day, Orders: d.Orders, Revenue: d.Revenue, Target: d.Target})
	}
	t := w.st.today
	st.Today = dayStatJSON{Day: t.Day, Orders: t.Orders, Revenue: t.Revenue, Target: t.Target}
	for _, m := range w.markets {
		st.Markets[m.Code] = marketState{Noise: m.noise, SAcc: m.sAcc, Targets: m.targets, Actuals: m.actuals}
	}
	for code, c := range w.currencies {
		st.FX[code] = [2]float64{c.rate, c.priceRate}
	}
	for id := range w.poWritten {
		st.POWritten = append(st.POWritten, id)
	}
	b, _ := json.Marshal(st)
	return b
}

func (w *World) loadState(b []byte) error {
	var st engineState
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	w.ids = st.IDs
	if st.Gains != nil {
		w.gains = st.Gains
	}
	w.lastHourly, w.lastDaily, w.nightlyDone = st.LastHourly, st.LastDaily, st.NightlyDone
	for _, d := range st.Days {
		w.st.days = append(w.st.days, dayStat{Day: d.Day, Orders: d.Orders, Revenue: d.Revenue, Target: d.Target})
	}
	w.st.today = dayStat{Day: st.Today.Day, Orders: st.Today.Orders, Revenue: st.Today.Revenue, Target: st.Today.Target}
	for _, m := range w.markets {
		if ms, ok := st.Markets[m.Code]; ok {
			m.noise, m.sAcc, m.targets, m.actuals = ms.Noise, ms.SAcc, ms.Targets, ms.Actuals
		}
	}
	for code, v := range st.FX {
		if c := w.currencies[code]; c != nil {
			c.rate, c.priceRate = v[0], v[1]
		}
	}
	w.ecoBackfilled = st.EcoBackfilled
	w.events = st.Events
	for _, id := range st.POWritten {
		w.poWritten[id] = true
	}
	if st.OrderCount != nil {
		w.orderCount = st.OrderCount
	}
	w.testCusts = st.TestCustomers
	return nil
}
