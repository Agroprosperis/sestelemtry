package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
)

func sameMoney(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

// pricedDayBackend serves one stored, final day with one priced hour.
type pricedDayBackend struct{ stubEconomicsBackend }

func (pricedDayBackend) LoadDay(_ context.Context, orgID string, day time.Time, tz string) (economics.StoredDay, bool, error) {
	rdn := 4.0
	rows := make([]*economics.HourRow, 24)
	rows[12] = &economics.HourRow{
		Hour: 12, HourStart: day.Add(12 * time.Hour), Rdn: &rdn,
		Flow: economics.HourFlows{PV: 10},
		Econ: economics.HourEconomics{Load: 8, ImportPrice: 6, ExportPrice: 3.2, Effect: 40},
	}
	return economics.StoredDay{OrganizationID: orgID, Day: day, Tz: tz, Rows: rows, IsFinal: true}, true, nil
}

func getJSON[T any](t *testing.T, h *Handlers, url string) (int, T) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	var out T
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: decode: %v", url, err)
		}
	}
	return rec.Code, out
}

// A day in a currency is the stored UAH day at that day's rate: money and
// prices convert, energy stays, and the response says what it is in.
func TestEconomicsDailyInCurrency(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetEconomicsService(economics.NewService(pricedDayBackend{}))
	h.SetFxRates(&fakeFxStore{rates: map[string]map[string]float64{"USD": {"2026-11-01": 40}}}, &fakeFxSync{})
	day := func(query string) (int, EconomicsDailyResponse) {
		return getJSON[EconomicsDailyResponse](t, h, "/api/v1/economics/daily?organization_id=ze&tz=Europe/Kyiv&"+query)
	}

	code, usd := day("date=2026-11-01&currency=USD")
	if code != http.StatusOK || usd.Currency != "USD" || usd.FxRate != 40 || usd.FxCarried {
		t.Fatalf("USD day: %d %+v", code, usd)
	}
	hour := usd.Hours[12]
	if !sameMoney(hour.EffectUah, 1) || !sameMoney(*hour.RdnUahPerKwh, 0.1) || !sameMoney(hour.ImportPriceUahPerKwh, 0.15) {
		t.Errorf("USD hour money: %+v", hour)
	}
	if hour.PvKwh != 10 || hour.LoadKwh != 8 {
		t.Errorf("USD hour energy changed: %+v", hour)
	}

	if _, carried := day("date=2026-11-02&currency=usd"); !carried.FxCarried || carried.FxRate != 40 {
		t.Errorf("a day without its own rate: %+v", carried)
	}
	if _, uah := day("date=2026-11-01"); uah.Currency != "UAH" || uah.FxRate != 0 || uah.Hours[12].EffectUah != 40 {
		t.Errorf("UAH day: %+v", uah)
	}
	if code, _ := day("date=2026-11-01&currency=GBP"); code != http.StatusBadRequest {
		t.Errorf("an unknown currency: %d", code)
	}
	if code, _ := day("date=2026-11-01&currency=EUR"); code != http.StatusServiceUnavailable {
		t.Errorf("a currency without rates: %d", code)
	}
}

// The portfolio and the month add up converted days, each at its own rate.
func TestEconomicsRollupsInCurrency(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	day := func(y int, m time.Month, d int, ebitda float64) economics.DailyRecord {
		return economics.DailyRecord{Day: time.Date(y, m, d, 0, 0, 0, 0, loc), IsFinal: true, Totals: economics.DailyTotals{
			Ebitda: ebitda, RevenueTotal: ebitda, RevenuePvSelf: ebitda, PV: 2 * ebitda, HoursWithData: 24,
		}}
	}
	h := NewHandlers(&mockStore{}, "*")
	h.SetOrganizations([]OrganizationInfo{{ID: "a"}, {ID: "b"}})
	h.SetEconomicsService(economics.NewService(portfolioBackend{days: map[string][]economics.DailyRecord{
		"a": {day(2026, 3, 10, 1000)},
		"b": {day(2026, 7, 1, 2000)},
	}}))
	h.SetFxRates(&fakeFxStore{rates: map[string]map[string]float64{"USD": {"2026-03-10": 40, "2026-07-01": 50}}}, &fakeFxSync{})

	code, year := getJSON[EconomicsPortfolioResponse](t, h, "/api/v1/economics/portfolio?tz=Europe/Kyiv&period=2026&currency=USD")
	if code != http.StatusOK || year.Currency != "USD" {
		t.Fatalf("portfolio: %d %+v", code, year)
	}
	if !sameMoney(year.Totals.EbitdaUah, 1000.0/40+2000.0/50) || year.Totals.PvKwh != 6000 {
		t.Errorf("portfolio ebitda %v pv %v, want 65 and 6000", year.Totals.EbitdaUah, year.Totals.PvKwh)
	}
	if len(year.Trend) != 12 || !sameMoney(year.Trend[2].EbitdaUah, 25) || !sameMoney(year.Trend[6].EbitdaUah, 40) {
		t.Errorf("trend = %+v, want Mar 25 and Jul 40", year.Trend)
	}
	if year.FxRateAvg < 40 || year.FxRateAvg > 50 || year.FxDaysCarried == 0 {
		t.Errorf("rate window: avg %v, carried %d", year.FxRateAvg, year.FxDaysCarried)
	}

	code, month := getJSON[EconomicsMonthlyResponse](t, h, "/api/v1/economics/monthly?organization_id=a&tz=Europe/Kyiv&month=2026-03&currency=USD")
	if code != http.StatusOK || month.Currency != "USD" || month.FxRateAvg != 40 || month.FxDaysCarried != 30 {
		t.Fatalf("month: %d currency %q avg %v carried %d", code, month.Currency, month.FxRateAvg, month.FxDaysCarried)
	}
	if !sameMoney(month.Totals.EbitdaUah, 25) {
		t.Errorf("month ebitda %v, want 25", month.Totals.EbitdaUah)
	}
	if _, uah := getJSON[EconomicsMonthlyResponse](t, h, "/api/v1/economics/monthly?organization_id=a&tz=Europe/Kyiv&month=2026-03"); uah.Currency != "UAH" || uah.Totals.EbitdaUah != 1000 {
		t.Errorf("UAH month: currency %q ebitda %v", uah.Currency, uah.Totals.EbitdaUah)
	}
}
