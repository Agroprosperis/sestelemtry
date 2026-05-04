package api

import (
	"math"
	"testing"
)

func TestParseCSV(t *testing.T) {
	got := ParseCSV("a, b ,,,c")
	if len(got) != 3 {
		t.Fatalf("expected 3 values, got %d", len(got))
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("unexpected values: %#v", got)
	}
}

// TestBucketAtLeastOneDay locks in the bucket-routing decision so a
// future regression can't accidentally route the day-preset's 5-minute
// (or hour-bucket) queries through the daily CAGG. Day-preset reads
// must always hit the raw hypertable to preserve intra-day granularity.
func TestBucketAtLeastOneDay(t *testing.T) {
	cases := []struct {
		bucket string
		want   bool
	}{
		{"", false},
		{"5 minutes", false},
		{"15 minutes", false},
		{"30 seconds", false},
		{"1 hour", false},
		{"6 hours", false},
		{"1 day", true},
		{"2 days", true},
		{"1 week", true},
		{"1 month", true},
		{"1 year", true},
		{"  1 DAY  ", true},
	}
	for _, tc := range cases {
		if got := bucketAtLeastOneDay(tc.bucket); got != tc.want {
			t.Errorf("bucketAtLeastOneDay(%q) = %v, want %v", tc.bucket, got, tc.want)
		}
	}
}

// TestApplyApplianceConsumptionRule mirrors the frontend rule: when the
// device-reported consumption counter is missing or stuck at zero, the
// algebraic appliance-balance fallback (purchased + pv + discharge -
// charge) must populate it so the dashboard summary still has a
// consumption number. Working counters (>0) must be left alone.
func TestApplyApplianceConsumptionRule(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]float64
		want float64
		// keep tracks whether totals[consumptionKey] should remain
		// untouched (i.e. the input value passes through unchanged).
		keep bool
	}{
		{
			name: "missing consumption key is a no-op",
			in: map[string]float64{
				"accumulated_electricity_purchased_kwh": 5,
			},
			keep: true, // key not present, function returns early
		},
		{
			name: "non-zero device counter is trusted",
			in: map[string]float64{
				"accumulated_power_consumption_kwh":     12.5,
				"accumulated_electricity_purchased_kwh": 5,
				"accumulated_pv_energy_yield_kwh":       7,
			},
			want: 12.5,
		},
		{
			name: "zero counter falls back to algebraic balance",
			in: map[string]float64{
				"accumulated_power_consumption_kwh":     0,
				"accumulated_electricity_purchased_kwh": 5,
				"accumulated_pv_energy_yield_kwh":       7,
				"total_energy_discharged_kwh":           3,
				"total_energy_charged_kwh":              2,
			},
			want: 13, // 5 + 7 + 3 - 2
		},
		{
			name: "negative balance is clamped to zero",
			in: map[string]float64{
				"accumulated_power_consumption_kwh":     0,
				"accumulated_electricity_purchased_kwh": 0,
				"accumulated_pv_energy_yield_kwh":       0,
				"total_energy_discharged_kwh":           0,
				"total_energy_charged_kwh":              10,
			},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.in["accumulated_power_consumption_kwh"]
			applyApplianceConsumptionRule(tc.in)
			got, ok := tc.in["accumulated_power_consumption_kwh"]
			if tc.keep {
				if ok {
					t.Fatalf("expected consumption key to remain absent, got %v", got)
				}
				return
			}
			if !ok {
				t.Fatalf("expected consumption key to be present after rule")
			}
			_ = before
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("consumption = %v, want %v", got, tc.want)
			}
		})
	}
}
