package app

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/mymdz/lumo-market/internal/domain"
)

type segDef struct {
	share, rate, sens, ret, coupon, conv, basket, budget, oneTimer, life float64
}

var segDefs = [domain.NumSegments]segDef{
	domain.SegRegular:    {share: 0.30, rate: 0.030, sens: 0.50, ret: 1.0, coupon: 0.30, conv: 1.00, basket: 1.8, budget: 90, oneTimer: 0.42, life: 700},
	domain.SegBargain:    {share: 0.17, rate: 0.032, sens: 0.85, ret: 0.9, coupon: 0.80, conv: 0.90, basket: 1.9, budget: 60, oneTimer: 0.40, life: 500},
	domain.SegLoyal:      {share: 0.07, rate: 0.075, sens: 0.20, ret: 0.8, coupon: 0.10, conv: 1.15, basket: 2.0, budget: 180, oneTimer: 0.10, life: 1400},
	domain.SegOccasional: {share: 0.26, rate: 0.012, sens: 0.55, ret: 1.0, coupon: 0.25, conv: 0.85, basket: 1.45, budget: 110, oneTimer: 0.55, life: 400},
	domain.SegFamily:     {share: 0.10, rate: 0.050, sens: 0.60, ret: 0.7, coupon: 0.35, conv: 1.05, basket: 2.3, budget: 120, oneTimer: 0.30, life: 900},
	domain.SegTech:       {share: 0.05, rate: 0.040, sens: 0.30, ret: 0.9, coupon: 0.20, conv: 0.95, basket: 1.4, budget: 250, oneTimer: 0.35, life: 700},
	domain.SegFashion:    {share: 0.05, rate: 0.060, sens: 0.45, ret: 2.3, coupon: 0.40, conv: 0.95, basket: 2.2, budget: 120, oneTimer: 0.30, life: 600},
}

var segPicker = func() *Picker[domain.Segment] {
	items := make([]domain.Segment, domain.NumSegments)
	ws := make([]float64, domain.NumSegments)
	for i := range items {
		items[i] = domain.Segment(i)
		ws[i] = segDefs[i].share
	}
	return NewPicker(items, ws)
}()

var ageBands = NewPicker([]int{18, 25, 35, 45, 55, 65}, []float64{12, 24, 22, 18, 14, 10})
var channelPicker = NewPicker([]int{0, 1, 2, 3, 4, 5, 6, 7}, []float64{30, 22, 14, 10, 3, 6, 12, 3})

var translit = map[rune]string{
	'ä': "ae", 'ö': "oe", 'ü': "ue", 'ß': "ss", 'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'à': "a", 'â': "a",
	'á': "a", 'ã': "a", 'ç': "c", 'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ó': "o", 'ò': "o", 'ô': "o",
	'õ': "o", 'ú': "u", 'ù': "u", 'û': "u", 'ñ': "n", 'ł': "l", 'ś': "s", 'ź': "z", 'ż': "z", 'ć': "c",
	'ń': "n", 'ą': "a", 'ę': "e", 'č': "c", 'ř': "r", 'š': "s", 'ž': "z", 'ý': "y", 'ě': "e", 'ů': "u",
	'ď': "d", 'ť': "t", 'ň': "n", 'å': "a", 'ø': "o", 'æ': "ae", 'ș': "s", 'ş': "s", 'ț': "t", 'ţ': "t",
	'ă': "a", 'ı': "i", 'ğ': "g", 'Ö': "oe", 'Ü': "ue", 'Ä': "ae", 'É': "e", 'Ł': "l", 'Š': "s", 'Č': "c",
	'Ž': "z", 'Ș': "s", 'Ț': "t", 'Á': "a", 'Ó': "o", 'Í': "i", 'Ç': "c", 'Å': "a", 'Ø': "o",
}

func asciiLower(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if t, ok := translit[r]; ok {
			sb.WriteString(t)
			continue
		}
		r = unicode.ToLower(r)
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func (w *World) dirty(p float64) bool { return w.cfg.Dirt > 0 && w.rng.Bool(p*w.cfg.Dirt) }

// newProspect creates a would-be customer visiting from market m.
func (w *World) newProspect(m *market, at time.Time) *prospect {
	r := w.rng
	pr := &prospect{}
	c := &pr.C
	c.Country = uint8(m.Idx)
	c.Segment = segPicker.Pick(r)
	sd := segDefs[c.Segment]
	g := r.Float64()
	switch {
	case g < 0.52:
		c.Gender = 1
	case g < 0.98:
		c.Gender = 2
	}
	band := ageBands.Pick(r)
	span := 7
	if band == 65 {
		span = 16
	}
	c.Age = uint8(band + r.IntN(span))
	c.Rate = float32(r.LogNorm(sd.rate, 0.5))
	c.PriceSens = float32(r.Clamp(r.Norm(sd.sens, 0.15), 0.02, 0.98))
	c.ReturnProp = float32(r.LogNorm(sd.ret, 0.35))
	c.CouponAff = float32(r.Clamp(r.Norm(sd.coupon, 0.15), 0, 1))
	c.PayPref = 255
	if r.Bool(0.6) {
		pm := m.payments.Pick(r)
		for i, x := range m.payList {
			if x == pm {
				c.PayPref = uint8(i)
			}
		}
	}
	dev := r.Float64()
	older := c.Age >= 55
	switch {
	case dev < 0.35 || (older && dev < 0.6):
		c.Device = 0
	case dev < 0.65:
		c.Device = 1
	case dev < 0.83:
		c.Device = 2
	default:
		c.Device = 3
	}
	c.Channel = uint8(channelPicker.Pick(r))
	c.Active = true
	if r.Bool(0.55) {
		c.Flags |= domain.FlagOptIn
	}
	w.fillAffinity(c, r)

	lang := m.langs.Pick(r)
	pool := w.ref.People.Langs[lang]
	names := pool
	intl := false
	if r.Bool(m.spec.Intl) {
		names = w.ref.People.IntlGroups[intlGroups(m.Code).Pick(r)]
		intl = true
	}
	first := Pick(r, names.Male)
	switch c.Gender {
	case 1:
		first = Pick(r, names.Female)
	case 0:
		if r.Bool(0.5) {
			first = Pick(r, names.Female)
		}
	}
	last := Pick(r, names.Last)
	if c.Gender == 1 && !intl {
		last = feminine(lang, last)
	}
	p := &pr.Profile
	p.FirstName, p.LastName = first, last
	p.Language = lang
	p.Country = m.Code
	p.Email = w.email(first, last, m, int(c.Age), r)
	if r.Bool(0.75) {
		p.Phone = w.phone(m, r)
	}
	if r.Bool(0.6) {
		byear := at.Year() - int(c.Age)
		p.BirthDate = time.Date(byear, time.Month(r.IntRange(1, 12)), r.IntRange(1, 28), 0, 0, 0, 0, time.UTC)
	}
	w.dirtyName(p)
	pr.Addr = w.newAddress(m, lang, first+" "+last, p.Phone, r)
	return pr
}

var intlByCountry = map[string]map[string]float64{
	"DE": {"tr": 45, "ar": 15, "uk": 12, "ro": 10, "in": 6, "asia": 6, "af": 6},
	"AT": {"tr": 40, "ar": 10, "uk": 15, "ro": 20, "in": 5, "asia": 5, "af": 5},
	"FR": {"ar": 50, "af": 25, "asia": 10, "tr": 5, "br": 5, "in": 5},
	"BE": {"ar": 45, "af": 25, "tr": 15, "asia": 5, "in": 5, "ro": 5},
	"NL": {"tr": 35, "ar": 35, "in": 10, "asia": 8, "af": 7, "br": 5},
	"IT": {"ro": 35, "ar": 30, "af": 15, "asia": 10, "in": 5, "uk": 5},
	"ES": {"ar": 35, "br": 25, "ro": 20, "af": 10, "asia": 10},
	"PT": {"br": 60, "af": 25, "in": 10, "uk": 5},
	"IE": {"in": 35, "br": 20, "asia": 15, "af": 15, "ro": 15},
	"PL": {"uk": 80, "asia": 10, "in": 10},
	"CZ": {"uk": 70, "asia": 20, "ro": 10},
	"SE": {"ar": 40, "tr": 15, "af": 15, "asia": 10, "in": 10, "uk": 10},
	"DK": {"ar": 35, "tr": 25, "asia": 15, "in": 10, "af": 15},
	"FI": {"uk": 30, "asia": 25, "ar": 25, "af": 20},
	"MD": {"uk": 70, "ro": 30},
}

func intlGroups(country string) *Picker[string] {
	m, ok := intlByCountry[country]
	if !ok {
		m = map[string]float64{"tr": 1, "ar": 1, "uk": 1, "in": 1, "asia": 1, "af": 1}
	}
	return PickerFromMap(m)
}

// feminine returns the female form of a surname in languages that inflect them.
func feminine(lang, last string) string {
	switch lang {
	case "pl":
		for _, sfx := range []string{"ski", "cki", "dzki"} {
			if strings.HasSuffix(last, sfx) {
				return strings.TrimSuffix(last, "i") + "a"
			}
		}
	case "cs":
		switch {
		case strings.HasSuffix(last, "ý"):
			return strings.TrimSuffix(last, "ý") + "á"
		case strings.HasSuffix(last, "ek"):
			return strings.TrimSuffix(last, "ek") + "ková"
		case strings.HasSuffix(last, "ec"):
			return strings.TrimSuffix(last, "ec") + "cová"
		case strings.HasSuffix(last, "a"):
			return strings.TrimSuffix(last, "a") + "ová"
		default:
			return last + "ová"
		}
	case "ru":
		if strings.HasSuffix(last, "ov") || strings.HasSuffix(last, "ev") || strings.HasSuffix(last, "in") {
			return last + "a"
		}
	}
	return last
}

func (w *World) fillAffinity(c *domain.Customer, r *Rand) {
	hasPet := r.Bool(0.35)
	hasKids := c.Age >= 26 && c.Age <= 48 && r.Bool(0.55)
	gardener := c.Age >= 35 && r.Bool(0.4)
	var raw [domain.MaxDepts]float64
	max := 0.0
	for _, d := range w.depts {
		f := 1.0
		n := d.name
		switch {
		case strings.HasPrefix(n, "Electronics"):
			f = map[bool]float64{true: 1.3, false: 0.9}[c.Gender == 2]
			if c.Segment == domain.SegTech {
				f *= 4
			}
		case strings.HasPrefix(n, "Women"):
			f = map[uint8]float64{0: 1, 1: 3, 2: 0.15}[c.Gender]
			if c.Segment == domain.SegFashion {
				f *= 3.5
			}
		case strings.HasPrefix(n, "Men"):
			f = map[uint8]float64{0: 1, 1: 0.35, 2: 3}[c.Gender]
			if c.Segment == domain.SegFashion {
				f *= 3
			}
		case strings.HasPrefix(n, "Kids"), strings.HasPrefix(n, "Toys"):
			f = 0.3
			if hasKids {
				f = 3
			} else if c.Age >= 55 {
				f = 1.2
			}
			if c.Segment == domain.SegFamily {
				f *= 3
			}
		case strings.HasPrefix(n, "Beauty"):
			f = map[uint8]float64{0: 1, 1: 2, 2: 0.6}[c.Gender]
			if c.Segment == domain.SegFashion {
				f *= 2
			}
		case strings.HasPrefix(n, "DIY"):
			f = map[uint8]float64{0: 1, 1: 0.6, 2: 1.8}[c.Gender]
		case strings.HasPrefix(n, "Garden"):
			f = 0.4
			if gardener {
				f = 2.5
			}
		case strings.HasPrefix(n, "Automotive"):
			f = map[uint8]float64{0: 1, 1: 0.5, 2: 2}[c.Gender]
		case strings.HasPrefix(n, "Jewellery"):
			f = map[uint8]float64{0: 1, 1: 1.5, 2: 0.8}[c.Gender]
			if c.Segment == domain.SegFashion {
				f *= 2
			}
		case strings.HasPrefix(n, "Pet"):
			f = 0.08
			if hasPet {
				f = 4
			}
		case strings.HasPrefix(n, "Health"), strings.HasPrefix(n, "Grocery"):
			if c.Segment == domain.SegFamily {
				f = 2
			}
			if c.Segment == domain.SegBargain {
				f *= 1.3
			}
		case strings.HasPrefix(n, "Books"):
			if c.Age >= 45 {
				f = 1.3
			}
			if c.Segment == domain.SegOccasional {
				f *= 1.2
			}
		}
		v := f * r.LogNorm(1, 0.55)
		raw[d.idx] = v
		if v > max {
			max = v
		}
	}
	for i := range raw {
		c.Affinity[i] = uint8(math.Max(1, math.Round(raw[i]/max*255)))
	}
}

func (w *World) email(first, last string, m *market, age int, r *Rand) string {
	f, l := asciiLower(first), asciiLower(last)
	if f == "" {
		f = "user"
	}
	if l == "" {
		l = "mail"
	}
	year := w.now.Year() - age
	var local string
	switch r.IntN(10) {
	case 0, 1, 2:
		local = f + "." + l
	case 3:
		local = f + l
	case 4:
		local = f[:1] + "." + l
	case 5:
		local = f + "_" + l
	case 6:
		local = fmt.Sprintf("%s.%s%d", f, l, year%100)
	case 7:
		local = fmt.Sprintf("%s%d", f, r.IntRange(1, 999))
	case 8:
		local = fmt.Sprintf("%s.%s%d", f, l, year)
	default:
		local = fmt.Sprintf("%s%s%d", f[:1], l, r.IntRange(1, 99))
	}
	if r.Bool(0.35) {
		local += fmt.Sprint(r.IntRange(1, 99))
	}
	e := local + "@" + m.emails.Pick(r)
	switch {
	case w.dirty(0.05):
		e = strings.ToUpper(e[:1]) + e[1:]
	case w.dirty(0.02):
		e = " " + e
	case w.dirty(0.02):
		e += " "
	case w.dirty(0.01):
		e = strings.ToUpper(e)
	}
	return e
}

func (w *World) phone(m *market, r *Rand) string {
	sp := m.spec
	pre := Pick(r, sp.PhoneMobile)
	var num strings.Builder
	for i := 0; i < sp.PhoneDigits; i++ {
		num.WriteByte(byte('0' + r.IntN(10)))
	}
	n := num.String()
	cc := strings.TrimPrefix(sp.PhoneCode, "+")
	if !w.dirty(0.5) {
		return sp.PhoneCode + pre + n
	}
	switch r.IntN(5) {
	case 0:
		return "0" + pre + " " + n
	case 1:
		return "00" + cc + " " + pre + " " + n
	case 2:
		return sp.PhoneCode + " " + pre + " " + n
	case 3:
		return "(0" + pre + ") " + n[:len(n)/2] + "-" + n[len(n)/2:]
	default:
		return sp.PhoneCode + " (0)" + pre + n
	}
}

func (w *World) dirtyName(p *domain.CustomerProfile) {
	switch {
	case w.dirty(0.02):
		p.FirstName, p.LastName = strings.ToUpper(p.FirstName), strings.ToUpper(p.LastName)
	case w.dirty(0.02):
		p.FirstName, p.LastName = strings.ToLower(p.FirstName), strings.ToLower(p.LastName)
	case w.dirty(0.01):
		p.FirstName = p.FirstName + " "
	}
}

func fillPattern(r *Rand, pat string) string {
	var sb strings.Builder
	for _, ch := range pat {
		switch ch {
		case '#':
			sb.WriteByte(byte('0' + r.IntN(10)))
		case '@':
			sb.WriteByte(byte('A' + r.IntN(26)))
		default:
			sb.WriteRune(ch)
		}
	}
	return sb.String()
}

func (w *World) newAddress(m *market, lang, recipient, phone string, r *Rand) domain.Address {
	city := m.cities.Pick(r)
	pool := w.ref.People.Langs[lang]
	if len(pool.Streets) == 0 {
		pool = w.ref.People.Langs[m.langs.Items()[0]]
	}
	num := 1 + int(r.Exp(25))
	if num > 250 {
		num = r.IntRange(1, 40)
	}
	repl := strings.NewReplacer(
		"{street}", Pick(r, pool.Streets),
		"{num}", fmt.Sprint(num),
		"{apt}", fmt.Sprint(r.IntRange(1, 48)),
		"{letter}", string(rune('A'+r.IntN(4))),
		"{floor}", fmt.Sprint(r.IntRange(1, 8)),
		"{name}", Pick(r, pool.Last),
	)
	a := domain.Address{
		Recipient:  recipient,
		Line1:      repl.Replace(Pick(r, pool.StreetFmt)),
		PostalCode: fillPattern(r, Pick(r, city.Zip)),
		City:       city.Name,
		Country:    m.Code,
		Phone:      phone,
	}
	if len(pool.Line2Fmt) > 0 && r.Bool(pool.Line2Prob) {
		a.Line2 = repl.Replace(Pick(r, pool.Line2Fmt))
	}
	// dirty data
	if len(city.Variants) > 0 && w.dirty(0.12) {
		a.City = Pick(r, city.Variants)
	}
	switch m.Code {
	case "DE", "FR", "IT", "ES", "FI":
		if strings.HasPrefix(a.PostalCode, "0") && w.dirty(0.08) {
			a.PostalCode = strings.TrimPrefix(a.PostalCode, "0") // Excel ate the leading zero
		}
	case "NL":
		if w.dirty(0.3) {
			a.PostalCode = strings.ReplaceAll(a.PostalCode, " ", "")
		}
		if w.dirty(0.1) {
			a.PostalCode = strings.ToLower(a.PostalCode)
		}
	case "IE":
		if r.Bool(0.1) || w.dirty(0.3) {
			a.PostalCode = "" // Eircode is optional in practice
		}
	case "MD":
		if w.dirty(0.3) {
			a.PostalCode = strings.TrimPrefix(a.PostalCode, "MD-")
		}
	}
	if w.dirty(0.01) {
		a.Country = strings.ToLower(a.Country)
	}
	if w.dirty(0.03) {
		a.Line1 = strings.ToUpper(a.Line1)
	}
	return a
}

// register turns a prospect into a customer.
func (w *World) register(pr *prospect, at time.Time) *domain.Customer {
	r := w.rng
	c := pr.C
	cust := &c
	w.ids.Customer++
	cust.ID = w.ids.Customer
	cust.CreatedAt = at.Unix()
	cust.UpdatedAt = at.Unix()
	sd := segDefs[cust.Segment]
	if r.Bool(sd.oneTimer) {
		cust.ChurnAt = at.Add(time.Duration(r.Exp(45)*24) * time.Hour).Unix()
	} else {
		cust.ChurnAt = at.Add(time.Duration(r.Exp(sd.life)*24) * time.Hour).Unix()
	}
	w.ids.Address++
	a := pr.Addr
	a.ID = w.ids.Address
	a.CustomerID = cust.ID
	a.CreatedAt = at
	cust.AddressID = a.ID
	w.addCustomer(cust)
	prof := pr.Profile
	prof.C = cust
	w.uow.addCustomer(&prof)
	w.uow.addAddress(&a)

	// some people later open a second account (dedupe exercise)
	if w.dirty(0.03) {
		dp := prof
		f, l := asciiLower(prof.FirstName), asciiLower(prof.LastName)
		dp.Email = fmt.Sprintf("%s.%s%d@%s", f, l, r.IntRange(1, 99), w.marketOf(cust).emails.Pick(r))
		if r.Bool(0.4) {
			dp.FirstName = strings.ToUpper(dp.FirstName[:1]) + strings.ToLower(dp.FirstName[1:])
		}
		w.dups = append(w.dups, dupPlan{at: at.Add(time.Duration(r.Range(10, 400)*24) * time.Hour), profile: dp, addr: pr.Addr, market: prof.Country, src: *cust})
	}
	return cust
}

func (w *World) marketOf(c *domain.Customer) *market { return w.markets[c.Country] }

// addCustomer adds a customer to the in-memory indexes.
func (w *World) addCustomer(c *domain.Customer) {
	for int64(len(w.customers)) < c.ID-1 {
		w.customers = append(w.customers, nil)
	}
	if int64(len(w.customers)) == c.ID-1 {
		w.customers = append(w.customers, c)
	} else {
		w.customers[c.ID-1] = c
	}
	m := w.markets[c.Country]
	c.FenIdx = int32(m.fen.Add(w.custWeight(c)))
	m.fenIDs = append(m.fenIDs, c.ID)
}

// custWeight is the session rate used for sampling returning customers.
func (w *World) custWeight(c *domain.Customer) float64 {
	if c.Status != domain.CustActive || c.Has(domain.FlagTest) {
		return 0
	}
	if !c.Active {
		return float64(c.Rate) * 0.04
	}
	return float64(c.Rate)
}

func (w *World) updateWeight(c *domain.Customer) {
	m := w.markets[c.Country]
	m.fen.Set(int(c.FenIdx), w.custWeight(c))
}
