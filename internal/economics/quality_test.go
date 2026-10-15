package economics

import (
	"reflect"
	"testing"
)

func TestCounterFlags(t *testing.T) {
	got := counterFlags([]CounterEpisode{
		{MetricKey: "accumulated_electricity_sold_kwh", Kind: "dropout"},
		{MetricKey: "accumulated_electricity_purchased_kwh", Kind: "dropout"},
		{MetricKey: "accumulated_electricity_purchased_kwh", Kind: "dropout"},
		{MetricKey: "accumulated_pv_energy_yield_kwh", Kind: "frozen"},
		{MetricKey: "soc_percent", Kind: "dropout"},
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
