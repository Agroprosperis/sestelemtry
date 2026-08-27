package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

const (
	// pvPlanTodayTTL is how long a cached plan for a day that is still
	// running (or hasn't started) stays usable. The flow republishes the
	// current day's forecast a few times a day as the weather model
	// updates, so a short TTL keeps today honest without re-asking on
	// every dashboard poll.
	pvPlanTodayTTL = 30 * time.Minute
	// pvPlanMissRetry is how long a recorded miss (upstream had no
	// forecast for a finished day) is trusted before being retried. Past
	// forecasts don't change, but the flow can backfill history, so we
	// look again — once a day, not once a page view.
	pvPlanMissRetry = 24 * time.Hour
	// pvPlanFetchConcurrency bounds parallel upstream calls. Filling a
	// year cold is ~365 of them; six at a time keeps that within a
	// request while staying a polite neighbour to a shared n8n instance.
	pvPlanFetchConcurrency = 6
	// pvPlanFetchBudget caps the time spent filling the cache in one
	// request. Whatever landed inside the budget is summed and reported
	// with its coverage; the next poll continues where this one stopped,
	// and since past days are cached permanently the cold cost is paid
	// once per site.
	pvPlanFetchBudget = 25 * time.Second
	// pvPlanMaxFetchDays bounds the fill per request independently of
	// the clock, so a pathological range can't queue thousands of calls.
	pvPlanMaxFetchDays = 400
)

// pvPlanSummary answers GET /api/v1/pv-plan-summary with the planned
// generation for [from, to) in kWh, so the dashboard can show plan vs
// actual on the month and year presets the same way the day preset
// does from the hourly forecast it already plots.
//
// The period plan is the sum of per-day forecasts, cached in
// pv_plan_daily. Days the cache is missing (or is stale on) are fetched
// from the flow inline, within pvPlanFetchBudget, and written back.
//
// `to` is clamped to today: the flow's horizon is about two weeks, so
// including the rest of the current month or year would compare a
// full-period plan against elapsed-period actuals and read as a
// collapse in performance. Today itself is counted whole, matching what
// the day card does with a day still in progress.
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

	resp := PvPlanSummaryResponse{OrganizationID: orgID}
	code, supported := pvplan.ElevatorCodeFor(orgID)
	if !supported {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Supported = true

	fromDay, toDay, _ := civilDaySpan(from, to, loc)
	if fromDay.IsZero() {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	today := startOfCivilDay(time.Now(), loc)
	if toDay.After(today) {
		toDay = today
	}
	if toDay.Before(fromDay) {
		// The whole period is still in the future: no plan to report,
		// and nothing about that is an error.
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.FromDay = fromDay.Format("2006-01-02")
	resp.ToDay = toDay.Format("2006-01-02")
	resp.DaysExpected = int(civilDayIndex(toDay)-civilDayIndex(fromDay)) + 1

	start := time.Now()
	cached, err := h.store.PvPlanDays(r.Context(), orgID, fromDay, toDay)
	if err != nil {
		h.log.Warn("api_pv_plan_summary_cache",
			"organization_id", orgID, "err", err,
			"from_day", resp.FromDay, "to_day", resp.ToDay,
		)
		// A cache read failure says nothing about the period, so report
		// zero coverage and let the client show a placeholder instead of
		// a plan that happens to be missing most of its days.
		writeJSON(w, http.StatusOK, resp)
		return
	}

	plans, stale := pvPlanIndex(cached, today)
	missing := pvPlanMissingDays(fromDay, toDay, plans, stale)
	if len(missing) > 0 && h.pvPlan != nil {
		fetched := h.fillPvPlanDays(r.Context(), orgID, code, missing)
		if len(fetched) > 0 {
			for _, d := range fetched {
				plans[pvPlanKey(d.Day)] = d
			}
			if err := h.store.SavePvPlanDays(r.Context(), orgID, fetched); err != nil {
				// The numbers are already good for this response; only
				// the next request pays for the lost cache write.
				h.log.Warn("api_pv_plan_summary_save",
					"organization_id", orgID, "err", err, "days", len(fetched),
				)
			}
		}
	}

	for _, p := range plans {
		if p.PlannedKwh > 0 {
			resp.PlannedKwh += p.PlannedKwh
			resp.DaysCovered++
		}
	}
	h.log.Info("api_pv_plan_summary_ok",
		"organization_id", orgID,
		"from_day", resp.FromDay, "to_day", resp.ToDay,
		"days_covered", resp.DaysCovered,
		"days_expected", resp.DaysExpected,
		"fetched_days", len(missing),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	writeJSON(w, http.StatusOK, resp)
}

func pvPlanKey(day time.Time) string {
	return day.Format("2006-01-02")
}

// pvPlanIndex keys the cached rows by civil date and reports which of
// them need re-asking: a day that hasn't finished yet (its forecast is
// still being revised) and a recorded miss that has aged past its
// retry window.
func pvPlanIndex(cached []PvPlanDayTotal, today time.Time) (plans map[string]PvPlanDayTotal, stale map[string]bool) {
	plans = make(map[string]PvPlanDayTotal, len(cached))
	stale = make(map[string]bool, len(cached))
	now := time.Now()
	for _, row := range cached {
		key := pvPlanKey(row.Day)
		plans[key] = row
		age := now.Sub(row.FetchedAt)
		switch {
		case !row.Day.Before(today) && age > pvPlanTodayTTL:
			stale[key] = true
		case row.PlannedKwh <= 0 && age > pvPlanMissRetry:
			stale[key] = true
		}
	}
	return plans, stale
}

// pvPlanMissingDays walks the civil days in [fromDay, toDay] and
// returns those with no cached plan or a stale one, oldest first.
func pvPlanMissingDays(fromDay, toDay time.Time, plans map[string]PvPlanDayTotal, stale map[string]bool) []time.Time {
	var out []time.Time
	for day := fromDay; !day.After(toDay); day = day.AddDate(0, 0, 1) {
		key := pvPlanKey(day)
		if _, ok := plans[key]; ok && !stale[key] {
			continue
		}
		out = append(out, day)
		if len(out) >= pvPlanMaxFetchDays {
			break
		}
	}
	return out
}

// fillPvPlanDays fetches the given days from the forecast flow with
// bounded concurrency and a wall-clock budget, returning what it got.
// A day the flow has no forecast for comes back with PlannedKwh 0 so
// the miss is cached and not re-asked on the next poll; a day whose
// fetch errored is left out entirely, to be retried.
func (h *Handlers) fillPvPlanDays(ctx context.Context, orgID, code string, days []time.Time) []PvPlanDayTotal {
	ctx, cancel := context.WithTimeout(ctx, pvPlanFetchBudget)
	defer cancel()

	var (
		mu   sync.Mutex
		out  = make([]PvPlanDayTotal, 0, len(days))
		wg   sync.WaitGroup
		sem  = make(chan struct{}, pvPlanFetchConcurrency)
		now  = time.Now()
		errN int
	)
	for _, day := range days {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(day time.Time) {
			defer wg.Done()
			defer func() { <-sem }()
			kwh, ok, err := h.pvPlan.DayTotal(ctx, code, day)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errN++
				return
			}
			if !ok {
				kwh = 0
			}
			out = append(out, PvPlanDayTotal{Day: day, PlannedKwh: kwh, FetchedAt: now})
		}(day)
	}
	wg.Wait()
	if errN > 0 {
		h.log.Warn("api_pv_plan_fetch_partial",
			"organization_id", orgID,
			"requested", len(days), "fetched", len(out), "failed", errN,
		)
	}
	return out
}
