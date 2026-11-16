package api

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/aichat"
	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/economics"
)

// genStore reports generator data for the organizations in since.
type genStore struct {
	*mockStore
	since map[string]time.Time
}

func (s genStore) FirstSampleTime(_ context.Context, org, _ string) (time.Time, bool, error) {
	t, ok := s.since[org]
	return t, ok, nil
}

// genDayBackend serves portfolioBackend's days and one final day with
// its hours.
type genDayBackend struct {
	portfolioBackend
	day economics.StoredDay
}

func (b genDayBackend) LoadDay(_ context.Context, org string, day time.Time, _ string) (economics.StoredDay, bool, error) {
	if org != b.day.OrganizationID || !day.Equal(b.day.Day) {
		return economics.StoredDay{}, false, nil
	}
	return b.day, true, nil
}

// Кролевецький, a day on the generator at 82.5 ₴/l and 0.3 l/kWh: the
// assistant reads its energy and minutes wherever the economics tools
// open, the fuel only where the money shows.
func TestChatEconomicsCarryTheGenerator(t *testing.T) {
	loc, err := time.LoadLocation(chatTZ)
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	day := time.Date(2026, 11, 15, 0, 0, 0, 0, loc)
	gen := economics.GenTotals{
		GenKwh: 100, GenToLoad: 80, GenToEss: 20, LoadOffgrid: 120,
		GenFuelCost: 2475, GenFuelL: 30, GenFuelMethod: economics.GenFuelPerKwh,
		EffectOffgrid: 900, ExpenseGenCharge: 495,
		SupplyMinutes: economics.SupplyMinutes{Generator: 120, Island: 15, Blackout: 3},
	}
	totals := economics.DailyTotals{PV: 300, Load: 900, GridImport: 500, Effect: 1500, Ebitda: 1800, HoursWithData: 24, GenTotals: gen}
	rdn := 6.0
	hour := &economics.HourRow{
		Hour: 20, HourStart: day.Add(20 * time.Hour), Rdn: &rdn,
		Econ:          economics.HourEconomics{Load: 60, LoadOffgrid: 60, GenFuelCostUah: 1237.5, GenFuelL: 15, GenFuelMethod: economics.GenFuelPerKwh},
		GenKwh:        50,
		SupplyMinutes: economics.SupplyMinutes{Generator: 60},
	}
	plain := economics.DailyTotals{PV: 300, Load: 900, GridImport: 600, Effect: 1500, HoursWithData: 24}
	backend := genDayBackend{
		portfolioBackend: portfolioBackend{days: map[string][]economics.DailyRecord{
			"ke": {{Day: day, IsFinal: true, Totals: totals}},
			"pe": {{Day: day, IsFinal: true, Totals: plain}},
		}},
		day: economics.StoredDay{OrganizationID: "ke", Day: day, Tz: chatTZ, Rows: []*economics.HourRow{hour}, Totals: totals, IsFinal: true},
	}
	store := genStore{mockStore: &mockStore{}, since: map[string]time.Time{"ke": day, "de": day}}
	h, _ := authTestHandlers(t, store)
	h.SetOrganizations(append(slices.Clone(testOrganizations),
		OrganizationInfo{ID: "ke", Name: "KE"}, OrganizationInfo{ID: "de", Name: "DE"}, OrganizationInfo{ID: "de1", Name: "DE1"}))
	h.SetEconomicsService(economics.NewService(backend))

	tool := func(role auth.Role, org, name string) aichat.Tool {
		t.Helper()
		req := principalRequest(&auth.Principal{UserID: 1, Grants: []auth.Grant{{Role: role}}})
		return chatTool(t, h.chatTools(req, org, loc), name)
	}
	call := func(tl aichat.Tool, args string) string {
		t.Helper()
		res, _, err := tl.Call(context.Background(), json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s: %v", tl.Name, err)
		}
		return aichat.Compact(res)
	}
	contains := func(who, got string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s lacks %s: %s", who, w, got)
			}
		}
	}
	lacks := func(who, got string, unwanted ...string) {
		t.Helper()
		for _, w := range unwanted {
			if strings.Contains(got, w) {
				t.Errorf("%s has %s: %s", who, w, got)
			}
		}
	}

	// aichat.Compact sorts the keys.
	energy := `"generator":{"gen_kwh":100,"gen_to_ess_kwh":20,"gen_to_load_kwh":80,"load_offgrid_kwh":120,"minutes_blackout":3,"minutes_generator":120,"minutes_island":15}`
	fuel := `"generator":{"effect_offgrid_uah":900,"expense_gen_charge_uah":495,"gen_fuel_cost_uah":2475,"gen_fuel_l":30,"gen_fuel_method":"per_kwh"}`

	dayTool := tool(auth.RoleEconomist, "ke", "economics_day")
	contains("economist's day", call(dayTool, `{"date":"2026-11-15"}`), energy, fuel,
		`"gen_fuel_cost_uah":1237.5,"gen_fuel_l":15,"gen_kwh":50,`, `"hour":20,"load_kwh":60,"minutes_generator":60,`)
	contains("economist's day tool", dayTool.Description, "money.generator", "gen_fuel_cost_uah і gen_fuel_l")

	month := tool(auth.RoleEngineer, "ke", "economics_month")
	got := call(month, `{"month":"2026-11"}`)
	contains("engineer's month", got, energy, `"gen_kwh":100,"grid_export_kwh"`, `"minutes_blackout":3,"minutes_generator":120,"minutes_island":15,"pv_kwh"`)
	lacks("engineer's month", got, "gen_fuel", "effect_offgrid", `"money"`)
	contains("engineer's month tool", month.Description, "Пальне (гривні й літри) — теж лише")
	lacks("engineer's month tool", month.Description, "money.generator")

	contains("ceo's month", call(tool(auth.RoleCEO, "ke", "economics_month"), `{"month":"2026-11"}`), fuel, `"gen_fuel_cost_uah":2475,"gen_kwh":100,"grid_export_kwh"`)
	contains("ceo's year", call(tool(auth.RoleCEO, "ke", "economics_year"), `{"year":"2026"}`), energy, fuel,
		`"gen_fuel_cost_uah":2475,"gen_kwh":100,"grid_export_kwh"`, `"minutes_generator":120,"month":"2026-11"`)

	// A site without a generator reads as before.
	plainMonth := tool(auth.RoleCEO, "pe", "economics_month")
	lacks("pe month", call(plainMonth, `{"month":"2026-11"}`), "generator", "gen_")
	lacks("pe month tool", plainMonth.Description, "energy.generator")

	// Т2 shares its generator with Т1: the fuel is the elevator's, so the
	// site's tools say where it is; the elevator's read it as usual.
	shared := tool(auth.RoleCEO, "de", "economics_month").Description
	contains("de month tool", shared, "Генератор спільний для майданчиків об'єкта «Дубов'язівський елеватор»")
	lacks("de month tool", shared, "money.generator")
	contains("de-all month tool", tool(auth.RoleCEO, "de-all", "economics_month").Description, "money.generator")
}
