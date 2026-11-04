package fxrates

import (
	"sort"
	"time"
)

// Table is one currency's daily rates, UAH per unit, keyed by civil date
// (YYYY-MM-DD).
type Table struct {
	days  []string
	rates map[string]float64
}

// NewTable indexes rates by day.
func NewTable(rates map[string]float64) Table {
	days := make([]string, 0, len(rates))
	clean := make(map[string]float64, len(rates))
	for d, r := range rates {
		if r > 0 {
			days = append(days, d)
			clean[d] = r
		}
	}
	sort.Strings(days)
	return Table{days: days, rates: clean}
}

// Len is the number of days with their own rate.
func (t Table) Len() int { return len(t.days) }

// Rate is the rate for day: its own when stored, else the latest earlier
// one, else the earliest later one. carried reports a borrowed rate; ok
// is false only for an empty table.
func (t Table) Rate(day string) (rate float64, carried, ok bool) {
	if r, hit := t.rates[day]; hit {
		return r, false, true
	}
	if len(t.days) == 0 {
		return 0, false, false
	}
	i := sort.SearchStrings(t.days, day)
	if i > 0 {
		return t.rates[t.days[i-1]], true, true
	}
	return t.rates[t.days[0]], true, true
}

// Window summarizes the rates of the civil days [from, to] in loc: their
// mean and how many of them borrowed a neighbour's rate. Days after today
// are left out, they have no rate of their own yet.
func (t Table) Window(from, to time.Time, loc *time.Location, now time.Time) (avg float64, carried int) {
	today := now.In(loc).Format("2006-01-02")
	var sum float64
	var n int
	for d := civilDay(from, loc); !d.After(civilDay(to, loc)); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if key > today {
			break
		}
		r, borrowed, ok := t.Rate(key)
		if !ok {
			continue
		}
		sum += r
		n++
		if borrowed {
			carried++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), carried
}

func civilDay(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}
