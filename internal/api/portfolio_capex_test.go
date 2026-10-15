package api

import "testing"

func TestResolveCapexUah(t *testing.T) {
	versions := []TariffScheduleVersion{
		{EffectiveFrom: epochEffectiveFrom, Tariffs: OrgTariffs{CapexUah: 99e6}},
		{EffectiveFrom: "2025-06-01", Tariffs: OrgTariffs{CapexUah: 0}},
		{EffectiveFrom: "2025-06-20", Tariffs: OrgTariffs{CapexUah: 21e6}},
		{EffectiveFrom: "2026-03-15", Tariffs: OrgTariffs{CapexUah: 31e6}},
	}
	cases := []struct {
		month string
		want  float64
	}{
		{"2025-01", 21e6}, // before the first stage: the first stage's capital
		{"2025-12", 21e6},
		{"2026-03", 31e6}, // a stage mid-month counts for the whole month
		{"2026-12", 31e6},
	}
	for _, c := range cases {
		if got := resolveCapexUah(versions, 5e6, c.month); got != c.want {
			t.Errorf("%s: capex = %v, want %v", c.month, got, c.want)
		}
	}
	// Without a dated stage the tariff form's value stands.
	if got := resolveCapexUah(versions[:2], 5e6, "2026-12"); got != 5e6 {
		t.Errorf("form fallback = %v", got)
	}
	if got := resolveCapexUah(nil, 0, "2026-12"); got != 0 {
		t.Errorf("no capex = %v", got)
	}
}
