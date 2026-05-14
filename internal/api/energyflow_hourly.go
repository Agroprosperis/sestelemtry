package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
)

// hoursPerDay is the fixed slot count for one calendar day, in the
// timezone the caller asks for. The handler always returns exactly
// this many rows so the dashboard never has to special-case DST: any
// short / long day on the gregorian boundary still maps to 24 logical
// hours of one-hour wallclock width, which is what the analyst
// expects in a daily report.
const hoursPerDay = 24

// energyFlowSourceLookback decides how far before the day boundary
// the storage layer scans for accumulator readings. The on-the-fly
// allocator needs at least one sample strictly before each interval
// so Allocate() has a `prev` to subtract from. We take the same
// value the existing /energy-summary uses (no explicit lookback —
// the storage default is generous). Day-boundary samples are rare
// in practice because the collector polls every second.
const energyFlowSourceLookback = 0

func (h *Handlers) energyFlowHourly(w http.ResponseWriter, r *http.Request) {
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
	if dateStr == "" {
		http.Error(w, "date is required (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	tzStr := strings.TrimSpace(r.URL.Query().Get("tz"))
	loc, err := loadLocation(tzStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	parsedDate, err := time.ParseInLocation("2006-01-02", dateStr, loc)
	if err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	dayStart := time.Date(parsedDate.Year(), parsedDate.Month(), parsedDate.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	// Refuse far-future dates: the allocator would just return
	// zeros, but accepting them masks a typo in the dashboard's
	// URL builder. We allow up to 36 hours into the future so a
	// user crossing a timezone boundary near midnight can still
	// inspect "today" in their local zone without the server
	// rejecting it because UTC has already rolled to tomorrow.
	if dayStart.After(time.Now().UTC().Add(36 * time.Hour)) {
		http.Error(w, "date is too far in the future", http.StatusBadRequest)
		return
	}

	resp, err := h.computeEnergyFlowHourly(r.Context(), orgID, dayStart, dayEnd)
	if err != nil {
		h.log.Error("api_energy_flow_hourly",
			"organization_id", orgID,
			"date", dateStr,
			"tz", loc.String(),
			"err", err,
		)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	resp.OrganizationID = orgID
	resp.Date = dateStr
	resp.Tz = loc.String()

	h.log.Info("api_energy_flow_hourly_ok",
		"organization_id", orgID,
		"date", dateStr,
		"tz", loc.String(),
		"hours", len(resp.Hours),
	)
	writeJSON(w, http.StatusOK, resp)
}

// computeEnergyFlowHourly is the testable core of the handler. It
// pulls the raw allocator inputs once for the whole day, runs the
// shared `energyflow.IterateIntervals` walk once across all 1440+
// allocator buckets, and attributes each non-skipped Allocate result
// to the hour-of-day (in `loc`) of its `curr` timestamp. The
// 24-element slice is always returned in chronological order with
// `Hour == h.From.In(loc).Hour()`. Hours that have no rows in the
// underlying telemetry_samples table still appear in the slice with
// zero values (and an empty Warnings) so the dashboard can iterate
// without per-index nil checks.
//
// Bucketing per Allocate call (rather than pre-splitting rows by
// hour) is what keeps the per-hour totals' sum byte-identical to a
// daily Recompute: an interval that straddles the seam between two
// hours is counted exactly once, against whichever hour contains
// its end timestamp — matching `bucketEnd`'s right-closed convention
// in [internal/energyflow/recompute.go](internal/energyflow/recompute.go).
func (h *Handlers) computeEnergyFlowHourly(
	ctx context.Context,
	orgID string,
	dayStart, dayEnd time.Time,
) (*EnergyFlowHourlyResponse, error) {
	loc := dayStart.Location()
	rows, err := h.store.EnergyFlowSources(ctx, orgID, dayStart, dayEnd, energyFlowSourceLookback)
	if err != nil {
		return nil, fmt.Errorf("energy_flow sources: %w", err)
	}
	cfg := h.energyFlowOrgs[orgID]

	out := &EnergyFlowHourlyResponse{
		Hours: make([]EnergyFlowHourlyRow, hoursPerDay),
	}
	for h := 0; h < hoursPerDay; h++ {
		hourStart := dayStart.Add(time.Duration(h) * time.Hour)
		out.Hours[h] = EnergyFlowHourlyRow{
			Hour: h,
			From: hourStart,
			To:   hourStart.Add(time.Hour),
		}
	}

	if len(rows) < 2 {
		return out, nil
	}

	rawSamples := buildRawSamples(rows, cfg)
	var stats energyflow.RecomputeResult
	energyflow.IterateIntervals(
		rawSamples,
		energyflow.Options{
			EssDischargeSign:        cfg.EssDischargeSign,
			AllocationWindowSeconds: 60,
			MaxGapSeconds:           0,
		},
		func(curr time.Time, r energyflow.Result) {
			h := curr.In(loc).Hour()
			if h < 0 || h >= hoursPerDay {
				return
			}
			row := &out.Hours[h]
			row.PVToESSKwh += r.PVToESSKwh
			row.GridToESSKwh += r.GridToESSKwh
			row.ESSToLoadKwh += r.ESSToLoadKwh
			row.ESSToGridKwh += r.ESSToGridKwh
			// EssChargedKwh / EssDischargedKwh land here verbatim
			// (post-validation copy from the Allocate result), so a
			// counter rollback inside the hour clamps to zero rather
			// than poisoning the dashboard's hourly load math.
			row.EssChargedKwh += r.EssChargedKwh
			row.EssDischargedKwh += r.EssDischargedKwh
		},
		&stats,
	)

	// Skipped-interval / warning attribution: the iterate loop
	// doesn't tell us which hour an Allocate call was skipped for
	// (the callback simply isn't invoked), so we surface the bulk
	// counter on hour 0 as a coarse "this day has gaps" indicator.
	// Day-zero is the conventional carrier for whole-period
	// diagnostics across the API; the dashboard can show it once
	// in the day header rather than spreading repeated warnings
	// across every hour row.
	if stats.SkippedIntervals > 0 {
		out.Hours[0].SkippedIntervals = stats.SkippedIntervals
	}
	if len(stats.Warnings) > 0 {
		out.Hours[0].Warnings = append(out.Hours[0].Warnings, stats.Warnings...)
	}
	return out, nil
}
