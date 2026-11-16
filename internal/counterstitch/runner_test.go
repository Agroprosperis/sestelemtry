package counterstitch

import (
	"context"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/counters"
	"github.com/nesh/sestelemetry/internal/storage"
)

func day(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

// Дубовʼязівський's export counter fell ten times from April to July as
// devices moved to Т1's logger and has metered on since; Зернова's import
// still falls (10.11). The first are resets, closed at their start; the
// second stay open.
func TestResetsOf(t *testing.T) {
	until := day("2026-11-16")
	open := []storage.CounterEpisode{
		{MetricKey: "sold", StartedAt: day("2026-04-15"), ValueAfter: 0},
		{MetricKey: "sold", StartedAt: day("2026-07-18"), ValueAfter: 2358},
		{MetricKey: "purchased", StartedAt: day("2026-10-21"), ValueAfter: 111831},
		{MetricKey: "purchased", StartedAt: day("2026-11-10"), ValueAfter: 238194},
		{MetricKey: "dead", StartedAt: day("2026-09-01"), ValueAfter: 0},
	}
	top := map[string]float64{"sold": 34840, "purchased": 280000, "dead": 0.4}
	got, err := resetsOf(open, until, func(metric string, _ time.Time) (float64, bool, error) {
		return top[metric], true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("resets = %+v, want the two export falls", got)
	}
	for _, e := range got {
		if e.MetricKey != "sold" || e.Kind != string(counters.KindReset) || e.EndedAt == nil || !e.EndedAt.Equal(e.StartedAt) {
			t.Fatalf("reset = %+v", e)
		}
	}
}

// Кролевець's import stood still on 07.11 and was stored open while 08.11
// was today; it metered again on 08.11. The stretch closes after 07.11.
func TestStaleFrozen(t *testing.T) {
	stored := []storage.CounterEpisode{
		{StartedAt: day("2026-11-07"), Kind: "frozen", ValueBefore: 313056.5, ValueAfter: 313056.5},
		{StartedAt: day("2026-11-12"), Kind: "frozen"},
	}
	days := []counters.DaySpan{
		{Day: day("2026-11-06"), First: 312915.4, Last: 313056.5, Samples: 86028},
		{Day: day("2026-11-07"), First: 313056.5, Last: 313056.5, Samples: 85398},
		{Day: day("2026-11-08"), First: 313056.5, Last: 313084.1, Samples: 86334},
	}
	found := []counters.Episode{{Kind: counters.KindFrozen, Start: day("2026-11-12")}}
	got := staleFrozen(stored, found, days)
	if len(got) != 1 || !got[0].Start.Equal(day("2026-11-07")) || !got[0].End.Equal(day("2026-11-08")) {
		t.Fatalf("closed = %+v", got)
	}
}

// Кролевець: the grid lost at 18:27:40 by the generator controller, back
// at 04:00. The SmartLogger's grid counters fell to zero at 18:27:39 and
// came back at 04:04; its PV counter blipped for 35 s at the switchover.
func TestMarkOffgrid(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	lost, back := at("2026-11-15T16:27:40Z"), at("2026-11-16T02:00:00Z")
	r := &Runner{OffgridBetween: func(_ context.Context, _ string, from, to time.Time) (bool, error) {
		// Off-grid readings fall anywhere in [lost, back).
		return from.Before(back) && !to.Before(lost), nil
	}}
	eps := []counters.Episode{
		{Kind: counters.KindDropout, Start: at("2026-11-15T16:27:39Z"), End: at("2026-11-16T02:04:10Z")},
		{Kind: counters.KindDropout, Start: at("2026-11-15T16:27:39Z"), End: at("2026-11-15T16:28:14Z")},
		{Kind: counters.KindDropout, Start: at("2026-11-14T09:00:00Z"), End: at("2026-11-16T02:05:00Z")},
		{Kind: counters.KindDropout, Start: at("2026-11-14T09:00:00Z"), End: at("2026-11-14T12:00:00Z")},
		{Kind: counters.KindSeam, Start: at("2026-11-15T16:28:00Z"), End: at("2026-11-15T16:28:00Z")},
	}
	if err := r.markOffgrid(context.Background(), "ke", eps); err != nil {
		t.Fatal(err)
	}
	want := []counters.Kind{counters.KindOffgrid, counters.KindOffgrid, counters.KindOffgrid, counters.KindDropout, counters.KindSeam}
	for i, w := range want {
		if eps[i].Kind != w {
			t.Errorf("episode %d: %q, want %q", i, eps[i].Kind, w)
		}
	}
}
