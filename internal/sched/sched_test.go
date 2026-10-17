package sched

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	base := time.Date(2026, 10, 17, 10, 7, 30, 0, time.UTC) // 土曜
	for expr, want := range map[string]string{
		"* * * * *":      "2026-10-17 10:08",
		"*/15 * * * *":   "2026-10-17 10:15",
		"0 * * * *":      "2026-10-17 11:00",
		"30 9 * * *":     "2026-10-18 09:30",
		"0 9 * * 1":      "2026-10-19 09:00",
		"0 9 * * 1-5":    "2026-10-19 09:00",
		"0 0 1 * *":      "2026-11-01 00:00",
		"0 21 * * 0,6":   "2026-10-17 21:00",
		"0 9-17/4 * * *": "2026-10-17 13:00",
		"0 3 * * 7":      "2026-10-18 03:00",
	} {
		s, err := Parse(expr)
		if err != nil {
			t.Fatal(expr, err)
		}
		if got := s.Next(base).Format("2006-01-02 15:04"); got != want {
			t.Errorf("%s: %s want %s", expr, got, want)
		}
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "*/0 * * * *", "5-1 * * * *", "a * * * *"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestDue(t *testing.T) {
	s, _ := Parse("0 9 * * *")
	last := time.Date(2026, 10, 16, 9, 0, 5, 0, time.UTC)
	if s.Due(last, time.Date(2026, 10, 17, 8, 59, 0, 0, time.UTC)) {
		t.Error("not yet")
	}
	if !s.Due(last, time.Date(2026, 10, 17, 9, 0, 1, 0, time.UTC)) || !s.Due(last, time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)) {
		t.Error("due")
	}
}
