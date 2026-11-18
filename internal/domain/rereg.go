package domain

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

// MSK is Moscow time. Russia has no daylight saving time since 2014, so a fixed
// zone avoids depending on the host tzdata.
var MSK = time.FixedZone("MSK", 3*60*60)

const (
	// DefaultReregMaxBid is the per-bid backorder cap in roubles.
	DefaultReregMaxBid = "3000"
	// DefaultReregMaxDaily is the backorder spend cap per Moscow calendar day in roubles.
	DefaultReregMaxDaily = "10000"
)

// Release-day schedule of the REG.RU backorder (освобождающиеся домены).
// The last payment is accepted at 16:45 MSK on the release day. The guard
// window around 17:00 MSK is reserved for the production auto-buyer that
// shares the REG.API account (BILLING_LOCK, request budget).
var (
	reregDeadlineClock    = clock{16, 45}
	reregGuardWindowStart = clock{16, 55}
	reregGuardWindowEnd   = clock{17, 15}
)

type clock struct{ hour, minute int }

func (c clock) on(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), c.hour, c.minute, 0, 0, MSK)
}

// ReregZone reports whether REG.RU runs backorders for the zone.
func ReregZone(zone Zone) bool {
	return zone == ZoneRU || zone == ZoneRF || zone == ZoneSU
}

// ReregGuardWindow returns the [start, end) guard window of the Moscow day
// containing now, and whether now falls inside it.
func ReregGuardWindow(now time.Time) (time.Time, time.Time, bool) {
	local := now.In(MSK)
	start, end := reregGuardWindowStart.on(local), reregGuardWindowEnd.on(local)
	return start, end, !local.Before(start) && local.Before(end)
}

// ReregBidDeadline is the last-payment moment (16:45 MSK) of a release date
// written as YYYY-MM-DD.
func ReregBidDeadline(releaseDate string) (time.Time, bool) {
	day, err := time.ParseInLocation("2006-01-02", releaseDate, MSK)
	if err != nil || day.Format("2006-01-02") != releaseDate {
		return time.Time{}, false
	}
	return reregDeadlineClock.on(day), true
}

// MSKDate returns the Moscow calendar date of t as YYYY-MM-DD.
func MSKDate(t time.Time) string { return t.In(MSK).Format("2006-01-02") }

// Kopecks converts a non-negative decimal rouble amount such as "450",
// "350900.00", or "499.9" to integer kopecks. More than two significant
// decimals, signs, exponents, and empty input are rejected.
func Kopecks(amount string) (int64, bool) {
	value := strings.TrimSpace(amount)
	if value == "" || strings.ContainsAny(value, "+-eE") {
		return 0, false
	}
	parsed, ok := new(big.Rat).SetString(value)
	if !ok || parsed.Sign() < 0 {
		return 0, false
	}
	scaled := new(big.Rat).Mul(parsed, big.NewRat(100, 1))
	if !scaled.IsInt() || !scaled.Num().IsInt64() {
		return 0, false
	}
	return scaled.Num().Int64(), true
}

// Roubles renders kopecks as a decimal rouble amount without trailing zero
// kopecks: 45000 -> "450", 49990 -> "499.90".
func Roubles(kopecks int64) string {
	sign := ""
	if kopecks < 0 {
		sign, kopecks = "-", -kopecks
	}
	if kopecks%100 == 0 {
		return fmt.Sprintf("%s%d", sign, kopecks/100)
	}
	return fmt.Sprintf("%s%d.%02d", sign, kopecks/100, kopecks%100)
}
