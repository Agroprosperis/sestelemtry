package api

import (
	"context"
	"sync"
	"time"

	"github.com/nesh/sestelemetry/internal/pvplan"
)

// The n8n source: stations whose passport still selects the flow are
// read from it through a server-side cache, and their month/year day
// totals through the pv_plan_daily cache filled on demand.

const (
	// pvPlanTodayTTL is how long a cached n8n plan for a day that is
	// still running (or hasn't started) stays usable. The flow
	// republishes the current day's forecast a few times a day as the
	// weather model updates, so a short TTL keeps today honest without
	// re-asking on every dashboard poll.
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

// pvN8NCache keeps n8n site-days the day chart and control read, so a
// dashboard poll or a 15-minute planner pass doesn't call the flow
// every time.
type pvN8NCache struct {
	mu sync.Mutex
	m  map[string]cachedPvPlan // key: org|YYYY-MM-DD
}

type cachedPvPlan struct {
	byHour map[int]float64 // local hour start 0..23 → kWh (= avg kW)
	at     time.Time
}

// n8nDayHours returns one n8n site-day by local hour start. A finished
// day is cached for pvPlanMissRetry, a day still running for
// pvPlanTodayTTL; failures are not cached.
func (h *Handlers) n8nDayHours(ctx context.Context, org, code string, day time.Time, loc *time.Location) (map[int]float64, error) {
	key := org + "|" + day.Format("2006-01-02")
	ttl := pvPlanTodayTTL
	if day.Before(startOfCivilDay(time.Now(), loc)) {
		ttl = pvPlanMissRetry
	}
	h.pvN8N.mu.Lock()
	if ent, ok := h.pvN8N.m[key]; ok && time.Since(ent.at) < ttl {
		h.pvN8N.mu.Unlock()
		return ent.byHour, nil
	}
	h.pvN8N.mu.Unlock()

	// A tighter deadline than the client's own: callers answer a live
	// request and would rather go without a plan than hold it.
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	byHour, err := h.pvPlan.DayHourly(reqCtx, code, day)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	h.pvN8N.mu.Lock()
	if h.pvN8N.m == nil {
		h.pvN8N.m = map[string]cachedPvPlan{}
	}
	// Every day anyone opens adds an entry; drop the ones no TTL can
	// serve any more, so a long-running process doesn't keep them all.
	for k, ent := range h.pvN8N.m {
		if now.Sub(ent.at) >= pvPlanMissRetry {
			delete(h.pvN8N.m, k)
		}
	}
	h.pvN8N.m[key] = cachedPvPlan{byHour: byHour, at: now}
	h.pvN8N.mu.Unlock()
	return byHour, nil
}

// n8nCode returns the flow's code for org when the n8n source can
// serve it.
func (h *Handlers) n8nCode(org string) (string, bool) {
	code, ok := pvplan.ElevatorCodeFor(org)
	return code, ok && h.pvPlan != nil
}

// n8nPlanDays reads the cached n8n day totals of [fromDay, toDay] and
// fills the missing or stale ones from the flow. ok is false when the
// cache can't be read: that says nothing about the period, so the
// caller reports zero coverage rather than a plan that happens to be
// missing most of its days.
func (h *Handlers) n8nPlanDays(ctx context.Context, orgID, code string, fromDay, toDay, today time.Time) (map[string]PvPlanDayTotal, int, bool) {
	cached, err := h.store.PvPlanDays(ctx, orgID, fromDay, toDay)
	if err != nil {
		h.log.Warn("api_pv_plan_summary_cache",
			"organization_id", orgID, "err", err,
			"from_day", fromDay.Format("2006-01-02"), "to_day", toDay.Format("2006-01-02"),
		)
		return nil, 0, false
	}
	plans, stale := pvPlanIndex(cached, today)
	missing := pvPlanMissingDays(fromDay, toDay, plans, stale)
	if len(missing) == 0 || h.pvPlan == nil {
		return plans, 0, true
	}
	fetched := h.fillPvPlanDays(ctx, orgID, code, missing)
	if len(fetched) == 0 {
		return plans, 0, true
	}
	for _, d := range fetched {
		plans[pvPlanKey(d.Day)] = d
	}
	if err := h.store.SavePvPlanDays(ctx, orgID, fetched); err != nil {
		// The numbers are already good for this response; only the next
		// request pays for the lost cache write.
		h.log.Warn("api_pv_plan_summary_save",
			"organization_id", orgID, "err", err, "days", len(fetched),
		)
	}
	return plans, len(missing), true
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
