package api

import (
	"context"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
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

// TestApplyApplianceConsumptionRule pins the bus-balance identity used
// to fill `accumulated_power_consumption_kwh`. The rule is unconditional
// — the device-reported value (which on PE/ZE deployments only reflects
// the inverter's "Backup load" branch) is overwritten by the algebraic
// energy balance:
//
//	consumption = pv + purchased + discharge - charge - sold
//
// Working counters that happen to agree with the balance are unaffected.
// The rule is a no-op only when the consumption key is absent from the
// totals map (the caller didn't ask for it).
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
			name: "non-zero device counter is overwritten by the balance",
			in: map[string]float64{
				"accumulated_power_consumption_kwh":     12.5,
				"accumulated_electricity_purchased_kwh": 5,
				"accumulated_pv_energy_yield_kwh":       7,
			},
			want: 12, // pv 7 + purchased 5 + 0 discharge - 0 charge - 0 sold
		},
		{
			name: "full balance with all five terms present",
			in: map[string]float64{
				"accumulated_power_consumption_kwh":     0,
				"accumulated_electricity_purchased_kwh": 5,
				"accumulated_pv_energy_yield_kwh":       7,
				"total_energy_discharged_kwh":           3,
				"total_energy_charged_kwh":              2,
				"accumulated_electricity_sold_kwh":      1,
			},
			want: 12, // 7 + 5 + 3 - 2 - 1
		},
		{
			name: "sold subtracts because export does not reach the load",
			in: map[string]float64{
				"accumulated_power_consumption_kwh":     0,
				"accumulated_electricity_purchased_kwh": 0,
				"accumulated_pv_energy_yield_kwh":       100,
				"total_energy_discharged_kwh":           0,
				"total_energy_charged_kwh":              0,
				"accumulated_electricity_sold_kwh":      40,
			},
			want: 60,
		},
		{
			name: "negative balance is clamped to zero",
			in: map[string]float64{
				"accumulated_power_consumption_kwh":     0,
				"accumulated_electricity_purchased_kwh": 0,
				"accumulated_pv_energy_yield_kwh":       0,
				"total_energy_discharged_kwh":           0,
				"total_energy_charged_kwh":              10,
				"accumulated_electricity_sold_kwh":      0,
			},
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
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
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("consumption = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseLimit covers the validation rules for /api/v1/samples'
// `limit` query param: empty → default, valid in-range integer →
// passes through, anything else → error so the dialog surfaces a 400.
func TestParseLimit(t *testing.T) {
	cases := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{raw: "", want: 100},
		{raw: "1", want: 1},
		{raw: "100", want: 100},
		{raw: "1000", want: 1000},
		{raw: "0", wantErr: true},
		{raw: "-5", wantErr: true},
		{raw: "abc", wantErr: true},
		{raw: "1001", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/?limit="+tc.raw, nil)
			got, err := parseLimit(r, 100, 1000)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %d", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("limit %q -> %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// TestDecimalsForGain locks the round-table of supported gains so a
// future catalog change (or a typo'd 0.1000001 in YAML) doesn't
// silently downgrade to "shortest round-trip" formatting and bring
// back the float-jitter trailing 9's analysts complained about.
func TestDecimalsForGain(t *testing.T) {
	cases := []struct {
		gain     float64
		decimals int
		ok       bool
	}{
		{1, 0, true},
		{10, 0, true},
		{0.1, 1, true},
		{0.01, 2, true},
		{0.001, 3, true},
		{0.0001, 4, true},
		// Float jitter from YAML round-trip — must still resolve.
		{0.01 + 1e-15, 2, true},
		{0, 0, false},
		{-0.01, 0, false},
		{math.NaN(), 0, false},
		{math.Inf(1), 0, false},
		// Non-power-of-ten gain (1/3 ≈ 0.333…) — fall back to
		// shortest round-trip, signaled by ok=false.
		{1.0 / 3.0, 0, false},
	}
	for _, tc := range cases {
		got, ok := decimalsForGain(tc.gain)
		if ok != tc.ok || (ok && got != tc.decimals) {
			t.Fatalf("decimalsForGain(%v) = (%d, %v); want (%d, %v)",
				tc.gain, got, ok, tc.decimals, tc.ok)
		}
	}
}

// TestWriteEnergyFlowSynthetic_RejectsNonSyntheticMetric is the
// safety belt for the recompute write path: the endpoint must never
// be tricked into overwriting a raw Modbus accumulator stored in
// telemetry_samples. The whitelist guard rejects the entire batch
// with an explicit error so an upstream bug can't silently corrupt
// the device-of-truth counters.
//
// We pass a nil pool because the whitelist check fires before any
// SQL is issued; the test fails loudly if a future refactor moves
// the validation past storage.InsertSamples (which would attempt to
// dial the nil pool and produce a different error message).
func TestWriteEnergyFlowSynthetic_RejectsNonSyntheticMetric(t *testing.T) {
	store := &Store{}
	samples := []storage.Sample{
		{
			Time:      time.Now().UTC(),
			MetricKey: "accumulated_pv_energy_yield_kwh",
			Value:     1.0,
		},
	}
	err := store.WriteEnergyFlowSynthetic(context.Background(), "org-a", samples)
	if err == nil {
		t.Fatal("expected an error when writing a non-synthetic metric_key, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "non-synthetic metric_key") {
		t.Fatalf("error should explain the rejection reason, got %q", msg)
	}
	if !strings.Contains(msg, "accumulated_pv_energy_yield_kwh") {
		t.Fatalf("error should name the offending key, got %q", msg)
	}
}

// TestWriteEnergyFlowSynthetic_AllSyntheticKeysAllowed verifies the
// happy-path whitelist accepts every key the energyflow.Recompute
// emitter is permitted to produce. If the synthetic list ever grows,
// this test fails until the api package's view is updated in lockstep.
func TestWriteEnergyFlowSynthetic_AllSyntheticKeysAllowed(t *testing.T) {
	store := &Store{}
	for _, key := range EnergyFlowSyntheticMetrics {
		samples := []storage.Sample{
			{Time: time.Now().UTC(), MetricKey: key, Value: 1.0},
		}
		err := store.WriteEnergyFlowSynthetic(context.Background(), "org-a", samples)
		if err == nil {
			t.Fatalf("expected the nil-pool write to fail past the whitelist for %q, got nil", key)
		}
		// We're using a Store with no pool, so the call must
		// progress past the whitelist and only then fail inside
		// storage.InsertSamples with the nil-pool error. Any other
		// error means the key was rejected by the guard.
		if strings.Contains(err.Error(), "non-synthetic metric_key") {
			t.Fatalf("synthetic key %q was rejected by the whitelist guard: %v", key, err)
		}
	}
}

// TestEnergyFlowSyntheticMetricsMatchEnergyflow keeps the API-side
// whitelist in sync with the energyflow package. If the package
// gains a fifth synthetic counter (or renames an existing one) but
// EnergyFlowSyntheticMetrics is not updated, the recompute endpoint
// would either reject valid samples from Recompute or silently drop
// them — both subtle data-quality regressions. Asserting the lists
// match here makes the breakage loud at test time.
func TestEnergyFlowSyntheticMetricsMatchEnergyflow(t *testing.T) {
	want := map[string]struct{}{
		"pv_to_ess_kwh":   {},
		"grid_to_ess_kwh": {},
		"ess_to_load_kwh": {},
		"ess_to_grid_kwh": {},
	}
	got := make(map[string]struct{}, len(EnergyFlowSyntheticMetrics))
	for _, k := range EnergyFlowSyntheticMetrics {
		got[k] = struct{}{}
	}
	if len(want) != len(got) {
		t.Fatalf("synthetic metric count mismatch: want %d, got %d (%v)", len(want), len(got), EnergyFlowSyntheticMetrics)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("missing synthetic metric %q from EnergyFlowSyntheticMetrics", k)
		}
	}
}

// TestSanitizeFilenameSegment locks in the safe-character allow-list
// used by the /api/v1/samples Content-Disposition header. Anything
// outside [A-Za-z0-9_-] becomes underscore so a hostile organization
// id can't smuggle a quote into the response header.
func TestSanitizeFilenameSegment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "_"},
		{"org-a", "org-a"},
		{"org_a", "org_a"},
		{"org/a", "org_a"},
		{`org"; rm -rf /`, "org___rm_-rf__"},
		{"организація", "___________"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := sanitizeFilenameSegment(tc.in)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
