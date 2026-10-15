package economics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// CounterEpisode is a lifetime-counter discontinuity the stitcher
// recorded (see internal/counters), as far as day flags need it.
type CounterEpisode struct {
	MetricKey string
	Kind      string
}

// CounterEpisodeSource is the optional Backend extension that reports
// the counter episodes overlapping [from, to).
type CounterEpisodeSource interface {
	CounterEpisodes(ctx context.Context, orgID string, from, to time.Time) ([]CounterEpisode, error)
}

// counterFlagNames shortens the stitched counters to the names the
// reconciliation flags already use.
var counterFlagNames = map[string]string{
	"accumulated_pv_energy_yield_kwh":       "pv",
	"accumulated_electricity_purchased_kwh": "grid_import",
	"accumulated_electricity_sold_kwh":      "grid_export",
	essChargedCounterKey:                    "ess_charged",
	essDischargedCounterKey:                 "ess_discharged",
}

// counterFlags turns the day's counter episodes into flags such as
// counter_dropout:grid_import, one per kind and counter.
func counterFlags(episodes []CounterEpisode) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range episodes {
		name, ok := counterFlagNames[e.MetricKey]
		if !ok {
			continue
		}
		flag := "counter_" + e.Kind + ":" + name
		if !seen[flag] {
			seen[flag] = true
			out = append(out, flag)
		}
	}
	sort.Strings(out)
	return out
}

// balanceFlag reports a day whose counters cannot all be right: the
// energy that left the site (export, ESS charge) exceeds what entered
// it (PV, import, ESS discharge) by more than metering slack. The flag
// carries the shortfall in kWh.
func balanceFlag(t DailyTotals) (string, bool) {
	in := t.PV + t.GridImport + t.EssDischarged
	balance := in - t.GridExport - t.EssCharged
	if balance >= -math.Max(5, 0.02*in) {
		return "", false
	}
	return fmt.Sprintf("balance_negative:%.0f", -balance), true
}
