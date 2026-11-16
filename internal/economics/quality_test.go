package economics

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestCounterFlags(t *testing.T) {
	got := counterFlags([]CounterEpisode{
		{MetricKey: "accumulated_electricity_sold_kwh", Kind: "dropout"},
		{MetricKey: "accumulated_electricity_purchased_kwh", Kind: "dropout"},
		{MetricKey: "accumulated_electricity_purchased_kwh", Kind: "dropout"},
		{MetricKey: "accumulated_pv_energy_yield_kwh", Kind: "frozen"},
		{MetricKey: "soc_percent", Kind: "dropout"},
		// The grid meter's fall during an outage is the outage, and a
		// counter metering on from a new level is no fault either.
		{MetricKey: "total_energy_charged_kwh", Kind: "offgrid"},
		{MetricKey: "total_energy_discharged_kwh", Kind: "reset"},
	})
	want := []string{"counter_dropout:grid_export", "counter_dropout:grid_import", "counter_frozen:pv"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flags = %v, want %v", got, want)
	}
	if !dayHasCounterIssue(got) {
		t.Fatal("a dropout day is a counter issue")
	}
	if dayHasCounterIssue([]string{"counter_seam:pv", "counter_spike:ess_charged"}) {
		t.Fatal("a removed seam or spike leaves the day's numbers sound")
	}
}

// 6 August at Дубовязівський: 10.7 MWh exported against 2.5 MWh of PV.
func TestBalanceFlag(t *testing.T) {
	flag, ok := balanceFlag(DailyTotals{PV: 2493, GridImport: 105, EssDischarged: 755, GridExport: 10737, EssCharged: 767})
	if !ok || flag != "balance_negative:8151" {
		t.Fatalf("flag = %q, %v", flag, ok)
	}
	if _, ok := balanceFlag(DailyTotals{PV: 1000, GridImport: 500, EssDischarged: 100, GridExport: 400, EssCharged: 120}); ok {
		t.Fatal("a balanced day is not flagged")
	}
}

// flagsBackend serves counter episodes per organization and day and
// saves flags in place.
type flagsBackend struct {
	*orgDaysBackend
	episodes map[string][]CounterEpisode
}

func (b *flagsBackend) CounterEpisodes(_ context.Context, org string, from, _ time.Time) ([]CounterEpisode, error) {
	return b.episodes[dayKey(org, from)], nil
}

func (b *flagsBackend) SaveDayFlags(_ context.Context, org string, day time.Time, flags []string) error {
	d := b.days[dayKey(org, day)]
	d.Totals.QualityFlags = flags
	b.days[dayKey(org, day)] = d
	return nil
}

// Т2's export counter fell in April and metered on: once its dropout is
// a reset, its days lose the flag, the other flags stay, and the
// elevator's stored day follows.
func TestRefreshCounterFlags(t *testing.T) {
	inner, _ := newOrgDaysBackend(t, flatTariffs)
	b := &flagsBackend{orgDaysBackend: inner, episodes: map[string][]CounterEpisode{}}
	dayStart := groupDayAt(t)
	ctx := context.Background()
	for _, org := range []string{"de", "de1", "de-all"} {
		d := StoredDay{OrganizationID: org, Day: dayStart, Rows: make([]*HourRow, 24)}
		d.Totals.QualityFlags = []string{"import_lag:3", "counter_dropout:grid_export", "counter_step:pv"}
		_ = b.SaveDay(ctx, d)
	}
	b.episodes[dayKey("de", dayStart)] = []CounterEpisode{{MetricKey: "accumulated_electricity_sold_kwh", Kind: "reset"}}

	n, err := NewService(b).RefreshCounterFlags(ctx, "de", dayStart, dayStart, "Europe/Kyiv")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"import_lag:3", "counter_step:pv"}
	if got := b.days[dayKey("de", dayStart)].Totals.QualityFlags; !reflect.DeepEqual(got, want) {
		t.Fatalf("de flags = %v, want %v", got, want)
	}
	// de1 still has its own dropout, so the elevator keeps it.
	if got := b.days[dayKey("de-all", dayStart)].Totals.QualityFlags; !reflect.DeepEqual(got, []string{"import_lag:3", "counter_step:pv", "counter_dropout:grid_export"}) {
		t.Fatalf("de-all flags = %v", got)
	}
	if n != 2 {
		t.Fatalf("%d days changed, want de and the elevator's reordered flags", n)
	}
}
