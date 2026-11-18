package domain

import (
	"testing"
	"time"
)

func TestKopecksAndRoubles(t *testing.T) {
	for input, want := range map[string]int64{"450": 45000, "350900.00": 35090000, "499.9": 49990, "0": 0, " 1950 ": 195000} {
		got, ok := Kopecks(input)
		if !ok || got != want {
			t.Errorf("Kopecks(%q) = %d, %v; want %d", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "-1", "1e3", "+5", "1.005", "abc"} {
		if got, ok := Kopecks(input); ok {
			t.Errorf("Kopecks(%q) = %d, want rejection", input, got)
		}
	}
	for kopecks, want := range map[int64]string{45000: "450", 49990: "499.90", 5: "0.05"} {
		if got := Roubles(kopecks); got != want {
			t.Errorf("Roubles(%d) = %q, want %q", kopecks, got, want)
		}
	}
}

func TestReregGuardWindowBoundsInMoscowTime(t *testing.T) {
	at := func(hour, minute int) time.Time { return time.Date(2026, 11, 19, hour, minute, 0, 0, MSK) }
	for _, check := range []struct {
		when   time.Time
		inside bool
	}{
		{at(16, 54), false},
		{at(16, 55), true},
		{at(17, 0), true},
		{at(17, 14), true},
		{at(17, 15), false},
		// 14:00 UTC is 17:00 MSK: the window follows Moscow time, not the host zone.
		{time.Date(2026, 11, 19, 14, 0, 0, 0, time.UTC), true},
		{time.Date(2026, 11, 19, 17, 0, 0, 0, time.UTC), false},
	} {
		if _, _, inside := ReregGuardWindow(check.when); inside != check.inside {
			t.Errorf("ReregGuardWindow(%s) inside = %v, want %v", check.when, inside, check.inside)
		}
	}
}

func TestReregBidDeadlineIs1645MSK(t *testing.T) {
	deadline, ok := ReregBidDeadline("2026-11-19")
	if !ok || deadline.Format(time.RFC3339) != "2026-11-19T16:45:00+03:00" {
		t.Fatalf("deadline = %s, %v", deadline.Format(time.RFC3339), ok)
	}
	if _, ok := ReregBidDeadline("19.11.2026"); ok {
		t.Fatal("a non-ISO date was accepted")
	}
	if MSKDate(time.Date(2026, 11, 18, 21, 30, 0, 0, time.UTC)) != "2026-11-19" {
		t.Fatal("MSKDate does not use Moscow time")
	}
}
