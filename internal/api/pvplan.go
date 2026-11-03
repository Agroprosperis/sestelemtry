package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

// Reading a station's PV plan, from whichever source its passport
// selects. The n8n source lives in pvplan_n8n.go, the passport routes in
// pvplan_passport.go.

// pvPlanStation returns org's passport. A station without one is
// planned by n8n, as every station was before passports existed. A
// failed read is an error, not "no passport": guessing n8n would serve a
// station switched to the model the flow's numbers, and would drop
// Дубовʼязівський's whole-elevator day right where control reads it.
func (h *Handlers) pvPlanStation(ctx context.Context, org string) (pvplan.Station, error) {
	st, ok, err := h.store.PvPlanStation(ctx, org)
	if err != nil {
		return pvplan.Station{}, err
	}
	if !ok {
		return pvplan.Station{OrganizationID: org, Source: pvplan.SourceN8N}, nil
	}
	return st, nil
}

// pvPlanSummary answers GET /api/v1/pv-plan-summary with the planned
// generation for [from, to) in kWh, so the dashboard can show plan vs
// actual on the month and year presets the same way the day preset
// does from the hourly forecast it already plots.
//
// `to` is clamped to today: the forecast horizon is short, so including
// the rest of the current month or year would compare a full-period
// plan against elapsed-period actuals and read as a collapse in
// performance. Today itself is counted whole, matching what the day
// card does with a day still in progress.
func (h *Handlers) pvPlanSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	from, to, _, tz, err := parseRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if from.IsZero() || to.IsZero() {
		http.Error(w, "from and to are required", http.StatusBadRequest)
		return
	}
	loc, err := loadLocation(tz)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !to.After(from) {
		http.Error(w, "to must be after from", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, h.pvPlanTotal(r.Context(), orgID, from, to, loc))
}

// pvPlanTotal sums the planned generation of the civil days [from, to)
// touches, clamped to today, from the source the passport selects. The
// summary route and the assistant's pv_plan tool both answer from it.
//
// The model's day totals are its day-ahead plans. The n8n totals are
// cached in pv_plan_daily and filled from the flow on the way.
func (h *Handlers) pvPlanTotal(ctx context.Context, orgID string, from, to time.Time, loc *time.Location) PvPlanSummaryResponse {
	resp := PvPlanSummaryResponse{OrganizationID: orgID}
	st, err := h.pvPlanStation(ctx, orgID)
	if err != nil {
		h.log.Warn("api_pv_plan_summary_station", "organization_id", orgID, "err", err)
		return resp
	}
	resp.Source = string(st.Source)
	code, n8nOK := pvplan.ElevatorCodeFor(orgID)
	switch st.Source {
	case pvplan.SourceModel:
		resp.Supported = st.Modelled()
	default:
		resp.Supported = n8nOK
	}
	if !resp.Supported {
		return resp
	}

	fromDay, toDay, _ := civilDaySpan(from, to, loc)
	if fromDay.IsZero() {
		return resp
	}
	today := startOfCivilDay(time.Now(), loc)
	if toDay.After(today) {
		toDay = today
	}
	if toDay.Before(fromDay) {
		// The whole period is still in the future: no plan to report,
		// and nothing about that is an error.
		return resp
	}
	resp.FromDay = fromDay.Format("2006-01-02")
	resp.ToDay = toDay.Format("2006-01-02")
	resp.DaysExpected = int(civilDayIndex(toDay)-civilDayIndex(fromDay)) + 1
	if !st.CoversDay(resp.ToDay) {
		resp.WholeElevatorFrom = st.WholeElevatorFrom
	}

	start := time.Now()
	var (
		plans   map[string]PvPlanDayTotal
		fetched int
	)
	if st.Source == pvplan.SourceModel {
		rows, err := h.store.PvPlanModelDays(ctx, orgID, fromDay, toDay)
		if err != nil {
			h.log.Warn("api_pv_plan_summary_model", "organization_id", orgID, "err", err)
			return resp
		}
		plans = make(map[string]PvPlanDayTotal, len(rows))
		for _, row := range rows {
			plans[pvPlanKey(row.Day)] = row
		}
	} else {
		var ok bool
		plans, fetched, ok = h.n8nPlanDays(ctx, orgID, code, fromDay, toDay, today)
		if !ok {
			return resp
		}
	}

	for day := fromDay; !day.After(toDay); day = day.AddDate(0, 0, 1) {
		if p, ok := plans[pvPlanKey(day)]; ok && p.PlannedKwh > 0 {
			resp.PlannedKwh += p.PlannedKwh
			resp.DaysCovered++
		}
	}
	h.log.Info("api_pv_plan_summary_ok",
		"organization_id", orgID,
		"source", resp.Source,
		"from_day", resp.FromDay, "to_day", resp.ToDay,
		"days_covered", resp.DaysCovered,
		"days_expected", resp.DaysExpected,
		"fetched_days", fetched,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return resp
}

// pvForecast answers GET /api/v1/pv-forecast?organization_id&date&tz
// with the day's hourly plan for the day chart and the CSV export. The
// browser no longer calls n8n itself: the n8n source is read here,
// through the same cache control uses.
func (h *Handlers) pvForecast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	dateStr := strings.TrimSpace(r.URL.Query().Get("date"))
	loc, err := loadLocation(strings.TrimSpace(r.URL.Query().Get("tz")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	day, err := time.ParseInLocation("2006-01-02", dateStr, loc)
	if err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	st, err := h.pvPlanStation(r.Context(), orgID)
	if err != nil {
		h.log.Error("api_pv_forecast_station", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp := PvForecastResponse{
		OrganizationID: orgID,
		Date:           dateStr,
		Source:         string(st.Source),
		Hours:          []PvForecastHour{},
		WholeElevator:  !st.CoversDay(dateStr),
	}
	var byHour map[int]float64
	switch st.Source {
	case pvplan.SourceModel:
		resp.Supported = st.Modelled()
		if resp.Supported {
			hours, computed, err := h.store.PvPlanHours(r.Context(), orgID, civilDate(day), pvplan.DayAhead)
			if err != nil {
				h.log.Error("api_pv_forecast", "organization_id", orgID, "date", dateStr, "err", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			byHour = hours
			if !computed.IsZero() {
				resp.ComputedAt = &computed
			}
		}
	default:
		code, ok := h.n8nCode(orgID)
		resp.Supported = ok
		if ok {
			hours, err := h.n8nDayHours(r.Context(), orgID, code, day, loc)
			if err != nil {
				// A flow hiccup hides the plan line; it is not a failure
				// of the day chart.
				h.log.Warn("api_pv_forecast_n8n", "organization_id", orgID, "date", dateStr, "err", err)
			}
			byHour = hours
		}
	}
	for hour, kwh := range byHour {
		if kwh <= 0 {
			continue
		}
		resp.Hours = append(resp.Hours, PvForecastHour{Hour: hour, PlannedKwh: kwh})
		resp.PlannedKwh += kwh
	}
	sort.Slice(resp.Hours, func(i, j int) bool { return resp.Hours[i].Hour < resp.Hours[j].Hour })
	writeJSON(w, http.StatusOK, resp)
}

// civilDate is the calendar date of a site-local midnight as a UTC
// midnight, the form date columns are queried with.
func civilDate(day time.Time) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func pvPlanKey(day time.Time) string {
	return day.Format("2006-01-02")
}

// pvPlanControlHours is the plan control acts on for one civil day, by
// local hour start: the model's freshest revision (its day-ahead plan
// when no fresher one is stored), or the n8n day. ok is false when
// there is none, or when the station's planes describe the whole
// elevator that day — planning one site's battery on the whole
// elevator's sun would overcharge it.
func (h *Handlers) pvPlanControlHours(ctx context.Context, org string, day time.Time, loc *time.Location) (map[int]float64, bool) {
	st, err := h.pvPlanStation(ctx, org)
	if err != nil {
		h.log.Warn("api_pv_plan_control_station", "organization_id", org, "err", err)
		return nil, false
	}
	if !st.CoversDay(day.Format("2006-01-02")) {
		return nil, false
	}
	if st.Source == pvplan.SourceModel {
		if !st.Modelled() {
			return nil, false
		}
		for _, kind := range []pvplan.Kind{pvplan.Latest, pvplan.DayAhead} {
			hours, _, err := h.store.PvPlanHours(ctx, org, civilDate(day), kind)
			if err != nil {
				h.log.Warn("api_pv_plan_control", "organization_id", org, "day", day.Format("2006-01-02"), "err", err)
				return nil, false
			}
			if len(hours) > 0 {
				return hours, true
			}
		}
		return nil, false
	}
	code, ok := h.n8nCode(org)
	if !ok {
		return nil, false
	}
	hours, err := h.n8nDayHours(ctx, org, code, day, loc)
	if err != nil {
		h.log.Warn("api_pv_plan_control_n8n", "organization_id", org, "day", day.Format("2006-01-02"), "err", err)
		return nil, false
	}
	return hours, len(hours) > 0
}
