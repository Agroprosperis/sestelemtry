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
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/storage"
)

// genStore reports generator data for the organizations in since, and
// their generator passports.
type genStore struct {
	*mockStore
	since     map[string]time.Time
	passports map[string]storage.GeneratorPassportRow
}

func (s genStore) FirstSampleTime(_ context.Context, org, _ string) (time.Time, bool, error) {
	t, ok := s.since[org]
	return t, ok, nil
}

func (s genStore) GeneratorPassport(_ context.Context, org string) (storage.GeneratorPassportRow, bool, error) {
	row, ok := s.passports[org]
	return row, ok, nil
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

// genChatHandlers configures ke, de and de1 next to the test
// organizations, all three with generator data.
func genChatHandlers(t *testing.T, ms *mockStore, passports map[string]storage.GeneratorPassportRow) *Handlers {
	t.Helper()
	since := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	h, _ := authTestHandlers(t, genStore{mockStore: ms, since: map[string]time.Time{"ke": since, "de": since, "de1": since}, passports: passports})
	h.SetOrganizations(append(slices.Clone(testOrganizations),
		OrganizationInfo{ID: "ke", Name: "KE"}, OrganizationInfo{ID: "de", Name: "DE"}, OrganizationInfo{ID: "de1", Name: "DE1"}))
	return h
}

func chatToolFor(t *testing.T, h *Handlers, role auth.Role, org, name string, loc *time.Location) aichat.Tool {
	t.Helper()
	req := principalRequest(&auth.Principal{UserID: 1, Grants: []auth.Grant{{Role: role}}})
	return chatTool(t, h.chatTools(req, org, loc), name)
}

func callChatTool(t *testing.T, tool aichat.Tool, args string) string {
	t.Helper()
	res, _, err := tool.Call(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", tool.Name, err)
	}
	return aichat.Compact(res)
}

func wantIn(t *testing.T, who, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s lacks %s: %s", who, w, got)
		}
	}
}

func wantOut(t *testing.T, who, got string, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("%s has %s: %s", who, w, got)
		}
	}
}

// Кролевецький, a day on the generator at 82.5 ₴/l and 0.3 l/kWh: the
// assistant reads its energy and minutes wherever the economics tools
// open, the fuel only where the money shows. aichat.Compact sorts the
// keys of what the tools return.
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
	h := genChatHandlers(t, &mockStore{}, nil)
	h.SetEconomicsService(economics.NewService(genDayBackend{
		portfolioBackend: portfolioBackend{days: map[string][]economics.DailyRecord{
			"ke": {{Day: day, IsFinal: true, Totals: totals}},
			"pe": {{Day: day, IsFinal: true, Totals: plain}},
		}},
		day: economics.StoredDay{OrganizationID: "ke", Day: day, Tz: chatTZ, Rows: []*economics.HourRow{hour}, Totals: totals, IsFinal: true},
	}))
	tool := func(role auth.Role, org, name string) aichat.Tool { return chatToolFor(t, h, role, org, name, loc) }

	energy := `"generator":{"gen_kwh":100,"gen_to_ess_kwh":20,"gen_to_load_kwh":80,"load_offgrid_kwh":120,"minutes_blackout":3,"minutes_generator":120,"minutes_island":15}`
	fuel := `"generator":{"effect_offgrid_uah":900,"expense_gen_charge_uah":495,"gen_fuel_cost_uah":2475,"gen_fuel_l":30,"gen_fuel_method":"per_kwh"}`

	dayTool := tool(auth.RoleEconomist, "ke", "economics_day")
	wantIn(t, "economist's day", callChatTool(t, dayTool, `{"date":"2026-11-15"}`), energy, fuel,
		`"gen_fuel_cost_uah":1237.5,"gen_fuel_l":15,"gen_kwh":50,`, `"hour":20,"load_kwh":60,"minutes_generator":60,`)
	wantIn(t, "economist's day tool", dayTool.Description, "money.generator", "gen_fuel_cost_uah і gen_fuel_l")

	month := tool(auth.RoleEngineer, "ke", "economics_month")
	got := callChatTool(t, month, `{"month":"2026-11"}`)
	wantIn(t, "engineer's month", got, energy, `"gen_kwh":100,"grid_export_kwh"`, `"minutes_blackout":3,"minutes_generator":120,"minutes_island":15,"pv_kwh"`)
	wantOut(t, "engineer's month", got, "gen_fuel", "effect_offgrid", `"money"`)
	wantIn(t, "engineer's month tool", month.Description, "Пальне (гривні й літри) — теж лише")
	wantOut(t, "engineer's month tool", month.Description, "money.generator")

	wantIn(t, "ceo's month", callChatTool(t, tool(auth.RoleCEO, "ke", "economics_month"), `{"month":"2026-11"}`),
		fuel, `"gen_fuel_cost_uah":2475,"gen_kwh":100,"grid_export_kwh"`)
	wantIn(t, "ceo's year", callChatTool(t, tool(auth.RoleCEO, "ke", "economics_year"), `{"year":"2026"}`), energy, fuel,
		`"gen_fuel_cost_uah":2475,"gen_kwh":100,"grid_export_kwh"`, `"minutes_generator":120,"month":"2026-11"`)

	// A site without a generator reads as before.
	plainMonth := tool(auth.RoleCEO, "pe", "economics_month")
	wantOut(t, "pe month", callChatTool(t, plainMonth, `{"month":"2026-11"}`), "generator", "gen_")
	wantOut(t, "pe month tool", plainMonth.Description, "energy.generator")

	// Т2 shares its generator with Т1: the fuel is the elevator's, so the
	// site's tools say where it is; the elevator's read it as usual.
	shared := tool(auth.RoleCEO, "de", "economics_month").Description
	wantIn(t, "de month tool", shared, "Генератор спільний для майданчиків об'єкта «Дубов'язівський елеватор»")
	wantOut(t, "de month tool", shared, "money.generator")
	wantIn(t, "de-all month tool", tool(auth.RoleCEO, "de-all", "economics_month").Description, "money.generator")

	ceo := principalRequest(&auth.Principal{UserID: 1, Grants: []auth.Grant{{Role: auth.RoleCEO}}})
	res, _, err := chatTool(t, h.chatTools(ceo, "ke", loc), "portfolio").Call(context.Background(), json.RawMessage(`{"period":"2026-11"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.(map[string]any)["sites"].([]chatPortfolioSite) {
		if s.ID == "ke" && (s.GenKwh != 100 || s.GenFuelCostUah != 2475) {
			t.Errorf("portfolio ke = %+v", s)
		}
		if s.ID == "pe" && (s.GenKwh != 0 || s.GenFuelCostUah != 0) {
			t.Errorf("portfolio pe = %+v", s)
		}
	}
}

// The live snapshot tells the supply mode the way the live diagram
// does; the energy balance moves the generator out of the УЗЕ flows; the
// passport carries the «Генератор» block with the fuel in force today.
func TestChatSnapshotBalancePassportCarryTheGenerator(t *testing.T) {
	loc, err := time.LoadLocation(chatTZ)
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	now := time.Now()
	read := func(key string, v float64, age time.Duration) CurrentMetric {
		return CurrentMetric{MetricKey: key, Value: v, Time: now.Add(-age)}
	}
	snapshot := func(breakersAge time.Duration) CurrentResponse {
		return CurrentResponse{Metrics: map[string]CurrentMetric{
			"active_pv_power_kw":             read("active_pv_power_kw", 40, time.Second),
			energyflow.SrcGridBreakersOnBits: read(energyflow.SrcGridBreakersOnBits, 0, breakersAge),
			energyflow.SrcGenBreakersOnBits:  read(energyflow.SrcGenBreakersOnBits, 1, breakersAge),
			energyflow.SrcActiveGenPowerKw:   read(energyflow.SrcActiveGenPowerKw, 180, breakersAge),
		}}
	}
	f := func(v float64) *float64 { return &v }
	run, link, alive := now.Add(-time.Hour), now.Add(-5*time.Second), 1.0
	ms := &mockStore{
		currentResp: snapshot(2 * time.Second),
		summaryResp: EnergySummaryResponse{Totals: map[string]float64{"accumulated_power_consumption_kwh": 900}},
		flowDailyTotals: EnergyFlowTotals{PVToESSKwh: 10, Gen: &EnergyFlowGenTotals{
			GenKwh: 300, GenToLoadKwh: 250, GenToESSKwh: 50, Accounted: true, MinutesGenerator: 95, MinutesIsland: 20,
		}},
		flowDailyCovered: 3,
		inventory:        &PlantInventoryResponse{Time: now.Add(-time.Hour), PVRatedKw: f(1500)},
		tariffVersions: map[string][]TariffScheduleVersion{
			"ke": {{EffectiveFrom: "2026-01-01", Tariffs: OrgTariffs{GenFuelPriceUahPerL: 82.5, GenFuelMethod: "per_kwh", GenFuelConsumptionLPerKwh: 0.3}}},
			"de": {{EffectiveFrom: "2026-01-01", Tariffs: OrgTariffs{GenFuelPriceUahPerL: 80, GenFuelMethod: "per_hour", GenFuelConsumptionLPerH: 20}}},
		},
	}
	passport := storage.GeneratorPassportRow{
		DeviceHost: "10.24.40.239", LastSampleAt: &link, RatedKw: f(400), MeterKwh: f(41234.5), MeterAt: &link,
		MeterAlive: &alive, SwVersion: f(2.17), LastRunAt: &run,
	}
	h := genChatHandlers(t, ms, map[string]storage.GeneratorPassportRow{"ke": passport, "de1": passport})
	h.SetEconomicsService(economics.NewService(stubEconomicsBackend{}))
	tool := func(role auth.Role, org, name string) aichat.Tool { return chatToolFor(t, h, role, org, name, loc) }

	current := tool(auth.RoleCEO, "ke", "current_snapshot")
	wantIn(t, "ke snapshot", callChatTool(t, current, `{}`), `"supply_mode":"generator"`)
	wantIn(t, "ke snapshot tool", current.Description, "supply_mode — режим живлення")
	ms.currentResp = snapshot(45 * time.Second)
	wantIn(t, "stale snapshot", callChatTool(t, current, `{}`), `"supply_mode":"unknown"`)
	plain := tool(auth.RoleCEO, "pe", "current_snapshot")
	wantOut(t, "pe snapshot", callChatTool(t, plain, `{}`), "supply_mode")
	wantOut(t, "pe snapshot tool", plain.Description, "supply_mode")

	balance := callChatTool(t, tool(auth.RoleCEO, "ke", "energy_balance"), `{"from":"2026-11-13","to":"2026-11-15"}`)
	wantIn(t, "ke balance", balance,
		`"generator":{"accounted":true,"gen_kwh":300,"gen_to_ess_kwh":50,"gen_to_load_kwh":250,"minutes_generator":95,"minutes_island":20}`,
		`"ess_flows":{"ess_to_grid_kwh":0,"ess_to_load_kwh":0,"grid_to_ess_kwh":0,"pv_to_ess_kwh":10}`)

	wantIn(t, "admin passport", callChatTool(t, tool(auth.RoleAdmin, "ke", "station_passport"), `{}`),
		`"device_host":"10.24.40.239"`, `"link_ok":true`, `"meter_alive":true`, `"rated_kw":400`,
		`"fuel":{"consumption_l_per_kwh":0.3,"cost_uah_per_kwh":24.75,"include_vat":false,"method":"per_kwh","price_uah_per_l":82.5}`)
	// Т1's controller reads the shared generator; its fuel is Т2's.
	wantIn(t, "de1 passport", callChatTool(t, tool(auth.RoleAdmin, "de1", "station_passport"), `{}`),
		`"fuel":{"consumption_l_per_h":20,"cost_uah_per_h":1600,"include_vat":false,"method":"per_hour","price_uah_per_l":80}`)
	wantOut(t, "pe passport", callChatTool(t, tool(auth.RoleAdmin, "pe", "station_passport"), `{}`), "generator")
}
