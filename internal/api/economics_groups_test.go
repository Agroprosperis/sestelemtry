package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/aichat"
	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/pvplan"
)

// Дубовʼязівський in these tests: de metered the whole elevator until Т1
// got its own logger, de1 is Т1 from 2026-09-24, and de-all reads them as
// one elevator.
var dubovyazivka = []OrganizationInfo{{ID: "de", Name: "Т2"}, {ID: "de1", Name: "Т1"}, {ID: "ze", Name: "ZE"}}

func groupDays(t *testing.T) map[string][]economics.DailyRecord {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	day := func(m time.Month, d int, ebitda float64) economics.DailyRecord {
		return economics.DailyRecord{Day: time.Date(2026, m, d, 0, 0, 0, 0, loc), IsFinal: true, Totals: economics.DailyTotals{
			Ebitda: ebitda, RevenueTotal: ebitda, RevenuePvSelf: ebitda, PV: 10, HoursWithData: 24,
		}}
	}
	return map[string][]economics.DailyRecord{
		"de": {day(9, 20, 1000), day(10, 5, 500)},
		// Before de1 joined the group its days are not the elevator's.
		"de1": {day(9, 20, 777), day(10, 5, 300)},
		"ze":  {day(10, 5, 50)},
	}
}

func groupHandlers(t *testing.T, store *mockStore) *Handlers {
	t.Helper()
	h := NewHandlers(store, "*")
	h.SetOrganizations(dubovyazivka)
	h.SetEconomicsService(economics.NewService(economics.InGroups(portfolioBackend{days: groupDays(t)}, economics.Groups)))
	return h
}

// The economics pages read Дубовʼязівський as one elevator: its months add
// de and de1 up from the day de1 joined, and the portfolio shows one row
// for it in place of the two sites.
func TestEconomicGroupReportsTheElevatorAsOne(t *testing.T) {
	h := groupHandlers(t, &mockStore{})
	month := func(m string) float64 {
		t.Helper()
		code, resp := getJSON[EconomicsMonthlyResponse](t, h, "/api/v1/economics/monthly?organization_id=de-all&tz=Europe/Kyiv&month="+m)
		if code != http.StatusOK {
			t.Fatalf("month %s: %d", m, code)
		}
		return resp.Totals.EbitdaUah
	}
	if got := month("2026-10"); got != 800 {
		t.Errorf("October EBITDA %v, want de 500 + de1 300", got)
	}
	if got := month("2026-09"); got != 1000 {
		t.Errorf("September EBITDA %v, want de's 1000 alone", got)
	}

	code, portfolio := getJSON[EconomicsPortfolioResponse](t, h, "/api/v1/economics/portfolio?tz=Europe/Kyiv&period=2026")
	if code != http.StatusOK {
		t.Fatalf("portfolio: %d", code)
	}
	var ids []string
	for _, s := range portfolio.Sites {
		ids = append(ids, s.ID)
		if s.ID == "de-all" && s.Name != "Дубов'язівський елеватор" {
			t.Errorf("group row named %q", s.Name)
		}
	}
	if len(ids) != 2 || ids[0] != "de-all" || ids[1] != "ze" {
		t.Errorf("portfolio rows %v, want [de-all ze]", ids)
	}
	if portfolio.Totals.EbitdaUah != 1850 {
		t.Errorf("portfolio EBITDA %v, want 1850", portfolio.Totals.EbitdaUah)
	}
}

// A group's tariffs are its members' in force, combined: de's prices, the
// УЗЕ and the capital of both. The split day keeps the elevator's 860 kWh
// and 31.15 M, and a group's tariffs are not written through the group.
func TestEconomicGroupTariffsCombineItsMembers(t *testing.T) {
	version := func(day string, capacity, power, capex float64) TariffScheduleVersion {
		return TariffScheduleVersion{EffectiveFrom: day, Tariffs: OrgTariffs{
			DistributionUahPerKwh: 4.1, EssCapacityKwh: capacity, EssPowerLimitKw: power, CapexUah: capex,
		}}
	}
	store := &mockStore{
		tariffVersions: map[string][]TariffScheduleVersion{
			"de":  {version("1970-01-01", 860, 540, 0), version("2026-07-17", 860, 540, 31_149_769), version("2026-09-24", 516, 324, 23_598_551)},
			"de1": {version("1970-01-01", 344, 216, 7_551_218), version("2026-09-24", 344, 216, 7_551_218)},
		},
		// de's form still holds the whole elevator, as in prod after the
		// split: the group's tariffs must come from the schedule.
		tariffsByOrg: map[string]OrgTariffs{
			"de":  {DistributionUahPerKwh: 4.1, EssCapacityKwh: 860, EssPowerLimitKw: 540, CapexUah: 31_149_769},
			"de1": {DistributionUahPerKwh: 9.9, EssCapacityKwh: 344, EssPowerLimitKw: 216, CapexUah: 7_551_218},
		},
	}
	h := groupHandlers(t, store)

	code, schedule := getJSON[struct {
		Versions []TariffScheduleVersion `json:"versions"`
	}](t, h, "/api/v1/organization-tariff-schedule?organization_id=de-all")
	if code != http.StatusOK || len(schedule.Versions) != 3 {
		t.Fatalf("schedule: %d %+v", code, schedule.Versions)
	}
	for _, v := range schedule.Versions[1:] {
		if v.Tariffs.EssCapacityKwh != 860 || v.Tariffs.EssPowerLimitKw != 540 || v.Tariffs.CapexUah != 31_149_769 {
			t.Errorf("%s: %+v, want 860 kWh / 540 kW / 31 149 769", v.EffectiveFrom, v.Tariffs)
		}
	}
	if schedule.Versions[2].EffectiveFrom != "2026-09-24" || schedule.Versions[2].Tariffs.DistributionUahPerKwh != 4.1 {
		t.Errorf("split day version %+v, want de's prices on 2026-09-24", schedule.Versions[2])
	}

	code, form := getJSON[OrgTariffs](t, h, "/api/v1/organization-tariffs?organization_id=de-all")
	if code != http.StatusOK || form.CapexUah != 31_149_769 || form.EssCapacityKwh != 860 || form.DistributionUahPerKwh != 4.1 {
		t.Errorf("form: %d %+v", code, form)
	}

	code, portfolio := getJSON[EconomicsPortfolioResponse](t, h, "/api/v1/economics/portfolio?tz=Europe/Kyiv&period=2026")
	if code != http.StatusOK || len(portfolio.Sites) == 0 || portfolio.Sites[0].ID != "de-all" || portfolio.Sites[0].CapexUah != 31_149_769 {
		t.Errorf("portfolio CAPEX: %d %+v", code, portfolio.Sites)
	}

	router := h.Router()
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		target := "/api/v1/organization-tariff-schedule?organization_id=de-all&effective_from=2026-10-01"
		if rec := serve(router, method, target, nil, map[string]any{"effective_from": "2026-10-01", "tariffs": OrgTariffs{EssCapacityKwh: 1}}); rec.Code != http.StatusBadRequest {
			t.Errorf("%s schedule of a group: %d, want 400", method, rec.Code)
		}
	}
	if rec := serve(router, http.MethodPut, "/api/v1/organization-tariffs?organization_id=de-all", nil, OrgTariffs{EssCapacityKwh: 1}); rec.Code != http.StatusBadRequest || store.tariffsLastOrg != "" {
		t.Errorf("PUT tariffs of a group: %d, wrote %q", rec.Code, store.tariffsLastOrg)
	}
}

// Reading a group takes a grant on every member, and the group is listed
// to whoever holds those grants.
func TestEconomicGroupNeedsEveryMember(t *testing.T) {
	h, accounts := authTestHandlers(t, nil)
	h.SetOrganizations(dubovyazivka)
	h.SetEconomicsService(economics.NewService(economics.InGroups(portfolioBackend{days: groupDays(t)}, economics.Groups)))
	router := h.Router()
	month := "/api/v1/economics/monthly?organization_id=de-all&tz=Europe/Kyiv&month=2026-10"

	half := sessionCookie(t, accounts, "t2@example.com", auth.Grant{Role: auth.RoleEconomist, OrganizationID: "de"})
	if rec := serve(router, http.MethodGet, month, half, nil); rec.Code != http.StatusForbidden {
		t.Errorf("a grant on de alone: %d, want 403", rec.Code)
	}

	both := sessionCookie(t, accounts, "all@example.com",
		auth.Grant{Role: auth.RoleEconomist, OrganizationID: "de"}, auth.Grant{Role: auth.RoleEconomist, OrganizationID: "de1"})
	if rec := serve(router, http.MethodGet, month, both, nil); rec.Code != http.StatusOK {
		t.Errorf("grants on de and de1: %d, want 200", rec.Code)
	}
	var me AuthMeResponse
	if err := json.Unmarshal(serve(router, http.MethodGet, "/api/v1/auth/me", both, nil).Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if len(me.Groups) != 1 || me.Groups[0].ID != "de-all" || len(me.Groups[0].Permissions) != 1 || me.Groups[0].Permissions[0] != auth.PermEconomicsRead {
		t.Errorf("me groups = %+v, want de-all with economics.read", me.Groups)
	}
	var orgs OrganizationsResponse
	if err := json.Unmarshal(serve(router, http.MethodGet, "/api/v1/organizations", both, nil).Body.Bytes(), &orgs); err != nil {
		t.Fatal(err)
	}
	if len(orgs.Groups) != 1 || orgs.Groups[0].ID != "de-all" || len(orgs.Groups[0].Members) != 2 {
		t.Errorf("organizations groups = %+v", orgs.Groups)
	}
	if err := json.Unmarshal(serve(router, http.MethodGet, "/api/v1/auth/me", half, nil).Body.Bytes(), &me); err != nil || len(me.Groups) != 0 {
		t.Errorf("de alone sees groups %+v (err %v)", me.Groups, err)
	}
}

// The assistant opened on a group reads its economics, its plan and the
// market as the group's, and the telemetry, weather and passport of the
// site it names in each call: a group has none of its own. It goes by the
// group's name.
func TestEconomicGroupAssistantReadsEachSite(t *testing.T) {
	store := &mockStore{weatherResp: WeatherForecastResponse{Daily: []WeatherForecastDay{{Day: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)}}}}
	h := groupHandlers(t, store)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat?organization_id=de-all", nil)
	tools := map[string]aichat.Tool{}
	var names []string
	for _, tool := range h.chatTools(req, "de-all", time.UTC) {
		names = append(names, tool.Name)
		tools[tool.Name] = tool
	}
	want := []string{"current_snapshot", "energy_balance", "weather_forecast", "pv_plan", "station_passport",
		"economics_day", "economics_month", "economics_year", "uze_plan", "portfolio", "dam_prices"}
	if !slices.Equal(names, want) {
		t.Errorf("tools on a group = %v, want %v", names, want)
	}

	weather := tools["weather_forecast"]
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(weather.Parameters, &schema); err != nil || !slices.Equal(schema.Properties["site"].Enum, []string{"de", "de1"}) ||
		!slices.Equal(schema.Required, []string{"site", "from", "to"}) {
		t.Errorf("weather schema %s (err %v), want a required site of de, de1", weather.Parameters, err)
	}
	_, src, err := weather.Call(req.Context(), json.RawMessage(`{"site":"de1","from":"2026-11-10","to":"2026-11-10"}`))
	if err != nil || store.weatherOrg != "de1" || src.Label != "Т1 · Прогноз погоди Open-Meteo" {
		t.Errorf("weather of de1: read %q, source %q, err %v", store.weatherOrg, src.Label, err)
	}
	if _, _, err := weather.Call(req.Context(), json.RawMessage(`{"site":"de-all","from":"2026-11-10","to":"2026-11-10"}`)); err == nil {
		t.Error("weather of the group itself answered")
	}
	if got := h.organizationName("de-all"); got != "Дубов'язівський елеватор" {
		t.Errorf("group name for the assistant = %q", got)
	}
}

// Since the split both passports carry the whole elevator's planes, so the
// group's plan is one copy of them, not two; before de1 joined it is de's.
func TestEconomicGroupPlansTheElevatorOnce(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	whole := func(org string) pvplan.Station {
		st := modelStation(org)
		st.WholeElevatorFrom = "2026-09-24"
		return st
	}
	store := &mockStore{
		pvStations: map[string]pvplan.Station{"de": whole("de"), "de1": whole("de1")},
		pvModelDays: []PvPlanDayTotal{
			{Day: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), PlannedKwh: 80},
			{Day: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), PlannedKwh: 100},
		},
	}
	h := groupHandlers(t, store)
	q := url.Values{
		"organization_id": {"de-all"},
		"from":            {time.Date(2026, 9, 20, 0, 0, 0, 0, kyiv).Format(time.RFC3339)},
		"to":              {time.Date(2026, 10, 6, 0, 0, 0, 0, kyiv).Format(time.RFC3339)},
		"tz":              {"Europe/Kyiv"},
	}
	code, plan := getJSON[PvPlanSummaryResponse](t, h, "/api/v1/pv-plan-summary?"+q.Encode())
	if code != http.StatusOK || !plan.Supported || plan.PlannedKwh != 180 || plan.DaysCovered != 2 || plan.WholeElevatorFrom != "" {
		t.Errorf("group plan: %d %+v, want 80 + 100 kWh over 2 days", code, plan)
	}
}
