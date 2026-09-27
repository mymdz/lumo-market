package app

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mymdz/lumo-market/internal/domain"
)

// calendar holds deterministic, per-year schedules: public holidays,
// marketing campaigns, weather events and incidents.
type calendar struct {
	w     *World
	years map[int]*yearCal
}

type yearCal struct {
	year      int
	holidays  map[string]map[int]bool // country -> yearday
	campaigns []*domain.Campaign
	coupons   []*domain.Coupon
	weather   []weatherEvent
	incidents []incident
}

type weatherEvent struct {
	kind      string // heat | cold
	start     time.Time
	end       time.Time
	countries map[string]bool
	strength  float64
}

type incident struct {
	kind      string
	start     time.Time
	end       time.Time
	target    string
	strength  float64
	countries map[string]bool
	handled   bool
}

func (i *incident) active(t time.Time) bool { return !t.Before(i.start) && t.Before(i.end) }

func newCalendar(w *World) *calendar { return &calendar{w: w, years: map[int]*yearCal{}} }

func (c *calendar) year(y int) *yearCal {
	if yc, ok := c.years[y]; ok {
		return yc
	}
	yc := c.generate(y)
	c.years[y] = yc
	return yc
}

func hashSeed(seed uint64, parts ...string) uint64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d", seed)
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return h.Sum64()
}

// ---- movable feasts ----

func westernEaster(y int) time.Time {
	a := y % 19
	b := y / 100
	c := y % 100
	d := b / 4
	e := b % 4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i := c / 4
	k := c % 4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func orthodoxEaster(y int) time.Time {
	a := y % 4
	b := y % 7
	c := y % 19
	d := (19*c + 15) % 30
	e := (2*a + 4*b - d + 34) % 7
	month := (d + e + 114) / 31
	day := (d+e+114)%31 + 1
	julian := time.Date(y, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return julian.AddDate(0, 0, 13)
}

func nthWeekday(y int, m time.Month, wd time.Weekday, n int) time.Time {
	t := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	for t.Weekday() != wd {
		t = t.AddDate(0, 0, 1)
	}
	return t.AddDate(0, 0, 7*(n-1))
}

func blackFriday(y int) time.Time {
	return nthWeekday(y, time.November, time.Thursday, 4).AddDate(0, 0, 1)
}

func (c *calendar) generate(y int) *yearCal {
	w := c.w
	r := NewRand(hashSeed(w.cfg.Seed, "calendar", fmt.Sprint(y)))
	yc := &yearCal{year: y, holidays: map[string]map[int]bool{}}

	easter := westernEaster(y)
	orth := orthodoxEaster(y)
	for _, m := range w.markets {
		days := map[int]bool{}
		for _, h := range m.spec.Holidays {
			var d time.Time
			switch h {
			case "easter":
				d = easter
			case "easter_monday":
				d = easter.AddDate(0, 0, 1)
			case "good_friday":
				d = easter.AddDate(0, 0, -2)
			case "ascension":
				d = easter.AddDate(0, 0, 39)
			case "whit_monday":
				d = easter.AddDate(0, 0, 50)
			case "corpus_christi":
				d = easter.AddDate(0, 0, 60)
			case "orthodox_easter":
				d = orth
			case "orthodox_easter_monday":
				d = orth.AddDate(0, 0, 1)
			default:
				var mm, dd int
				if _, err := fmt.Sscanf(h, "%d-%d", &mm, &dd); err != nil {
					continue
				}
				d = time.Date(y, time.Month(mm), dd, 0, 0, 0, 0, time.UTC)
			}
			days[d.YearDay()] = true
		}
		yc.holidays[m.Code] = days
	}

	// ---- campaigns ----
	idx := 0
	add := func(name, typ string, from, to time.Time, traffic, disc, share float64, tags []string, depts []string) *domain.Campaign {
		idx++
		cp := &domain.Campaign{
			ID: int32(y*100 + idx), Name: name, Type: typ, StartsAt: from, EndsAt: to,
			DiscountPct: math.Round(disc*1000) / 10, CreatedAt: from.AddDate(0, 0, -r.IntRange(20, 45)),
			Traffic: traffic, Tags: tags, Share: share,
		}
		for _, dn := range depts {
			for _, d := range w.depts {
				if strings.Contains(d.name, dn) {
					cp.Depts = append(cp.Depts, d.idx)
				}
			}
		}
		yc.campaigns = append(yc.campaigns, cp)
		return cp
	}
	cidx := 0
	coupon := func(cp *domain.Campaign, code, typ string, val, minOrder float64, from, to time.Time, kind string) {
		cidx++
		cpID := int32(0)
		if cp != nil {
			cpID = cp.ID
		}
		cu := &domain.Coupon{
			ID: int32(y*100 + 50 + cidx), Code: code, CampaignID: cpID, DiscountType: typ, DiscountValue: val,
			MinOrder: domain.MoneyFromFloat(minOrder), ValidFrom: from, ValidTo: to, Kind: kind,
			CreatedAt: from.AddDate(0, 0, -7), UpdatedAt: from.AddDate(0, 0, -7),
		}
		if cp != nil {
			cu.Countries = cp.Countries
		}
		yc.coupons = append(yc.coupons, cu)
	}
	d := func(m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }
	endOf := func(t time.Time) time.Time { return t.Add(24*time.Hour - time.Second) }
	yy := y % 100

	add("Winter Sale", "sale", d(1, 2), endOf(d(1, 31)), 1.08, 0.25, 0.3, nil, []string{"Fashion", "Home", "Electronics", "Sports"})
	add("Valentine's Day", "holiday", d(2, 1), endOf(d(2, 14)), 1.03, 0.15, 0.2, []string{"valentine"}, []string{"Jewellery", "Beauty", "Women"})
	add("Easter Specials", "holiday", easter.AddDate(0, 0, -12), endOf(easter.AddDate(0, 0, -1)), 1.03, 0.15, 0.2, []string{"easter"}, []string{"Grocery", "Toys", "Kids"})
	sg := add("Spring Garden Days", "sale", d(3, 15), endOf(d(4, 15)), 1.02, 0.2, 0.25, nil, []string{"Garden", "DIY"})
	coupon(sg, fmt.Sprintf("GARDEN%02d", yy), "percent", 10, 50, sg.StartsAt, sg.EndsAt, "campaign")
	md := nthWeekday(y, time.May, time.Sunday, 2)
	add("Mother's Day Gifts", "holiday", md.AddDate(0, 0, -10), endOf(md.AddDate(0, 0, -1)), 1.02, 0.15, 0.2, []string{"gift"}, []string{"Beauty", "Jewellery"})
	if y%2 == 0 {
		name := "Football Championship"
		add(name, "event", d(6, 12), endOf(d(7, 14)), 1.02, 0.12, 0.2, []string{"football"}, []string{"Electronics", "Sports", "Grocery"})
	}
	ld := nthWeekday(y, time.July, time.Tuesday, 2)
	add("Lumo Days Early Deals", "sale", ld.AddDate(0, 0, -1), endOf(ld.AddDate(0, 0, -1)), 1.15, 0.2, 0.15, nil, nil)
	add("Lumo Days", "mega_sale", ld, endOf(ld.AddDate(0, 0, 1)), 1.9, 0.3, 0.35, []string{"gift", "blackfriday"}, nil)
	add("Summer Sale", "sale", d(7, 1), endOf(d(8, 10)), 1.05, 0.3, 0.35, nil, []string{"Fashion", "Garden", "Sports"})
	bts := add("Back to School", "season", d(8, 16), endOf(d(9, 15)), 1.04, 0.15, 0.25, []string{"school"}, []string{"Books", "Kids", "Electronics"})
	coupon(bts, fmt.Sprintf("SCHOOL%02d", yy), "percent", 10, 40, bts.StartsAt, bts.EndsAt, "campaign")
	add("Halloween", "holiday", d(10, 15), endOf(d(10, 31)), 1.0, 0.15, 0.3, []string{"halloween"}, []string{"Toys"})
	sd := add("Singles Day", "mega_sale", d(11, 11), endOf(d(11, 11)), 1.3, 0.11, 0.15, nil, nil)
	coupon(sd, "SINGLES11", "percent", 11, 50, sd.StartsAt, sd.EndsAt, "campaign")
	bf := blackFriday(y)
	bfc := add("Black Friday Week", "mega_sale", bf.AddDate(0, 0, -4), endOf(bf.AddDate(0, 0, 3)), 1.0, 0.3, 0.4, []string{"blackfriday", "gift"}, nil)
	coupon(bfc, fmt.Sprintf("BLACKFRIDAY%02d", yy), "percent", 10, 100, bf, endOf(bf.AddDate(0, 0, 3)), "campaign")
	xm := add("Christmas Gifts", "holiday", d(12, 1), endOf(d(12, 21)), 1.0, 0.15, 0.2, []string{"gift", "xmas"}, nil)
	coupon(xm, fmt.Sprintf("XMAS%02d", yy), "fixed", 10, 100, d(12, 1), endOf(d(12, 10)), "campaign")
	add("After-Christmas Sale", "sale", d(12, 26), endOf(d(12, 31)), 1.2, 0.3, 0.3, nil, []string{"Fashion", "Electronics", "Home"})

	// brand weeks
	if len(w.brands) > 0 {
		for i := 0; i < 6; i++ {
			b := w.brands[r.IntN(len(w.brands))]
			if b.PrivateLabel {
				continue
			}
			s := d(1, 1).AddDate(0, 0, r.IntRange(20, 330))
			cp := add("Brand Week: "+b.Name, "brand_week", s, endOf(s.AddDate(0, 0, 6)), 1.0, 0.2, 1.0, nil, nil)
			cp.BrandID = b.ID
		}
	}
	// influencer drops
	nInf := r.Poisson(22)
	codes := map[string]bool{}
	for i := 0; i < nInf; i++ {
		s := d(1, 1).Add(time.Duration(r.Range(0, 364*24)) * time.Hour)
		cp := add("Influencer Collab", "influencer", s, s.AddDate(0, 0, 7), 1.0, 0, 0, nil, nil)
		n := r.IntRange(1, 3)
		for j := 0; j < n; j++ {
			m := w.markets[r.WeightedIndex(marketWeights(w))]
			cp.Countries = append(cp.Countries, m.Code)
		}
		cp.AcqBoost = r.Range(1.2, 1.7)
		name := Pick(r, w.ref.Texts.Influencers)
		pct := Pick(r, []float64{10, 15, 15, 20})
		code := fmt.Sprintf("%s%d", name, int(pct))
		if codes[code] {
			code = fmt.Sprintf("%s%d%02d", name, int(pct), yy)
		}
		if codes[code] {
			continue
		}
		codes[code] = true
		cp.Name = "Influencer Collab: " + name
		coupon(cp, code, "percent", pct, 20, s, s.AddDate(0, 0, 7), "influencer")
	}

	// ---- weather ----
	clusters := [][]string{{"ES", "PT", "IT"}, {"FR", "BE", "NL", "IE"}, {"DE", "AT", "CZ", "PL", "MD"}, {"SE", "DK", "FI"}}
	for _, cl := range clusters {
		set := map[string]bool{}
		for _, cc := range cl {
			set[cc] = true
		}
		for i, n := 0, r.IntRange(1, 3); i < n; i++ {
			s := d(6, 10).AddDate(0, 0, r.IntRange(0, 70))
			yc.weather = append(yc.weather, weatherEvent{kind: "heat", start: s, end: s.AddDate(0, 0, r.IntRange(4, 10)), countries: set, strength: r.Range(2, 4.5)})
		}
		for i, n := 0, r.IntRange(1, 2); i < n; i++ {
			var s time.Time
			if r.Bool(0.6) {
				s = d(1, 5).AddDate(0, 0, r.IntRange(0, 45))
			} else {
				s = d(12, 1).AddDate(0, 0, r.IntRange(0, 25))
			}
			yc.weather = append(yc.weather, weatherEvent{kind: "cold", start: s, end: s.AddDate(0, 0, r.IntRange(4, 9)), countries: set, strength: r.Range(1.8, 3)})
		}
	}

	// ---- incidents ----
	randTime := func() time.Time { return d(1, 1).Add(time.Duration(r.Range(0, 365*24*3600)) * time.Second) }
	payMethods := []string{"card", "card", "paypal", "klarna", "ideal", "blik", "bancontact", "apple_pay"}
	for i, n := 0, r.Poisson(6); i < n; i++ {
		s := randTime()
		yc.incidents = append(yc.incidents, incident{kind: "payment_outage", start: s, end: s.Add(time.Duration(r.Range(40, 240)) * time.Minute), target: Pick(r, payMethods), strength: r.Range(0.7, 0.95)})
	}
	for i, n := 0, r.Poisson(3); i < n; i++ {
		s := randTime()
		yc.incidents = append(yc.incidents, incident{kind: "site_outage", start: s, end: s.Add(time.Duration(r.Range(15, 90)) * time.Minute), strength: 0.03})
	}
	for i, n := 0, r.Poisson(4); i < n; i++ {
		s := randTime()
		yc.incidents = append(yc.incidents, incident{kind: "site_degraded", start: s, end: s.Add(time.Duration(r.Range(60, 240)) * time.Minute), strength: r.Range(0.4, 0.7)})
	}
	if r.Bool(0.6) { // Black Friday traffic brings the site to its knees
		s := bf.Add(time.Duration(r.Range(8, 12)) * time.Hour)
		yc.incidents = append(yc.incidents, incident{kind: "site_degraded", start: s, end: s.Add(time.Duration(r.Range(60, 180)) * time.Minute), strength: r.Range(0.45, 0.7)})
	}
	for i, n := 0, r.Poisson(2); i < n; i++ {
		s := randTime()
		wh := Pick(r, []string{"LEJ1", "LEJ1", "TLB1", "POZ1", "ZAZ1"})
		yc.incidents = append(yc.incidents, incident{kind: "warehouse_delay", start: s, end: s.Add(time.Duration(r.IntRange(2, 6)) * 24 * time.Hour), target: wh, strength: float64(r.IntRange(1, 3))})
	}
	carrierNames := make([]string, 0, len(w.carriers))
	for k := range w.carriers {
		carrierNames = append(carrierNames, k)
	}
	sort.Strings(carrierNames)
	for i, n := 0, r.Poisson(3); i < n; i++ {
		s := randTime()
		yc.incidents = append(yc.incidents, incident{kind: "carrier_strike", start: s, end: s.Add(time.Duration(r.IntRange(2, 5)) * 24 * time.Hour), target: Pick(r, carrierNames), strength: float64(r.IntRange(2, 5))})
	}
	for i, n := 0, r.Poisson(1.5); i < n; i++ {
		s := randTime()
		yc.incidents = append(yc.incidents, incident{kind: "pricing_bug", start: s, end: s.Add(time.Duration(r.Range(60, 180)) * time.Minute), strength: r.Range(0.01, 0.1)})
	}
	for i, n := 0, r.Poisson(3); i < n; i++ {
		s := randTime()
		yc.incidents = append(yc.incidents, incident{kind: "bot_attack", start: s, end: s.Add(time.Duration(r.Range(2, 8)) * time.Hour), strength: r.Range(300, 2500)})
	}
	for i, n := 0, r.Poisson(1.5); i < n; i++ {
		s := randTime()
		yc.incidents = append(yc.incidents, incident{kind: "android_crash", start: s, end: s.Add(time.Duration(r.Range(8, 36)) * time.Hour), strength: 0.3})
	}
	if w.cfg.Dirt > 0 {
		tables := []string{"orders", "shipments", "payments", "customers", "offers", "carts"}
		for i, n := 0, r.Poisson(4*w.cfg.Dirt); i < n; i++ {
			s := randTime()
			yc.incidents = append(yc.incidents, incident{kind: "updated_at_bug", start: s, end: s.Add(time.Duration(r.Range(12, 120)) * time.Hour), target: Pick(r, tables)})
		}
		for i, n := 0, r.Poisson(3*w.cfg.Dirt); i < n; i++ {
			s := randTime()
			yc.incidents = append(yc.incidents, incident{kind: "duplicate_events", start: s, end: s.Add(time.Duration(r.Range(6, 72)) * time.Hour), strength: r.Range(0.05, 0.3)})
		}
		for i, n := 0, r.Poisson(4*w.cfg.Dirt); i < n; i++ {
			s := randTime()
			yc.incidents = append(yc.incidents, incident{kind: "late_webhooks", start: s, end: s.Add(time.Duration(r.Range(3, 24)) * time.Hour), strength: r.Range(60, 360)})
		}
	}
	sort.Slice(yc.incidents, func(i, j int) bool { return yc.incidents[i].start.Before(yc.incidents[j].start) })
	return yc
}

func marketWeights(w *World) []float64 {
	out := make([]float64, len(w.markets))
	for i, m := range w.markets {
		out[i] = m.spec.Weight
	}
	return out
}

// ---- queries used by the demand model ----

func (c *calendar) around(t time.Time) []*yearCal {
	y := t.Year()
	out := []*yearCal{c.year(y)}
	if t.Month() == time.January {
		out = append(out, c.year(y-1))
	}
	return out
}

func (c *calendar) activeCampaigns(t time.Time) []*domain.Campaign {
	var out []*domain.Campaign
	for _, yc := range c.around(t) {
		for _, cp := range yc.campaigns {
			if !t.Before(cp.StartsAt) && !t.After(cp.EndsAt) {
				out = append(out, cp)
			}
		}
	}
	return out
}

func (c *calendar) activeIncidents(t time.Time, kind string) []*incident {
	var out []*incident
	for _, yc := range c.around(t) {
		for i := range yc.incidents {
			in := &yc.incidents[i]
			if in.kind == kind && in.active(t) {
				out = append(out, in)
			}
		}
	}
	return out
}

func (c *calendar) incidentFactor(t time.Time, kind, target string) (float64, bool) {
	for _, in := range c.activeIncidents(t, kind) {
		if target == "" || in.target == target {
			return in.strength, true
		}
	}
	return 0, false
}

func (c *calendar) isHoliday(country string, t time.Time) bool {
	return c.year(t.Year()).holidays[country][t.YearDay()]
}

var (
	hourWeekday = normalize([]float64{0.25, 0.13, 0.08, 0.06, 0.06, 0.10, 0.25, 0.55, 0.85, 1.05, 1.15, 1.20, 1.30, 1.25, 1.20, 1.20, 1.25, 1.30, 1.45, 1.70, 1.90, 1.85, 1.40, 0.75})
	hourWeekend = normalize([]float64{0.35, 0.20, 0.12, 0.08, 0.07, 0.08, 0.15, 0.30, 0.55, 0.85, 1.10, 1.25, 1.30, 1.30, 1.30, 1.30, 1.35, 1.40, 1.50, 1.65, 1.85, 1.80, 1.35, 0.80})
	dowFactor   = normalize([]float64{1.05, 1.10, 1.05, 1.02, 1.00, 0.93, 0.85}) // Sun..Sat
	monthFactor = normalize([]float64{1.04, 0.94, 0.98, 0.96, 0.95, 0.90, 0.92, 0.90, 0.98, 1.02, 1.08, 1.12})
)

func normalize(v []float64) []float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	m := s / float64(len(v))
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / m
	}
	return out
}

// sessionFactor is the multiplier of shopping sessions in a market at time t.
func (w *World) sessionFactor(m *market, t time.Time) float64 {
	lt := t.In(m.TZ)
	hour := lt.Hour()
	frac := float64(lt.Minute()) / 60
	prof := hourWeekday
	wd := lt.Weekday()
	holiday := w.cal.isHoliday(m.Code, lt)
	if wd == time.Saturday || wd == time.Sunday || holiday {
		prof = hourWeekend
	}
	h := prof[hour]*(1-frac) + prof[(hour+1)%24]*frac
	f := h * dowFactor[wd]
	// smooth month factor
	mi := int(lt.Month()) - 1
	df := float64(lt.Day()) / 31
	f *= monthFactor[mi]*(1-df) + monthFactor[(mi+1)%12]*df
	// holidays
	if holiday {
		f *= 0.85
	}
	mo, day := lt.Month(), lt.Day()
	switch {
	case mo == time.December && day == 24:
		f *= 0.4
	case mo == time.December && day == 25:
		f *= 0.55
	case mo == time.December && day == 31:
		f *= 0.6
	case mo == time.January && day == 1:
		f *= 0.7
	case mo == time.December && day >= 21 && day <= 23:
		f *= 0.8
	}
	// payday
	if day >= 25 && day <= 28 {
		f *= 1.04
	} else if day <= 3 {
		f *= 1.05
	}
	// campaigns
	for _, cp := range w.cal.activeCampaigns(t) {
		if len(cp.Countries) > 0 && !containsStr(cp.Countries, m.Code) {
			continue
		}
		f *= w.campaignTraffic(cp, lt)
	}
	// weather: heat keeps people outside
	for _, we := range w.weatherAt(t, m.Code) {
		if we.kind == "heat" {
			f *= 0.96
		} else {
			f *= 1.03
		}
	}
	if v, ok := w.cal.incidentFactor(t, "site_outage", ""); ok {
		f *= v
	}
	return f * math.Exp(m.noise)
}

func (w *World) campaignTraffic(cp *domain.Campaign, lt time.Time) float64 {
	switch cp.Name {
	case "Black Friday Week":
		bf := blackFriday(lt.Year())
		d := int(math.Floor(lt.Sub(bf).Hours() / 24))
		switch {
		case d < 0:
			return 1.3
		case d == 0:
			return 2.3
		case d == 1:
			return 1.6
		case d == 2:
			return 1.45
		default:
			return 1.9
		}
	case "Christmas Gifts":
		day := lt.Day()
		return 1.1 + 0.3*math.Min(1, float64(day)/16)
	}
	if cp.Traffic <= 0 {
		return 1
	}
	return cp.Traffic
}

func (w *World) weatherAt(t time.Time, country string) []*weatherEvent {
	var out []*weatherEvent
	for _, yc := range w.cal.around(t) {
		for i := range yc.weather {
			we := &yc.weather[i]
			if we.countries[country] && !t.Before(we.start) && t.Before(we.end) {
				out = append(out, we)
			}
		}
	}
	return out
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
