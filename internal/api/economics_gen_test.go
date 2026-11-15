package api

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/storage"
)

// An hour without generator data serves the same JSON as before.
func TestEconomicsHourJSONWithoutGen(t *testing.T) {
	rdn := 2.0
	body, err := json.Marshal(economicsHourToJSON(&economics.HourRow{Hour: 3, Rdn: &rdn}))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"gen_", "minutes_", "offgrid"} {
		if strings.Contains(string(body), key) {
			t.Fatalf("hour JSON carries %q: %s", key, body)
		}
	}
	accounted := &economics.HourRow{
		GenKwh: 70, SupplyMinutes: economics.SupplyMinutes{Generator: 60},
		Flow: economics.HourFlows{Gen: economics.GenFlows{Accounted: true, GenKwh: 70, GenToLoad: 70}},
		Econ: economics.HourEconomics{Load: 100, GenFuelCostUah: 1050, GenFuelMethod: economics.GenFuelPerKwh},
	}
	body, _ = json.Marshal(economicsHourToJSON(accounted))
	for _, key := range []string{`"gen_kwh":70`, `"gen_accounted":true`, `"minutes_generator":60`, `"gen_fuel_cost_uah":1050`, `"gen_fuel_method":"per_kwh"`} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("hour JSON lacks %s: %s", key, body)
		}
	}
}

func TestGenFlowRowMinutes(t *testing.T) {
	if genFlowRow(nil) != nil {
		t.Fatal("no gen block, no generator row")
	}
	got := genFlowRow(&EnergyFlowGenHour{GenKwh: 5, Minutes: map[string]int{
		"generator": 30, "island": 20, "blackout": 5, "transition": 3, "unknown": 2, "grid": 0,
	}})
	want := economics.SupplyMinutes{Generator: 30, Island: 20, Blackout: 5, Transition: 3, Unknown: 2}
	if got.Minutes != want || got.GenKwh != 5 {
		t.Fatalf("got %+v", got)
	}
}

func TestGenHourStorageRoundTrip(t *testing.T) {
	start := time.Date(2026, 10, 20, 2, 0, 0, 0, time.UTC)
	row := &economics.HourRow{
		Hour: 2, HourStart: start, GenKwh: 150, GenEstimated: true,
		SupplyMinutes: economics.SupplyMinutes{Generator: 60},
		Flow: economics.HourFlows{EssCharged: 50, Gen: economics.GenFlows{
			Accounted: true, GenKwh: 150, GenToEss: 50, GenToLoad: 100, LoadOffgrid: 100,
			Minutes: economics.SupplyMinutes{Generator: 60},
		}},
		Econ: economics.HourEconomics{
			Load: 100, LoadOffgrid: 100, GenFuelCostUah: 2250, GenFuelL: 45, GenFuelMethod: economics.GenFuelPerKwh, GenChargeCostUah: 750,
		},
	}
	stored := hourRowToStorage("ke", row)
	back := storageToHourRow(2, &stored)
	if back.Flow.Gen != row.Flow.Gen || back.SupplyMinutes != row.SupplyMinutes || back.GenKwh != 150 || !back.GenEstimated {
		t.Fatalf("round trip lost the generator: %+v", back)
	}
	if back.Econ.GenFuelCostUah != 2250 || back.Econ.GenChargeCostUah != 750 || back.Econ.GenFuelMethod != economics.GenFuelPerKwh || back.Econ.LoadOffgrid != 100 {
		t.Fatalf("round trip lost the fuel: %+v", back.Econ)
	}

	// Not counted: only the reference fields come back, the flows stay zero.
	plain := &economics.HourRow{Hour: 2, HourStart: start, GenKwh: 150, SupplyMinutes: economics.SupplyMinutes{Generator: 60}}
	stored = hourRowToStorage("ke", plain)
	back = storageToHourRow(2, &stored)
	if back.Flow.Gen != (economics.GenFlows{}) || back.GenKwh != 150 {
		t.Fatalf("plain hour: %+v", back)
	}

	day := economics.StoredDay{OrganizationID: "ke", Day: start, Totals: economics.DailyTotals{GenTotals: economics.GenTotals{
		GenKwh: 260, GenFuelCost: 3900, GenFuelMethod: economics.GenFuelPerKwh, ExpenseGenCharge: 750,
		SupplyMinutes: economics.SupplyMinutes{Generator: 150, Island: 30},
	}}}
	if got := storageToDailyTotals(dailyToStorage(day)); got.GenTotals != day.Totals.GenTotals {
		t.Fatalf("day round trip: %+v", got.GenTotals)
	}
}

func TestFlowTotalsFromDailySums(t *testing.T) {
	if got := flowTotalsFromDailySums(storage.EnergyFlowDailySums{PVToESSKwh: 1}); got.Gen != nil {
		t.Fatalf("no generator, no block: %+v", got.Gen)
	}
	got := flowTotalsFromDailySums(storage.EnergyFlowDailySums{GenKwh: 300})
	if got.Gen == nil || got.Gen.GenKwh != 300 || got.Gen.Accounted {
		t.Fatalf("not counted: %+v", got.Gen)
	}
	got = flowTotalsFromDailySums(storage.EnergyFlowDailySums{GenKwh: 300, GenToLoadKwh: 250, GenToESSKwh: 50})
	if got.Gen == nil || !got.Gen.Accounted {
		t.Fatalf("counted: %+v", got.Gen)
	}
}

// fuelBackend is an economics backend whose every day prices the fuel.
type fuelBackend struct{ stubEconomicsBackend }

func (fuelBackend) TariffSchedule(context.Context, string) (economics.Schedule, error) {
	return economics.Schedule{{EffectiveFrom: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), Tariffs: economics.Tariffs{
		GenFuelPriceUahPerL: 50, GenFuelMethod: economics.GenFuelPerKwh, GenFuelConsumptionLPerKwh: 0.3,
	}}}, nil
}

// The live summary keeps its four flows while the generator is not
// counted and adds the generator's energy; once the economics count it,
// the generator's split comes along.
func TestEnergySummaryGenLive(t *testing.T) {
	dayStart := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	smart := syntheticDayRows(dayStart)
	rows := append(append([]EnergyFlowRawRow(nil), smart...), generatorHourRows(dayStart, 18)...)
	ctx := context.Background()

	plain, err := NewHandlers(&mockStore{flowSources: smart}, "*").computeEnergyFlowTotals(ctx, "demo", dayStart, dayStart.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlers(&mockStore{flowSources: rows}, "*")
	got, err := h.computeEnergyFlowTotals(ctx, "demo", dayStart, dayStart.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got.PVToESSKwh != plain.PVToESSKwh || got.GridToESSKwh != plain.GridToESSKwh ||
		got.ESSToLoadKwh != plain.ESSToLoadKwh || got.ESSToGridKwh != plain.ESSToGridKwh {
		t.Fatalf("flows changed without accounting: %+v vs %+v", got, plain)
	}
	if got.Gen == nil || got.Gen.GenKwh != 300 || got.Gen.Accounted || got.Gen.GenToLoadKwh != 0 {
		t.Fatalf("gen block = %+v", got.Gen)
	}

	h.SetEconomicsService(economics.NewService(fuelBackend{}))
	got, err = h.computeEnergyFlowTotals(ctx, "demo", dayStart, dayStart.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got.Gen == nil || !got.Gen.Accounted {
		t.Fatalf("gen block = %+v", got.Gen)
	}
	if split := got.Gen.GenToLoadKwh + got.Gen.GenToESSKwh; math.Abs(split-300) > 1 {
		t.Fatalf("generator split %g, want the 300 kWh", split)
	}
}

// generatorHourRows is an Encombi running the site off the grid for the
// clock hour starting at hour: 300 kW, 300 kWh on its meter.
func generatorHourRows(dayStart time.Time, hour int) []EnergyFlowRawRow {
	from := hour * 3600
	rows := make([]EnergyFlowRawRow, 0, 4000)
	for s := from; s <= from+3600; s += 5 {
		ts := dayStart.Add(time.Duration(s) * time.Second)
		for k, v := range map[string]float64{
			energyflow.SrcGridBreakersOnBits: 0,
			energyflow.SrcGenBreakersOnBits:  1,
			energyflow.SrcActiveGenPowerKw:   300,
			energyflow.SrcGenBusVoltageAV:    230,
			energyflow.SrcGenMeterAlive:      1,
		} {
			rows = append(rows, EnergyFlowRawRow{Time: ts, MetricKey: k, Value: v, DeviceHost: "10.24.40.239"})
		}
	}
	return append(rows,
		EnergyFlowRawRow{Time: dayStart.Add(time.Duration(hour) * time.Hour), MetricKey: energyflow.SrcAccumulatedGenEnergyKwh, Value: 331000, DeviceHost: "10.24.40.239"},
		EnergyFlowRawRow{Time: dayStart.Add(time.Duration(hour+1) * time.Hour), MetricKey: energyflow.SrcAccumulatedGenEnergyKwh, Value: 331300, DeviceHost: "10.24.40.239"},
	)
}
