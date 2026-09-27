// Package domain holds the pure business model of the simulated marketplace:
// entities, value objects and reference specifications. It has no
// dependencies on infrastructure.
package domain

import (
	"fmt"
	"math"
)

// Money is an amount in minor units (cents) of some currency.
type Money int64

func MoneyFromFloat(f float64) Money { return Money(math.Round(f * 100)) }

func (m Money) Float() float64 { return float64(m) / 100 }

// Mul multiplies by a factor, rounding to the nearest minor unit.
func (m Money) Mul(f float64) Money { return Money(math.Round(float64(m) * f)) }

func (m Money) String() string {
	sign := ""
	v := int64(m)
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// RoundingStyle describes how consumer prices look in a given currency.
type RoundingStyle string

const (
	RoundCents99   RoundingStyle = "cents99"  // 19.99, 249.00 for big amounts
	RoundInteger9  RoundingStyle = "integer9" // 299, 1 299
	RoundCentsFlat RoundingStyle = "cents"    // plain cents
)

// Currency is a reference entity.
type Currency struct {
	Code     string
	Name     string
	Rounding RoundingStyle
	// Rate is the initial amount of this currency per 1 EUR.
	Rate float64
	// Drift is the yearly log drift of the rate (positive = currency weakens).
	Drift float64
	// Vol is the daily log volatility of the rate.
	Vol float64
}

// PsychPrice turns a raw amount into a "shelf" price typical for the currency.
func PsychPrice(raw float64, style RoundingStyle) Money {
	if raw <= 0 {
		return 0
	}
	switch style {
	case RoundInteger9:
		if raw < 10 {
			return MoneyFromFloat(math.Max(1, math.Round(raw)))
		}
		step := 10.0
		if raw >= 1000 {
			step = 100
		}
		v := math.Floor(raw/step)*step + (step - 1)
		if v-raw > step*0.6 {
			v -= step
		}
		if v <= 0 {
			v = step - 1
		}
		return MoneyFromFloat(v)
	case RoundCents99:
		switch {
		case raw < 3:
			return MoneyFromFloat(math.Floor(raw*10)/10 + 0.09)
		case raw < 200:
			return MoneyFromFloat(math.Floor(raw) + 0.99)
		case raw < 1000:
			v := math.Floor(raw/10)*10 + 9.99
			if v-raw > 6 {
				v -= 10
			}
			return MoneyFromFloat(v)
		default:
			v := math.Floor(raw/100)*100 + 99
			if v-raw > 60 {
				v -= 100
			}
			return MoneyFromFloat(v)
		}
	default:
		return MoneyFromFloat(raw)
	}
}
