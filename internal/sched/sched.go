// Package sched は cron と同じ5つの欄（分 時 日 月 曜日）の予定を読む。
// 各欄は * ・数・範囲（1-5）・並び（1,3）・間隔（*/10、9-17/2）を書ける。曜日は 0 と 7 が日曜。
package sched

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Spec struct {
	min, hour, dom, mon, dow [61]bool
	domAll, dowAll           bool
	Expr                     string
}

var bounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

// Parse は cron の式を読む。
func Parse(expr string) (*Spec, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, fmt.Errorf("欄は5つ（分 時 日 月 曜日）: %q", expr)
	}
	s := &Spec{Expr: strings.Join(f, " ")}
	sets := []*[61]bool{&s.min, &s.hour, &s.dom, &s.mon, &s.dow}
	for i, field := range f {
		if err := parseField(field, bounds[i][0], bounds[i][1], sets[i]); err != nil {
			return nil, fmt.Errorf("%d 番目の欄 %q: %w", i+1, field, err)
		}
	}
	if s.dow[7] {
		s.dow[0] = true
	}
	s.domAll, s.dowAll = f[2] == "*", f[4] == "*"
	return s, nil
}

func parseField(field string, lo, hi int, set *[61]bool) error {
	for _, part := range strings.Split(field, ",") {
		step := 1
		if a, b, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(b)
			if err != nil || n <= 0 {
				return fmt.Errorf("間隔が読めない")
			}
			part, step = a, n
		}
		from, to := lo, hi
		if part != "*" {
			a, b, isRange := strings.Cut(part, "-")
			x, err := strconv.Atoi(a)
			if err != nil {
				return fmt.Errorf("数が読めない")
			}
			from, to = x, x
			if isRange {
				if to, err = strconv.Atoi(b); err != nil {
					return fmt.Errorf("範囲が読めない")
				}
			} else if step > 1 {
				to = hi
			}
		}
		if from < lo || to > hi || from > to {
			return fmt.Errorf("%d〜%d の外", lo, hi)
		}
		for v := from; v <= to; v += step {
			set[v] = true
		}
	}
	return nil
}

func (s *Spec) day(t time.Time) bool {
	d, w := s.dom[t.Day()], s.dow[int(t.Weekday())]
	// cron と同じく、日と曜日の両方を指定したときはどちらかに合えばよい
	switch {
	case s.domAll && s.dowAll:
		return true
	case s.domAll:
		return w
	case s.dowAll:
		return d
	}
	return d || w
}

// Match は t（分まで）が予定に合うか。
func (s *Spec) Match(t time.Time) bool {
	return s.min[t.Minute()] && s.hour[t.Hour()] && s.mon[int(t.Month())] && s.day(t)
}

// Next は t より後で最初に予定に合う時刻（最大 5 年先まで探す）。
func (s *Spec) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	end := t.AddDate(5, 0, 0)
	for t.Before(end) {
		if !s.mon[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.day(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !s.hour[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if !s.min[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// Due は、前に送った時刻 last のあと、now までに予定の時刻が来たか。
// マシンが止まっていて時刻を逃したときも、次に起きたときに1回送る。
func (s *Spec) Due(last, now time.Time) bool {
	if last.IsZero() {
		return true
	}
	n := s.Next(last)
	return !n.IsZero() && !n.After(now)
}
