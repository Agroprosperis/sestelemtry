package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nesh/sestelemetry/internal/auth"
	"github.com/nesh/sestelemetry/internal/economics"
)

// demoOrgID is the synthetic demo organization shipped in the example
// config. It is excluded from cross-site rollups (e.g. the portfolio
// dashboard) because it is not a real site.
const demoOrgID = "demo-org"

// EconomicsHour is one hour of the served economics result (flat JSON,
// snake_case). nil entries in the response array render as null.
type EconomicsHour struct {
	Hour         int       `json:"hour"`
	HourStart    time.Time `json:"hour_start"`
	RdnUahPerKwh *float64  `json:"rdn_uah_per_kwh"`

	PvKwh            float64 `json:"pv_kwh"`
	GridImportKwh    float64 `json:"grid_import_kwh"`
	GridExportKwh    float64 `json:"grid_export_kwh"`
	EssChargedKwh    float64 `json:"ess_charged_kwh"`
	EssDischargedKwh float64 `json:"ess_discharged_kwh"`
	PvToEssKwh       float64 `json:"pv_to_ess_kwh"`
	GridToEssKwh     float64 `json:"grid_to_ess_kwh"`
	EssToLoadKwh     float64 `json:"ess_to_load_kwh"`
	EssToGridKwh     float64 `json:"ess_to_grid_kwh"`

	LoadKwh              float64 `json:"load_kwh"`
	PvToLoadKwh          float64 `json:"pv_to_load_kwh"`
	PvToGridKwh          float64 `json:"pv_to_grid_kwh"`
	GridToLoadKwh        float64 `json:"grid_to_load_kwh"`
	ImportPriceUahPerKwh float64 `json:"import_price_uah_per_kwh"`
	ExportPriceUahPerKwh float64 `json:"export_price_uah_per_kwh"`
	BaselineCostUah      float64 `json:"baseline_cost_uah"`
	ActualCostUah        float64 `json:"actual_cost_uah"`
	EffectUah            float64 `json:"effect_uah"`
	EssNetUah            float64 `json:"ess_net_uah"`

	EssRemainingKwhStart     *float64 `json:"ess_remaining_kwh_start"`
	EssCostBasisUahStart     *float64 `json:"ess_cost_basis_uah_start"`
	EssAvgCostUahPerKwhStart *float64 `json:"ess_avg_cost_uah_per_kwh_start"`
	EssWithdrawnCostUah      *float64 `json:"ess_withdrawn_cost_uah"`
	EssRealizedProfitUah     *float64 `json:"ess_realized_profit_uah"`
	EssCostBasisUahEnd       *float64 `json:"ess_cost_basis_uah_end"`
	EssAvgCostUahPerKwhEnd   *float64 `json:"ess_avg_cost_uah_per_kwh_end"`
	EssResidualKwhEnd        *float64 `json:"ess_residual_kwh_end"`
}

// EconomicsDailyResponse is the body of GET /api/v1/economics/daily.
// Hours is always 24 long (nil → null for hours with no flow data).
type EconomicsDailyResponse struct {
	OrganizationID    string           `json:"organization_id"`
	Date              string           `json:"date"`
	Tz                string           `json:"tz"`
	IsFinal           bool             `json:"is_final"`
	HoursMissingPrice int              `json:"hours_missing_price"`
	Hours             []*EconomicsHour `json:"hours"`

	// Reconciled is true when the day's flows were scaled to the
	// canonical FusionSolar daily KPIs. QualityFlags carries diagnostics
	// (e.g. "load_mismatch:0.07"); Reconciliation maps each measured
	// quantity to its computed/canonical/factor detail.
	Reconciled     bool                                `json:"reconciled"`
	QualityFlags   []string                            `json:"quality_flags,omitempty"`
	Reconciliation map[string]economics.ReconcileField `json:"reconciliation,omitempty"`

	// ComputedAt is when the day was last computed (RFC3339, UTC): for a
	// day still running, the moment its numbers are current as of.
	ComputedAt string `json:"computed_at,omitempty"`

	// Currency is what every *_uah field above is in: UAH unless the
	// request asked for ?currency=. FxRate is the day's UAH per unit it
	// was converted at; FxCarried marks a day that borrowed the nearest
	// rate for lack of its own, FxProvisional today's own rate before the
	// rate hour, which the sync still replaces with the current one.
	Currency      string  `json:"currency"`
	FxRate        float64 `json:"fx_rate,omitempty"`
	FxCarried     bool    `json:"fx_carried,omitempty"`
	FxProvisional bool    `json:"fx_provisional,omitempty"`
}

// fxWindowJSON is how a period response reports its currency: the
// fields keep their historical _uah names, every amount is in Currency.
// FxRateAvg is the UAH per unit the period's EBITDA came out at, each
// day's rate weighted by that day's EBITDA (the plain mean of the days'
// rates while there is no EBITDA). FxDaysCarried counts the days that
// borrowed the nearest rate.
type fxWindowJSON struct {
	Currency      string  `json:"currency"`
	FxRateAvg     float64 `json:"fx_rate_avg,omitempty"`
	FxDaysCarried int     `json:"fx_days_carried,omitempty"`
}

func fxWindowFor(view fxView, from, to time.Time, weights economics.EbitdaWeights) fxWindowJSON {
	avg, carried := view.window(from, to)
	if rate, ok := weights.Rate(); ok {
		avg = rate
	}
	return fxWindowJSON{Currency: view.code(), FxRateAvg: avg, FxDaysCarried: carried}
}

// computedAtJSON renders an economics ComputedAt for the wire; zero (no
// stored data) stays empty.
func computedAtJSON(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func economicsHourToJSON(r *economics.HourRow) *EconomicsHour {
	if r == nil {
		return nil
	}
	return &EconomicsHour{
		Hour:                     r.Hour,
		HourStart:                r.HourStart,
		RdnUahPerKwh:             r.Rdn,
		PvKwh:                    r.Flow.PV,
		GridImportKwh:            r.Flow.GridImport,
		GridExportKwh:            r.Flow.GridExport,
		EssChargedKwh:            r.Flow.EssCharged,
		EssDischargedKwh:         r.Flow.EssDischarged,
		PvToEssKwh:               r.Flow.PVToEss,
		GridToEssKwh:             r.Flow.GridToEss,
		EssToLoadKwh:             r.Flow.EssToLoad,
		EssToGridKwh:             r.Flow.EssToGrid,
		LoadKwh:                  r.Econ.Load,
		PvToLoadKwh:              r.Econ.PVToLoad,
		PvToGridKwh:              r.Econ.PVToGrid,
		GridToLoadKwh:            r.Econ.GridToLoad,
		ImportPriceUahPerKwh:     r.Econ.ImportPrice,
		ExportPriceUahPerKwh:     r.Econ.ExportPrice,
		BaselineCostUah:          r.Econ.BaselineCost,
		ActualCostUah:            r.Econ.ActualCost,
		EffectUah:                r.Econ.Effect,
		EssNetUah:                r.Econ.EssNet,
		EssRemainingKwhStart:     r.EssRemainingKwhStart,
		EssCostBasisUahStart:     r.EssCostBasisUahStart,
		EssAvgCostUahPerKwhStart: r.EssAvgCostUahPerKwhStart,
		EssWithdrawnCostUah:      r.EssWithdrawnCostUah,
		EssRealizedProfitUah:     r.EssRealizedProfitUah,
		EssCostBasisUahEnd:       r.EssCostBasisUahEnd,
		EssAvgCostUahPerKwhEnd:   r.EssAvgCostUahPerKwhEnd,
		EssResidualKwhEnd:        r.EssResidualKwhEnd,
	}
}

// economicsDaily serves precomputed economics for one day, read-through
// (final days from cache, otherwise recomputed and persisted on read).
// With ?currency= the stored UAH day is converted at that day's rate on
// the way out; what is computed and persisted stays UAH.
//
//	GET /api/v1/economics/daily?organization_id=&date=YYYY-MM-DD&tz=&currency=
func (h *Handlers) economicsDaily(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.economics == nil {
		http.Error(w, "economics service not configured", http.StatusServiceUnavailable)
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
	dayStart, err := time.ParseInLocation("2006-01-02", dateStr, loc)
	if err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	currency, err := parseCurrency(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	view, err := h.fxViewFor(r.Context(), currency, loc)
	if err != nil {
		h.writeFxViewError(w, "api_economics_daily", err)
		return
	}

	day, err := h.economics.GetDay(r.Context(), orgID, dateStr, loc.String())
	if err != nil {
		h.log.Error("api_economics_daily", "organization_id", orgID, "date", dateStr, "tz", loc.String(), "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	computedAt := day.ComputedAt
	if computedAt.IsZero() {
		// GetDay computed (and saved) the day just now.
		computedAt = time.Now()
	}
	resp := EconomicsDailyResponse{
		OrganizationID: orgID,
		Date:           dateStr,
		Tz:             loc.String(),
		IsFinal:        day.IsFinal,
		Reconciled:     day.Totals.Reconciled,
		QualityFlags:   day.Totals.QualityFlags,
		Reconciliation: day.Totals.Reconciliation,
		ComputedAt:     computedAtJSON(computedAt),
		Currency:       view.code(),
	}
	if !view.uah() {
		rate, carried := view.rate(dayStart)
		day = economics.ScaleDay(day, 1/rate)
		resp.FxRate, resp.FxCarried = rate, carried
		resp.FxProvisional = !carried && fxProvisional(dayStart, time.Now(), loc)
	}
	resp.Hours = make([]*EconomicsHour, len(day.Rows))
	for i, row := range day.Rows {
		resp.Hours[i] = economicsHourToJSON(row)
		if row != nil && row.Rdn == nil {
			resp.HoursMissingPrice++
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// UzePlanHour is one hour of the recommended УЗЕ dispatch.
type UzePlanHour struct {
	Hour int `json:"hour"`
	// RecommendedEssKw is signed like the telemetry metric
	// active_ess_power_kw: positive = discharge, negative = charge.
	RecommendedEssKw float64 `json:"recommended_ess_kw"`
	// SocPct is the modelled state of charge at the END of the hour.
	SocPct float64 `json:"soc_pct"`

	EssToLoadKwh float64 `json:"ess_to_load_kwh"`
	EssToGridKwh float64 `json:"ess_to_grid_kwh"`
	PvToEssKwh   float64 `json:"pv_to_ess_kwh"`
	GridToEssKwh float64 `json:"grid_to_ess_kwh"`

	EffectUah float64 `json:"effect_uah"`

	Action     string `json:"action"`
	ReasonCode string `json:"reason_code"`
	ReasonText string `json:"reason_text"`

	// RecommendedLoadKw is the elevator consumption schedule that soaks
	// up exported PV and cheap РДН hours with the same daily energy.
	// null for hours without telemetry.
	RecommendedLoadKw *float64 `json:"recommended_load_kw"`

	RdnUahPerKwh *float64 `json:"rdn_uah_per_kwh"`
}

// UzePlanTotals is the day's optimum-vs-fact headline plus the waterfall
// legs behind the optimum.
type UzePlanTotals struct {
	OptimumUah    float64 `json:"optimum_uah"`
	FactUah       float64 `json:"fact_uah"`
	ReserveUah    float64 `json:"reserve_uah"`
	CapturedShare float64 `json:"captured_share"`

	ChargePvKwh   float64 `json:"charge_pv_kwh"`
	ChargeGridKwh float64 `json:"charge_grid_kwh"`
	DischargeKwh  float64 `json:"discharge_kwh"`

	ExportValUah    float64 `json:"export_val_uah"`
	LoadValUah      float64 `json:"load_val_uah"`
	ChargePvCostUah float64 `json:"charge_pv_cost_uah"`
	GridCostUah     float64 `json:"grid_cost_uah"`
	DegradationUah  float64 `json:"degradation_uah"`
}

// UzePlanResponse is the body of GET /api/v1/uze-plan.
type UzePlanResponse struct {
	OrganizationID string `json:"organization_id"`
	Date           string `json:"date"`
	Tz             string `json:"tz"`

	// Available is false when the day has no usable hours or the SOC
	// window is degenerate; Hours is then empty.
	Available   bool    `json:"available"`
	SocStartPct float64 `json:"soc_start_pct"`
	CapacityKwh float64 `json:"capacity_kwh"`
	PowerKw     float64 `json:"power_kw"`

	Hours    []UzePlanHour `json:"hours"`
	Totals   UzePlanTotals `json:"totals"`
	Warnings []string      `json:"warnings,omitempty"`
}

// uzePlan serves the recommended УЗЕ dispatch for one day. A past day or
// today is the retrospective benchmark (perfect foresight over realised
// flows). The next civil day in the request timezone is a forecast: today's
// consumption profile, the generation forecast and that day's РДН prices.
//
//	GET /api/v1/uze-plan?organization_id=&date=YYYY-MM-DD&tz=
func (h *Handlers) uzePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.economics == nil {
		http.Error(w, "economics service not configured", http.StatusServiceUnavailable)
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
	loc, err := loadLocation(strings.TrimSpace(r.URL.Query().Get("tz")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	parsed, err := time.ParseInLocation("2006-01-02", dateStr, loc)
	if err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	var plan economics.DayPlan
	if parsed.Equal(today.AddDate(0, 0, 1)) {
		plan, err = h.economics.GetForecastDayPlan(r.Context(), orgID, dateStr, loc.String(), now, h.forecastPV(r.Context(), orgID, parsed, loc))
	} else {
		plan, err = h.economics.GetDayPlan(r.Context(), orgID, dateStr, loc.String())
	}
	if err != nil {
		h.log.Error("api_uze_plan", "organization_id", orgID, "date", dateStr, "tz", loc.String(), "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := UzePlanResponse{
		OrganizationID: plan.OrganizationID,
		Date:           plan.Date,
		Tz:             plan.Tz,
		Available:      plan.Available,
		SocStartPct:    plan.SocStartPct,
		CapacityKwh:    plan.CapacityKwh,
		PowerKw:        plan.PowerKw,
		Hours:          make([]UzePlanHour, len(plan.Hours)),
		Totals: UzePlanTotals{
			OptimumUah:      plan.Totals.OptimumUah,
			FactUah:         plan.Totals.FactUah,
			ReserveUah:      plan.Totals.ReserveUah,
			CapturedShare:   plan.Totals.CapturedShare,
			ChargePvKwh:     plan.Totals.ChargePvKwh,
			ChargeGridKwh:   plan.Totals.ChargeGridKwh,
			DischargeKwh:    plan.Totals.DischargeKwh,
			ExportValUah:    plan.Totals.ExportValUah,
			LoadValUah:      plan.Totals.LoadValUah,
			ChargePvCostUah: plan.Totals.ChargePvCostUah,
			GridCostUah:     plan.Totals.GridCostUah,
			DegradationUah:  plan.Totals.DegradationUah,
		},
		Warnings: plan.Warnings,
	}
	for i, hr := range plan.Hours {
		resp.Hours[i] = UzePlanHour{
			Hour:              hr.Hour,
			RecommendedEssKw:  hr.RecommendedEssKw,
			SocPct:            hr.SocPct,
			EssToLoadKwh:      hr.EssToLoadKwh,
			EssToGridKwh:      hr.EssToGridKwh,
			PvToEssKwh:        hr.PvToEssKwh,
			GridToEssKwh:      hr.GridToEssKwh,
			EffectUah:         hr.EffectUah,
			Action:            hr.Action,
			ReasonCode:        hr.ReasonCode,
			ReasonText:        hr.ReasonText,
			RecommendedLoadKw: hr.RecommendedLoadKw,
			RdnUahPerKwh:      hr.Rdn,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// forecastPV is tomorrow's generation plan in kWh by hour start, from
// the source the station's passport selects. No plan, or planes that
// describe the whole elevator rather than this site, yields nil: the
// recommendation then runs on consumption and prices alone.
func (h *Handlers) forecastPV(ctx context.Context, orgID string, day time.Time, loc *time.Location) map[int]float64 {
	byHour, ok := h.pvPlanControlHours(ctx, orgID, day, loc)
	if !ok {
		return nil
	}
	return byHour
}

// EconomicsMonthExtreme is the best / worst day of the month.
type EconomicsMonthExtreme struct {
	Date      string  `json:"date"`
	EffectUah float64 `json:"effect_uah"`
}

// EconomicsMonthlyTotals is the month rollup in the served response.
type EconomicsMonthlyTotals struct {
	BaselineCostUah float64 `json:"baseline_cost_uah"`
	ActualCostUah   float64 `json:"actual_cost_uah"`
	EffectUah       float64 `json:"effect_uah"`
	EssNetUah       float64 `json:"ess_net_uah"`

	LoadKwh          float64 `json:"load_kwh"`
	PvKwh            float64 `json:"pv_kwh"`
	GridImportKwh    float64 `json:"grid_import_kwh"`
	GridExportKwh    float64 `json:"grid_export_kwh"`
	EssChargedKwh    float64 `json:"ess_charged_kwh"`
	EssDischargedKwh float64 `json:"ess_discharged_kwh"`
	PvToLoadKwh      float64 `json:"pv_to_load_kwh"`
	PvToEssKwh       float64 `json:"pv_to_ess_kwh"`
	PvToGridKwh      float64 `json:"pv_to_grid_kwh"`
	GridToLoadKwh    float64 `json:"grid_to_load_kwh"`
	GridToEssKwh     float64 `json:"grid_to_ess_kwh"`
	EssToLoadKwh     float64 `json:"ess_to_load_kwh"`
	EssToGridKwh     float64 `json:"ess_to_grid_kwh"`

	AvgImportPriceUahPerKwh float64 `json:"avg_import_price_uah_per_kwh"`
	AvgExportPriceUahPerKwh float64 `json:"avg_export_price_uah_per_kwh"`
	RdnAvgUahPerKwh         float64 `json:"rdn_avg_uah_per_kwh"`
	RdnMaxUahPerKwh         float64 `json:"rdn_max_uah_per_kwh"`

	RevenuePvExportUah   float64 `json:"revenue_pv_export_uah"`
	RevenuePvSelfUah     float64 `json:"revenue_pv_self_uah"`
	RevenueEssExportUah  float64 `json:"revenue_ess_export_uah"`
	RevenueEssSelfUah    float64 `json:"revenue_ess_self_uah"`
	RevenueTotalUah      float64 `json:"revenue_total_uah"`
	ExpenseGridChargeUah float64 `json:"expense_grid_charge_uah"`
	ExpenseTotalUah      float64 `json:"expense_total_uah"`
	EbitdaUah            float64 `json:"ebitda_uah"`

	// PvExportPotentialUah values the whole PV yield at the export price
	// of the hour that produced it — the plant with no battery and no
	// on-site load, selling everything to the grid. A yardstick for the
	// dashboard, not a revenue leg.
	PvExportPotentialUah float64 `json:"pv_export_potential_uah"`

	// PvEssExportPotentialUah is the same merchant world with the battery
	// allowed to time-shift the yield to pricier hours (PV charging only,
	// perfect foresight, ККД and wear applied). Always ≥ the bare
	// potential above.
	PvEssExportPotentialUah float64 `json:"pv_ess_export_potential_uah"`

	// ImportCostUah is what the grid import actually cost over the
	// period. It is deliberately NOT an EBITDA expense line: the part
	// consumed directly is already priced in through the
	// self-consumption revenue legs, and only the battery-charging part
	// (ExpenseGridChargeUah) is booked as a cost.
	ImportCostUah float64 `json:"import_cost_uah"`

	EssWithdrawnCostUah         float64 `json:"ess_withdrawn_cost_uah"`
	EssRealizedProfitUah        float64 `json:"ess_realized_profit_uah"`
	EssDegradationCostUah       float64 `json:"ess_degradation_cost_uah"`
	EssAvgCostBasisUahPerKwhEod float64 `json:"ess_avg_cost_basis_uah_per_kwh_eod"`
	EssResidualKwhEod           float64 `json:"ess_residual_kwh_eod"`
	EssCostBasisUahEod          float64 `json:"ess_cost_basis_uah_eod"`

	EquivalentCycles  float64 `json:"equivalent_cycles"`
	DaysWithData      int     `json:"days_with_data"`
	HoursWithData     int     `json:"hours_with_data"`
	HoursMissingPrice int     `json:"hours_missing_price"`
	// FlaggedDays counts days whose flows are approximate because the
	// FusionSolar counters froze / lagged (see MonthlyTotals.FlaggedDays).
	FlaggedDays int `json:"flagged_days"`

	EssFactUah          float64 `json:"ess_fact_uah"`
	EssOptimumUah       float64 `json:"ess_optimum_uah"`
	EssReserveUah       float64 `json:"ess_reserve_uah"`
	EssCapturedShare    float64 `json:"ess_captured_share"`
	EssReserveTimingUah float64 `json:"ess_reserve_timing_uah"`
	EssReserveSocUah    float64 `json:"ess_reserve_soc_uah"`
	EssReservePvUah     float64 `json:"ess_reserve_pv_uah"`
	EssPvMissedKwh      float64 `json:"ess_pv_missed_kwh"`

	EssDataQuality EconomicsDataQuality `json:"ess_data_quality"`

	BestDay      EconomicsMonthExtreme `json:"best_day"`
	MinEffectDay EconomicsMonthExtreme `json:"min_effect_day"`
}

// EconomicsDataQuality reports the ESS (УЗЕ) anomaly filter outcome:
// anomalous hours (physically impossible charge/discharge readings) are
// excluded from the fact/optimum/reserve; the rest of each day stays.
type EconomicsDataQuality struct {
	DataOk                     bool     `json:"data_ok"`
	TotalDays                  int      `json:"total_days"`
	AnomalousHours             int      `json:"anomalous_hours"`
	AnomalousDays              int      `json:"anomalous_days"`
	AnomalousDates             []string `json:"anomalous_dates"`
	// Anomalies is the per-hour decode (reasons + peak/charge signals).
	Anomalies []EconomicsAnomalyHour `json:"anomalies,omitempty"`
	// ReasonCounts rolls up anomaly reason codes across Anomalies.
	ReasonCounts map[string]int `json:"reason_counts,omitempty"`
	MaxChargeKwhPerInterval    float64 `json:"max_charge_kwh_per_interval"`
	MaxDischargeKwhPerInterval float64 `json:"max_discharge_kwh_per_interval"`
	PowerLimitKwhPerInterval   float64 `json:"power_limit_kwh_per_interval"`
	MaxIntervalPowerKw         float64 `json:"max_interval_power_kw"`
}

// EconomicsAnomalyHour is one excluded УЗЕ hour with classified reasons.
type EconomicsAnomalyHour struct {
	At            string   `json:"at"`
	Date          string   `json:"date"`
	Hour          int      `json:"hour"`
	Reasons       []string `json:"reasons"`
	PeakKw        float64  `json:"peak_kw"`
	ChargedKwh    float64  `json:"charged_kwh"`
	DischargedKwh float64  `json:"discharged_kwh"`
}

func dataQualityToJSON(q economics.DataQuality) EconomicsDataQuality {
	out := EconomicsDataQuality{
		DataOk:                     q.DataOK,
		TotalDays:                  q.TotalDays,
		AnomalousHours:             q.AnomalousHours,
		AnomalousDays:              q.AnomalousDays,
		AnomalousDates:             q.AnomalousDates,
		ReasonCounts:               q.ReasonCounts,
		MaxChargeKwhPerInterval:    q.MaxChargeKwhPerInterval,
		MaxDischargeKwhPerInterval: q.MaxDischargeKwhPerInterval,
		PowerLimitKwhPerInterval:   q.PowerLimitKwhPerInterval,
		MaxIntervalPowerKw:         q.MaxIntervalPowerKw,
	}
	if len(q.Anomalies) > 0 {
		out.Anomalies = make([]EconomicsAnomalyHour, len(q.Anomalies))
		for i, a := range q.Anomalies {
			out.Anomalies[i] = EconomicsAnomalyHour{
				At:            a.At,
				Date:          a.Date,
				Hour:          a.Hour,
				Reasons:       append([]string(nil), a.Reasons...),
				PeakKw:        a.PeakKw,
				ChargedKwh:    a.ChargedKwh,
				DischargedKwh: a.DischargedKwh,
			}
		}
	}
	return out
}

// EconomicsMonthlyDay is one day of the month breakdown (daily totals
// the trend chart / detail table render, plus per-day RDN + cycles).
type EconomicsMonthlyDay struct {
	Date             string  `json:"date"`
	IsFinal          bool    `json:"is_final"`
	RdnAvgUahPerKwh  float64 `json:"rdn_avg_uah_per_kwh"`
	EquivalentCycles float64 `json:"equivalent_cycles"`

	BaselineCostUah float64 `json:"baseline_cost_uah"`
	ActualCostUah   float64 `json:"actual_cost_uah"`
	EffectUah       float64 `json:"effect_uah"`
	EssNetUah       float64 `json:"ess_net_uah"`
	EbitdaUah       float64 `json:"ebitda_uah"`
	ImportCostUah   float64 `json:"import_cost_uah"`

	EssFactUah          float64 `json:"ess_fact_uah"`
	EssOptimumUah       float64 `json:"ess_optimum_uah"`
	EssReserveUah       float64 `json:"ess_reserve_uah"`
	EssReserveTimingUah float64 `json:"ess_reserve_timing_uah"`
	EssReserveSocUah    float64 `json:"ess_reserve_soc_uah"`
	EssReservePvUah     float64 `json:"ess_reserve_pv_uah"`
	EssPvMissedKwh      float64 `json:"ess_pv_missed_kwh"`

	LoadKwh          float64 `json:"load_kwh"`
	PvKwh            float64 `json:"pv_kwh"`
	GridImportKwh    float64 `json:"grid_import_kwh"`
	GridExportKwh    float64 `json:"grid_export_kwh"`
	EssChargedKwh    float64 `json:"ess_charged_kwh"`
	EssDischargedKwh float64 `json:"ess_discharged_kwh"`
	PvToLoadKwh      float64 `json:"pv_to_load_kwh"`
	PvToEssKwh       float64 `json:"pv_to_ess_kwh"`
	PvToGridKwh      float64 `json:"pv_to_grid_kwh"`
	GridToLoadKwh    float64 `json:"grid_to_load_kwh"`
	GridToEssKwh     float64 `json:"grid_to_ess_kwh"`
	EssToLoadKwh     float64 `json:"ess_to_load_kwh"`
	EssToGridKwh     float64 `json:"ess_to_grid_kwh"`

	HoursWithData     int `json:"hours_with_data"`
	HoursMissingPrice int `json:"hours_missing_price"`

	// QualityFlags carries the day's reconciliation diagnostics (e.g.
	// "import_lag:512", "load_mismatch:0.59") so the daily-detail table
	// can mark days whose flow split is approximate.
	QualityFlags []string `json:"quality_flags,omitempty"`
}

// EconomicsMonthlyMarginHour is one heatmap cell: the margin an hour of
// discharge earned per kWh, plus the arithmetic behind it so the grid can
// explain itself on hover.
type EconomicsMonthlyMarginHour struct {
	MarginUahPerKwh float64 `json:"margin_uah_per_kwh"`
	DischargedKwh   float64 `json:"discharged_kwh"`
	RevenueUah      float64 `json:"revenue_uah"`
	CostUah         float64 `json:"cost_uah"`
	WearUah         float64 `json:"wear_uah"`
}

// EconomicsMonthlyDayMargin is one heatmap row: 24 hourly ESS cells.
// null for hours the pack sat out.
type EconomicsMonthlyDayMargin struct {
	Date  string                        `json:"date"`
	Hours []*EconomicsMonthlyMarginHour `json:"hours"`
}

// EconomicsUzeCycle is one significant УЗЕ day (reserve ≥ 1000 ₴) with the
// full hourly optimal-vs-fact schedule the cycle chart renders (§1.3).
type EconomicsUzeCycle struct {
	StartDate       string                 `json:"start_date"`
	EndDate         string                 `json:"end_date"`
	Label           string                 `json:"label"`
	ActualEffectUah float64                `json:"actual_effect_uah"`
	OptEffectUah    float64                `json:"opt_effect_uah"`
	ReserveUah      float64                `json:"reserve_uah"`
	CapturePct      float64                `json:"capture_pct"`
	Chart           EconomicsUzeCycleChart `json:"chart"`
}

// EconomicsUzeCycleChart is the per-hour data behind one cycle's chart.
type EconomicsUzeCycleChart struct {
	Labels      []string                 `json:"labels"`
	CapacityKwh float64                  `json:"capacity_kwh"`
	PowerKw     float64                  `json:"power_kw"`
	Optimal     EconomicsUzeCycleOptimal `json:"optimal"`
	Fact        EconomicsUzeCycleFact    `json:"fact"`
	Summary     EconomicsUzeCycleSummary `json:"summary"`
}

// EconomicsUzeCycleOptimal is the optimal dispatch per hour.
type EconomicsUzeCycleOptimal struct {
	ToLoadKwh   []float64  `json:"to_load_kwh"`
	ToGridKwh   []float64  `json:"to_grid_kwh"`
	ChgPvKwh    []float64  `json:"chg_pv_kwh"`
	ChgGridKwh  []float64  `json:"chg_grid_kwh"`
	SocPct      []*float64 `json:"soc_pct"`
	SocStart    float64    `json:"soc_start"`
	ExportUah   []float64  `json:"export_uah"`
	LoadUah     []float64  `json:"load_uah"`
	GridCostUah []float64  `json:"grid_cost_uah"`
}

// EconomicsUzeCycleFact is the realised УЗЕ behaviour per hour.
type EconomicsUzeCycleFact struct {
	EssKw    []float64  `json:"ess_kw"`
	SocPct   []*float64 `json:"soc_pct"`
	SocStart *float64   `json:"soc_start"`
	Rdn      []float64  `json:"rdn"`
}

// EconomicsUzeCycleSummary aggregates optimal-vs-fact totals for a cycle.
type EconomicsUzeCycleSummary struct {
	Optimal EconomicsUzeCycleSummaryOptimal `json:"optimal"`
	Fact    EconomicsUzeCycleSummaryFact    `json:"fact"`
}

// EconomicsUzeCycleSummaryOptimal is the optimal-cycle waterfall.
type EconomicsUzeCycleSummaryOptimal struct {
	Effect        float64 `json:"effect"`
	ExportVal     float64 `json:"export_val"`
	LoadVal       float64 `json:"load_val"`
	ChargePvCost  float64 `json:"charge_pv_cost"`
	GridCost      float64 `json:"grid_cost"`
	Degradation   float64 `json:"degradation"`
	ChargePvKwh   float64 `json:"charge_pv_kwh"`
	ChargeGridKwh float64 `json:"charge_grid_kwh"`
	DischargeKwh  float64 `json:"discharge_kwh"`
}

// EconomicsUzeCycleSummaryFact is the realised cycle effect.
type EconomicsUzeCycleSummaryFact struct {
	Effect float64 `json:"effect"`
}

func uzeCycleToJSON(c economics.UzeCycle) EconomicsUzeCycle {
	ch := c.Chart
	return EconomicsUzeCycle{
		StartDate:       c.StartDate,
		EndDate:         c.EndDate,
		Label:           c.Label,
		ActualEffectUah: c.ActualEffectUah,
		OptEffectUah:    c.OptEffectUah,
		ReserveUah:      c.ReserveUah,
		CapturePct:      c.CapturePct,
		Chart: EconomicsUzeCycleChart{
			Labels:      ch.Labels,
			CapacityKwh: ch.CapacityKwh,
			PowerKw:     ch.PowerKw,
			Optimal: EconomicsUzeCycleOptimal{
				ToLoadKwh:   ch.Optimal.ToLoadKwh,
				ToGridKwh:   ch.Optimal.ToGridKwh,
				ChgPvKwh:    ch.Optimal.ChgPvKwh,
				ChgGridKwh:  ch.Optimal.ChgGridKwh,
				SocPct:      ch.Optimal.SocPct,
				SocStart:    ch.Optimal.SocStart,
				ExportUah:   ch.Optimal.ExportUah,
				LoadUah:     ch.Optimal.LoadUah,
				GridCostUah: ch.Optimal.GridCostUah,
			},
			Fact: EconomicsUzeCycleFact{
				EssKw:    ch.Fact.EssKw,
				SocPct:   ch.Fact.SocPct,
				SocStart: ch.Fact.SocStart,
				Rdn:      ch.Fact.Rdn,
			},
			Summary: EconomicsUzeCycleSummary{
				Optimal: EconomicsUzeCycleSummaryOptimal{
					Effect:        ch.Summary.Optimal.EffectUah,
					ExportVal:     ch.Summary.Optimal.ExportVal,
					LoadVal:       ch.Summary.Optimal.LoadVal,
					ChargePvCost:  ch.Summary.Optimal.ChargePvCost,
					GridCost:      ch.Summary.Optimal.GridCost,
					Degradation:   ch.Summary.Optimal.Degradation,
					ChargePvKwh:   ch.Summary.Optimal.ChargePvKwh,
					ChargeGridKwh: ch.Summary.Optimal.ChargeGridKwh,
					DischargeKwh:  ch.Summary.Optimal.DischargeKwh,
				},
				Fact: EconomicsUzeCycleSummaryFact{Effect: ch.Summary.Fact.EffectUah},
			},
		},
	}
}

// EconomicsMonthlyResponse is the body of GET /api/v1/economics/monthly.
type EconomicsMonthlyResponse struct {
	OrganizationID string                      `json:"organization_id"`
	Month          string                      `json:"month"`
	Tz             string                      `json:"tz"`
	DaysInMonth    int                         `json:"days_in_month"`
	Totals         EconomicsMonthlyTotals      `json:"totals"`
	Days           []EconomicsMonthlyDay       `json:"days"`
	HourlyMargin   []EconomicsMonthlyDayMargin `json:"hourly_margin"`
	UzeCycles      []EconomicsUzeCycle         `json:"uze_cycles"`
	// ComputedAt is when the month's newest day was last computed
	// (RFC3339, UTC); empty without data.
	ComputedAt string `json:"computed_at,omitempty"`
	fxWindowJSON
}

// marginCellsToJSON maps a row of heatmap cells. Both grids — the
// monthly day x hour and the annual month x hour-of-day — carry the same
// cell, so they serialize through here and the frontend explains them
// with one tooltip.
func marginCellsToJSON(cells []*economics.MarginHour) []*EconomicsMonthlyMarginHour {
	out := make([]*EconomicsMonthlyMarginHour, len(cells))
	for i, c := range cells {
		if c == nil {
			continue
		}
		out[i] = &EconomicsMonthlyMarginHour{
			MarginUahPerKwh: c.Margin,
			DischargedKwh:   c.DischargedKwh,
			RevenueUah:      c.RevenueUah,
			CostUah:         c.CostUah,
			WearUah:         c.WearUah,
		}
	}
	return out
}

func marginRowToJSON(m economics.DayMargin) EconomicsMonthlyDayMargin {
	return EconomicsMonthlyDayMargin{Date: m.Date, Hours: marginCellsToJSON(m.Hours)}
}

// importCostUah reconstructs the money actually spent on grid import.
// The average import price is itself a kWh-weighted ratio over exactly
// this import volume, so multiplying back recovers the original sum of
// price x kWh — an exact figure, not an estimate (same reconstruction
// the monthly rollup relies on when re-weighting daily prices).
func importCostUah(avgImportPrice, gridImportKwh float64) float64 {
	return avgImportPrice * gridImportKwh
}

func monthlyTotalsToJSON(t economics.MonthlyTotals) EconomicsMonthlyTotals {
	return EconomicsMonthlyTotals{
		BaselineCostUah:             t.BaselineCost,
		ActualCostUah:               t.ActualCost,
		EffectUah:                   t.Effect,
		EssNetUah:                   t.EssNet,
		LoadKwh:                     t.Load,
		PvKwh:                       t.PV,
		GridImportKwh:               t.GridImport,
		GridExportKwh:               t.GridExport,
		EssChargedKwh:               t.EssCharged,
		EssDischargedKwh:            t.EssDischarged,
		PvToLoadKwh:                 t.PVToLoad,
		PvToEssKwh:                  t.PVToEss,
		PvToGridKwh:                 t.PVToGrid,
		GridToLoadKwh:               t.GridToLoad,
		GridToEssKwh:                t.GridToEss,
		EssToLoadKwh:                t.EssToLoad,
		EssToGridKwh:                t.EssToGrid,
		AvgImportPriceUahPerKwh:     t.AvgImportPrice,
		AvgExportPriceUahPerKwh:     t.AvgExportPrice,
		RdnAvgUahPerKwh:             t.RdnAvgUahPerKwh,
		RdnMaxUahPerKwh:             t.RdnMaxUahPerKwh,
		RevenuePvExportUah:          t.RevenuePvExport,
		RevenuePvSelfUah:            t.RevenuePvSelf,
		RevenueEssExportUah:         t.RevenueEssExport,
		RevenueEssSelfUah:           t.RevenueEssSelf,
		RevenueTotalUah:             t.RevenueTotal,
		ExpenseGridChargeUah:        t.ExpenseGridCharge,
		ExpenseTotalUah:             t.ExpenseTotal,
		EbitdaUah:                   t.Ebitda,
		PvExportPotentialUah:        t.PvExportPotential,
		PvEssExportPotentialUah:     t.PvEssExportPotential,
		ImportCostUah:               importCostUah(t.AvgImportPrice, t.GridImport),
		EssWithdrawnCostUah:         t.EssWithdrawnCost,
		EssRealizedProfitUah:        t.EssRealizedProfit,
		EssDegradationCostUah:       t.EssDegradationCost,
		EssAvgCostBasisUahPerKwhEod: t.EssAvgCostBasisEod,
		EssResidualKwhEod:           t.EssResidualKwhEod,
		EssCostBasisUahEod:          t.EssCostBasisUahEod,
		EquivalentCycles:            t.EquivalentCycles,
		DaysWithData:                t.DaysWithData,
		HoursWithData:               t.HoursWithData,
		HoursMissingPrice:           t.HoursMissingPrice,
		FlaggedDays:                 t.FlaggedDays,
		EssFactUah:                  t.EssFact,
		EssOptimumUah:               t.EssOptimum,
		EssReserveUah:               t.EssReserve,
		EssCapturedShare:            t.EssCapturedShare,
		EssReserveTimingUah:         t.EssReserveTiming,
		EssReserveSocUah:            t.EssReserveSoc,
		EssReservePvUah:             t.EssReservePv,
		EssPvMissedKwh:              t.EssPvMissedKwh,
		EssDataQuality:              dataQualityToJSON(t.EssDataQuality),
		BestDay:                     EconomicsMonthExtreme{Date: t.BestDay.Date, EffectUah: t.BestDay.EffectUah},
		MinEffectDay:                EconomicsMonthExtreme{Date: t.MinEffectDay.Date, EffectUah: t.MinEffectDay.EffectUah},
	}
}

func monthlyDayToJSON(d economics.MonthDay) EconomicsMonthlyDay {
	t := d.Totals
	return EconomicsMonthlyDay{
		Date:                d.Date,
		IsFinal:             d.IsFinal,
		RdnAvgUahPerKwh:     d.RdnAvgUahPerKwh,
		EquivalentCycles:    d.EquivalentCycles,
		BaselineCostUah:     t.BaselineCost,
		ActualCostUah:       t.ActualCost,
		EffectUah:           t.Effect,
		EssNetUah:           t.EssNet,
		EbitdaUah:           t.Ebitda,
		ImportCostUah:       importCostUah(t.AvgImportPrice, t.GridImport),
		EssFactUah:          d.EssFact,
		EssOptimumUah:       d.EssOptimum,
		EssReserveUah:       d.EssReserve,
		EssReserveTimingUah: d.EssReserveTiming,
		EssReserveSocUah:    d.EssReserveSoc,
		EssReservePvUah:     d.EssReservePv,
		EssPvMissedKwh:      d.EssPvMissedKwh,
		LoadKwh:             t.Load,
		PvKwh:               t.PV,
		GridImportKwh:       t.GridImport,
		GridExportKwh:       t.GridExport,
		EssChargedKwh:       t.EssCharged,
		EssDischargedKwh:    t.EssDischarged,
		PvToLoadKwh:         t.PVToLoad,
		PvToEssKwh:          t.PVToEss,
		PvToGridKwh:         t.PVToGrid,
		GridToLoadKwh:       t.GridToLoad,
		GridToEssKwh:        t.GridToEss,
		EssToLoadKwh:        t.EssToLoad,
		EssToGridKwh:        t.EssToGrid,
		HoursWithData:       t.HoursWithData,
		HoursMissingPrice:   t.HoursMissingPrice,
		QualityFlags:        t.QualityFlags,
	}
}

// economicsMonthly serves a month rollup of the per-day economics,
// read-through (final days from cache, the open tail recomputed on read).
//
//	GET /api/v1/economics/monthly?organization_id=&month=YYYY-MM&tz=&currency=
func (h *Handlers) economicsMonthly(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.economics == nil {
		http.Error(w, "economics service not configured", http.StatusServiceUnavailable)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	monthStr := strings.TrimSpace(r.URL.Query().Get("month"))
	if monthStr == "" {
		http.Error(w, "month is required (YYYY-MM)", http.StatusBadRequest)
		return
	}
	tzStr := strings.TrimSpace(r.URL.Query().Get("tz"))
	loc, err := loadLocation(tzStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	monthStart, err := time.ParseInLocation("2006-01", monthStr, loc)
	if err != nil {
		http.Error(w, "month must be YYYY-MM", http.StatusBadRequest)
		return
	}
	currency, err := parseCurrency(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	view, err := h.fxViewFor(r.Context(), currency, loc)
	if err != nil {
		h.writeFxViewError(w, "api_economics_monthly", err)
		return
	}

	svc := h.economicsIn(view)
	month, err := svc.GetMonth(r.Context(), orgID, monthStr, loc.String())
	if err != nil {
		h.log.Error("api_economics_monthly", "organization_id", orgID, "month", monthStr, "tz", loc.String(), "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	monthEnd := monthStart.AddDate(0, 1, -1)
	weights, err := svc.EbitdaWeights(r.Context(), orgID, monthStart, monthEnd)
	if err != nil {
		h.log.Warn("api_economics_monthly_fx", "organization_id", orgID, "month", monthStr, "err", err)
	}

	resp := EconomicsMonthlyResponse{
		OrganizationID: orgID,
		Month:          monthStr,
		Tz:             loc.String(),
		DaysInMonth:    month.DaysInMonth,
		Totals:         monthlyTotalsToJSON(month.Totals),
		Days:           make([]EconomicsMonthlyDay, 0, len(month.Days)),
		HourlyMargin:   make([]EconomicsMonthlyDayMargin, 0, len(month.HourlyMargin)),
		UzeCycles:      make([]EconomicsUzeCycle, 0, len(month.Cycles)),
		ComputedAt:     computedAtJSON(month.ComputedAt),
		fxWindowJSON:   fxWindowFor(view, monthStart, monthEnd, weights),
	}
	for _, d := range month.Days {
		resp.Days = append(resp.Days, monthlyDayToJSON(d))
	}
	for _, m := range month.HourlyMargin {
		resp.HourlyMargin = append(resp.HourlyMargin, marginRowToJSON(m))
	}
	for _, c := range month.Cycles {
		resp.UzeCycles = append(resp.UzeCycles, uzeCycleToJSON(c))
	}
	writeJSON(w, http.StatusOK, resp)
}

// EconomicsAnnualMonthRollup is one month's contribution to the annual
// view: the YYYY-MM label plus that month's totals (same shape the
// monthly dashboard renders, so the year trend/table reuse the fields).
type EconomicsAnnualMonthRollup struct {
	Month  string                 `json:"month"`
	Totals EconomicsMonthlyTotals `json:"totals"`
}

// EconomicsAnnualQuarter is one quarter card: project effect, EBITDA + PV.
type EconomicsAnnualQuarter struct {
	Year      int     `json:"year"`
	Quarter   int     `json:"quarter"`
	EffectUah float64 `json:"effect_uah"`
	EbitdaUah float64 `json:"ebitda_uah"`
	PvKwh     float64 `json:"pv_kwh"`
}

// EconomicsAnnualMonthMargin is one heatmap row: 24 hour-of-day ESS
// cells, each summing that hour across the month; null when the pack
// sat that hour out all month.
type EconomicsAnnualMonthMargin struct {
	Month string                        `json:"month"`
	Hours []*EconomicsMonthlyMarginHour `json:"hours"`
}

// EconomicsAnnualResponse is the body of GET /api/v1/economics/annual.
type EconomicsAnnualResponse struct {
	OrganizationID string `json:"organization_id"`
	Period         string `json:"period"`
	From           string `json:"from"`
	To             string `json:"to"`
	Tz             string `json:"tz"`
	MonthsWithData int    `json:"months_with_data"`
	// PriorEbitdaUah is the cumulative EBITDA of stored days before the
	// window start — the ROI opening balance for a single-year view.
	PriorEbitdaUah float64 `json:"prior_ebitda_uah"`
	// PriorMonthsWithData is the number of distinct months with data before
	// the window, so the ROI payback can annualise all-time EBITDA over the
	// full operating span.
	PriorMonthsWithData int                          `json:"prior_months_with_data"`
	Totals              EconomicsMonthlyTotals       `json:"totals"`
	Months              []EconomicsAnnualMonthRollup `json:"months"`
	Quarters            []EconomicsAnnualQuarter     `json:"quarters"`
	MonthlyMargin       []EconomicsAnnualMonthMargin `json:"monthly_margin"`
	// ComputedAt is when the window's newest day was last computed
	// (RFC3339, UTC); empty without data.
	ComputedAt string `json:"computed_at,omitempty"`
	fxWindowJSON
}

// monthsWindow is the civil-day span of a from..to month window (both
// YYYY-MM); ok is false when either bound does not parse.
func monthsWindow(from, to string, loc *time.Location) (time.Time, time.Time, bool) {
	first, err := time.ParseInLocation("2006-01", from, loc)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	last, err := time.ParseInLocation("2006-01", to, loc)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return first, last.AddDate(0, 1, -1), true
}

// economicsAnnual serves a calendar-year rollup of the per-month
// economics, read-through (the economics-recompute daemon keeps the
// underlying daily/hourly records warm; this path never recomputes).
//
//	GET /api/v1/economics/annual?organization_id=&period=YYYY&tz=&currency=
func (h *Handlers) economicsAnnual(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.economics == nil {
		http.Error(w, "economics service not configured", http.StatusServiceUnavailable)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	tzStr := strings.TrimSpace(r.URL.Query().Get("tz"))
	loc, err := loadLocation(tzStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// A sliding window is requested with from/to (both YYYY-MM). When
	// absent we fall back to the calendar-year period (YYYY).
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	periodStr := strings.TrimSpace(r.URL.Query().Get("period"))
	currency, err := parseCurrency(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var year economics.StoredYear
	if fromStr != "" || toStr != "" {
		if _, err := time.ParseInLocation("2006-01", fromStr, loc); err != nil {
			http.Error(w, "from must be YYYY-MM", http.StatusBadRequest)
			return
		}
		if _, err := time.ParseInLocation("2006-01", toStr, loc); err != nil {
			http.Error(w, "to must be YYYY-MM", http.StatusBadRequest)
			return
		}
	} else {
		if periodStr == "" {
			http.Error(w, "period is required (YYYY)", http.StatusBadRequest)
			return
		}
		if _, perr := time.ParseInLocation("2006", periodStr, loc); perr != nil {
			http.Error(w, "period must be YYYY", http.StatusBadRequest)
			return
		}
	}
	view, err := h.fxViewFor(r.Context(), currency, loc)
	if err != nil {
		h.writeFxViewError(w, "api_economics_annual", err)
		return
	}
	svc := h.economicsIn(view)
	if fromStr != "" || toStr != "" {
		year, err = svc.GetPeriod(r.Context(), orgID, fromStr, toStr, loc.String())
	} else {
		year, err = svc.GetYear(r.Context(), orgID, periodStr, loc.String())
	}
	if err != nil {
		h.log.Error("api_economics_annual", "organization_id", orgID, "period", periodStr, "from", fromStr, "to", toStr, "tz", loc.String(), "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := EconomicsAnnualResponse{
		OrganizationID:      orgID,
		Period:              year.Period,
		From:                year.From,
		To:                  year.To,
		Tz:                  loc.String(),
		MonthsWithData:      year.MonthsWithData,
		PriorEbitdaUah:      year.PriorEbitda,
		PriorMonthsWithData: year.PriorMonthsWithData,
		Totals:              monthlyTotalsToJSON(year.Totals),
		Months:              make([]EconomicsAnnualMonthRollup, 0, len(year.Months)),
		Quarters:            make([]EconomicsAnnualQuarter, 0, len(year.Quarters)),
		MonthlyMargin:       make([]EconomicsAnnualMonthMargin, 0, len(year.MonthlyMargin)),
		ComputedAt:          computedAtJSON(year.ComputedAt),
		fxWindowJSON:        fxWindowJSON{Currency: view.code()},
	}
	if first, last, ok := monthsWindow(year.From, year.To, loc); ok {
		weights, werr := svc.EbitdaWeights(r.Context(), orgID, first, last)
		if werr != nil {
			h.log.Warn("api_economics_annual_fx", "organization_id", orgID, "from", year.From, "to", year.To, "err", werr)
		}
		resp.fxWindowJSON = fxWindowFor(view, first, last, weights)
	}
	for _, m := range year.Months {
		resp.Months = append(resp.Months, EconomicsAnnualMonthRollup{
			Month:  m.Month,
			Totals: monthlyTotalsToJSON(m.Totals),
		})
	}
	for _, q := range year.Quarters {
		resp.Quarters = append(resp.Quarters, EconomicsAnnualQuarter{
			Year:      q.Year,
			Quarter:   q.Quarter,
			EffectUah: q.EffectUah,
			EbitdaUah: q.EbitdaUah,
			PvKwh:     q.PvKwh,
		})
	}
	for _, m := range year.MonthlyMargin {
		resp.MonthlyMargin = append(resp.MonthlyMargin, EconomicsAnnualMonthMargin{
			Month: m.Month,
			Hours: marginCellsToJSON(m.Hours),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// EconomicsPortfolioSite is one object's contribution to the portfolio
// (zведений) rollup: the headline project effect plus the two reserve
// levers (work-schedule + УЗЕ optimum) and the УЗЕ data-quality flags.
type EconomicsPortfolioSite struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	HasData            bool    `json:"has_data"`
	EffectUah          float64 `json:"effect_uah"`
	EbitdaUah          float64 `json:"ebitda_uah"`
	ScheduleReserveUah float64 `json:"schedule_reserve_uah"`
	BessReserveUah     float64 `json:"bess_reserve_uah"`
	ActionReserveUah   float64 `json:"action_reserve_uah"`
	BessDataOk         bool     `json:"bess_data_ok"`
	BessAnomalousHours int      `json:"bess_anomalous_hours"`
	BessAnomalousDays  int      `json:"bess_anomalous_days"`
	// BessAnomalousDates lists civil dates that contain ≥1 excluded УЗЕ
	// hour; the portfolio ⚠ drills into the first of them.
	BessAnomalousDates []string `json:"bess_anomalous_dates,omitempty"`
	// BessAnomalyReasons lists distinct reason codes seen on this site
	// (peak_spike / hourly_over_limit / after_gap) for the portfolio ⚠ tip.
	BessAnomalyReasons []string `json:"bess_anomaly_reasons,omitempty"`
	PvKwh              float64  `json:"pv_kwh"`
	LoadKwh            float64 `json:"load_kwh"`
	GridImportKwh      float64 `json:"grid_import_kwh"`
	GridExportKwh      float64 `json:"grid_export_kwh"`
	EssNetUah          float64 `json:"ess_net_uah"`
	// CapexUah is the invested capital standing at the end of the
	// period (0 when the tariffs carry none); with MonthsWithData it
	// gives the object's ROCE the way the economics page's card does.
	// On the total row it is the sum over the objects that have both.
	CapexUah       float64 `json:"capex_uah"`
	MonthsWithData int     `json:"months_with_data"`
}

// EconomicsPortfolioTrendMonth is one month of the portfolio energy trend
// (year scope): the YYYY-MM key plus the sum across all objects, with
// EBITDA as the money figure the portfolio view reports everywhere.
type EconomicsPortfolioTrendMonth struct {
	Month         string  `json:"month"`
	PvKwh         float64 `json:"pv_kwh"`
	LoadKwh       float64 `json:"load_kwh"`
	GridImportKwh float64 `json:"grid_import_kwh"`
	GridExportKwh float64 `json:"grid_export_kwh"`
	EbitdaUah     float64 `json:"ebitda_uah"`
}

// EconomicsPortfolioResponse is the body of GET /api/v1/economics/portfolio:
// the per-object rows plus a portfolio total, for a month or a year/window.
type EconomicsPortfolioResponse struct {
	Scope          string                         `json:"scope"` // "month" | "year"
	Label          string                         `json:"label"`
	Tz             string                         `json:"tz"`
	MonthsWithData int                            `json:"months_with_data"`
	Sites          []EconomicsPortfolioSite       `json:"sites"`
	Totals         EconomicsPortfolioSite         `json:"totals"`
	Trend          []EconomicsPortfolioTrendMonth `json:"trend"`
	fxWindowJSON
}

// scheduleReserveUah is the work-schedule (elevator) reserve: shifting
// flexible daytime load to consume PV that is currently exported, valued
// at the import–export price gap. Mirrors the frontend reserveSplit so the
// portfolio bars and the per-object AI panel agree.
func scheduleReserveUah(t economics.MonthlyTotals) float64 {
	gap := t.AvgImportPrice - t.AvgExportPrice
	if gap < 0 {
		gap = 0
	}
	shiftable := t.PVToGrid
	if t.GridToLoad < shiftable {
		shiftable = t.GridToLoad
	}
	return shiftable * gap
}

// reasonKeys returns sorted distinct reason codes from a ReasonCounts map.
func reasonKeys(counts map[string]int) []string {
	if len(counts) == 0 {
		return nil
	}
	out := make([]string, 0, len(counts))
	for k, n := range counts {
		if n > 0 {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// portfolioConcurrency caps how many objects the portfolio rolls up at
// once; each period rollup already spreads its months across all cores,
// so this mainly overlaps one object's DB reads with another's compute.
const portfolioConcurrency = 4

// portfolioSiteFromTotals builds one site row from a period's totals.
func portfolioSiteFromTotals(id, name string, t economics.MonthlyTotals, hasData bool) EconomicsPortfolioSite {
	sched := scheduleReserveUah(t)
	bess := t.EssReserve
	if bess < 0 {
		bess = 0
	}
	return EconomicsPortfolioSite{
		ID:                 id,
		Name:               name,
		HasData:            hasData,
		EffectUah:          t.Effect,
		EbitdaUah:          t.Ebitda,
		ScheduleReserveUah: sched,
		BessReserveUah:     bess,
		ActionReserveUah:   sched + bess,
		BessDataOk:         t.EssDataQuality.DataOK,
		BessAnomalousHours: t.EssDataQuality.AnomalousHours,
		BessAnomalousDays:  t.EssDataQuality.AnomalousDays,
		BessAnomalousDates: append([]string(nil), t.EssDataQuality.AnomalousDates...),
		BessAnomalyReasons: reasonKeys(t.EssDataQuality.ReasonCounts),
		PvKwh:              t.PV,
		LoadKwh:            t.Load,
		GridImportKwh:      t.GridImport,
		GridExportKwh:      t.GridExport,
		EssNetUah:          t.EssNet,
	}
}

// epochEffectiveFrom is the catch-all tariff version the backend seeds
// from the tariff form.
const epochEffectiveFrom = "1970-01-01"

// resolveCapexUah is the capital standing at the end of lastMonth
// (YYYY-MM), resolved like the economics page's ROCE card
// (web/src/economics/payback.ts capexResolver): the latest dated
// version with CAPEX that takes effect by the end of that month, the
// first such version for earlier months, and the tariff form's value
// when no version carries CAPEX. The 1970 catch-all is a copy of the
// form, not a dated stage. versions are ascending by effective date.
func resolveCapexUah(versions []TariffScheduleVersion, formCapexUah float64, lastMonth string) float64 {
	return resolveCapex(versions, formCapexUah, lastMonth, nil, nil)
}

// resolveCapex is resolveCapexUah in a display currency. Each version
// carries the cumulative investment, so each step's increment is
// converted at the rate of the version's effective date (the day it was
// invested), and the form's flat value at the rate of formDay. A nil
// scaleOn keeps UAH.
func resolveCapex(versions []TariffScheduleVersion, formCapexUah float64, lastMonth string, scaleOn func(day string) float64, formDay func() string) float64 {
	scale := func(day string) float64 {
		if scaleOn == nil {
			return 1
		}
		return scaleOn(day)
	}
	var funded []TariffScheduleVersion
	for _, v := range versions {
		if v.EffectiveFrom != epochEffectiveFrom && len(v.EffectiveFrom) == len("2006-01-02") && v.Tariffs.CapexUah > 0 {
			funded = append(funded, v)
		}
	}
	if len(funded) == 0 {
		if formCapexUah <= 0 {
			return 0
		}
		if scaleOn == nil || formDay == nil {
			return formCapexUah
		}
		return formCapexUah * scale(formDay())
	}
	monthEnd := lastMonth + "-31"
	var standing, value float64
	for i, v := range funded {
		if i > 0 && v.EffectiveFrom > monthEnd {
			break
		}
		value += (v.Tariffs.CapexUah - standing) * scale(v.EffectiveFrom)
		standing = v.Tariffs.CapexUah
	}
	return value
}

// portfolioCapex reads an object's tariffs for resolveCapex. A failed
// read leaves the object without ROCE rather than failing the whole
// rollup.
func (h *Handlers) portfolioCapex(ctx context.Context, orgID, lastMonth string, view fxView) float64 {
	versions, err := h.tariffVersions(ctx, orgID)
	if err != nil {
		h.log.Warn("api_economics_portfolio_capex", "organization_id", orgID, "err", err)
		versions = nil
	}
	var form float64
	if t, ok, err := h.orgTariffs(ctx, orgID); err == nil && ok {
		form = t.CapexUah
	}
	if view.uah() {
		return resolveCapex(versions, form, lastMonth, nil, nil)
	}
	scaleOn := func(day string) float64 {
		d, err := time.ParseInLocation("2006-01-02", day, view.loc)
		if err != nil {
			return view.scale(time.Now())
		}
		return view.scale(d)
	}
	formDay := func() string {
		first, ok, err := h.economics.FirstDay(ctx, orgID)
		if err != nil || !ok {
			return time.Now().In(view.loc).Format("2006-01-02")
		}
		return first.Format("2006-01-02")
	}
	return resolveCapex(versions, form, lastMonth, scaleOn, formDay)
}

// economicsPortfolio serves a portfolio (all-objects) rollup for a month
// or a year/window: per-object project effect + work-schedule and УЗЕ
// reserves (project_net) + УЗЕ data-quality flags, plus a portfolio total.
//
//	GET /api/v1/economics/portfolio?month=YYYY-MM&tz=&currency=
//	GET /api/v1/economics/portfolio?period=YYYY&tz=&currency=
//	GET /api/v1/economics/portfolio?from=YYYY-MM&to=YYYY-MM&tz=&currency=
func (h *Handlers) economicsPortfolio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.economics == nil {
		http.Error(w, "economics service not configured", http.StatusServiceUnavailable)
		return
	}
	tzStr := strings.TrimSpace(r.URL.Query().Get("tz"))
	loc, err := loadLocation(tzStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	monthStr := strings.TrimSpace(r.URL.Query().Get("month"))
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	periodStr := strings.TrimSpace(r.URL.Query().Get("period"))

	scope := "year"
	label := periodStr
	if monthStr != "" {
		if _, err := time.ParseInLocation("2006-01", monthStr, loc); err != nil {
			http.Error(w, "month must be YYYY-MM", http.StatusBadRequest)
			return
		}
		scope = "month"
		label = monthStr
	} else if fromStr != "" || toStr != "" {
		if _, err := time.ParseInLocation("2006-01", fromStr, loc); err != nil {
			http.Error(w, "from must be YYYY-MM", http.StatusBadRequest)
			return
		}
		if _, err := time.ParseInLocation("2006-01", toStr, loc); err != nil {
			http.Error(w, "to must be YYYY-MM", http.StatusBadRequest)
			return
		}
		label = fromStr + ".." + toStr
	} else {
		if periodStr == "" {
			http.Error(w, "one of month / period / from+to is required", http.StatusBadRequest)
			return
		}
		if _, perr := time.ParseInLocation("2006", periodStr, loc); perr != nil {
			http.Error(w, "period must be YYYY", http.StatusBadRequest)
			return
		}
	}

	currency, err := parseCurrency(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	view, err := h.fxViewFor(r.Context(), currency, loc)
	if err != nil {
		h.writeFxViewError(w, "api_economics_portfolio", err)
		return
	}

	q := portfolioQuery{Scope: scope, Label: label, Month: monthStr, Period: periodStr, From: fromStr, To: toStr}
	visible := func(org string) bool { return h.visibleTo(r, org, auth.PermEconomicsRead) }
	resp, weights := h.portfolioRollupIn(r.Context(), q, loc, visible, view)
	resp.fxWindowJSON = fxWindowJSON{Currency: view.code()}
	if first, last, ok := q.window(loc); ok {
		resp.fxWindowJSON = fxWindowFor(view, first, last, weights)
	}
	writeJSON(w, http.StatusOK, resp)
}

// window is the civil-day span of the portfolio period.
func (q portfolioQuery) window(loc *time.Location) (time.Time, time.Time, bool) {
	switch {
	case q.Scope == "month":
		return monthsWindow(q.Month, q.Month, loc)
	case q.From != "" || q.To != "":
		return monthsWindow(q.From, q.To, loc)
	default:
		return monthsWindow(q.Period+"-01", q.Period+"-12", loc)
	}
}

// portfolioQuery is one portfolio period: a month (Scope "month"), a
// calendar year (Period) or a window of months (From..To, both YYYY-MM).
type portfolioQuery struct {
	Scope, Label            string
	Month, Period, From, To string
}

// portfolioRollup builds the portfolio over the organizations visible
// accepts. The economics route and the assistant's portfolio tool both
// answer from it.
func (h *Handlers) portfolioRollup(ctx context.Context, q portfolioQuery, loc *time.Location, visible func(org string) bool) EconomicsPortfolioResponse {
	resp, _ := h.portfolioRollupIn(ctx, q, loc, visible, fxView{currency: "UAH", loc: loc})
	return resp
}

// portfolioRollupIn is portfolioRollup in the view's currency: each
// object's days at their own rates, its CAPEX at the rates of its
// versions' dates. It also returns the weights of the objects' EBITDA,
// which the portfolio's rate is read from.
func (h *Handlers) portfolioRollupIn(ctx context.Context, q portfolioQuery, loc *time.Location, visible func(org string) bool, view fxView) (EconomicsPortfolioResponse, economics.EbitdaWeights) {
	svc := h.economicsIn(view)
	scope, monthStr, periodStr, fromStr, toStr := q.Scope, q.Month, q.Period, q.From, q.To
	resp := EconomicsPortfolioResponse{
		Scope: scope,
		Label: q.Label,
		Tz:    loc.String(),
		Sites: make([]EconomicsPortfolioSite, 0, len(h.organizations)),
	}
	// The demo organization carries synthetic data and is not a real
	// site, so it must never appear in the portfolio rollup. The rollup
	// covers only the objects the caller may read economics of, an
	// economic group in place of its members.
	orgs := make([]OrganizationInfo, 0, len(h.organizations))
	for _, org := range h.economicObjects(visible) {
		if org.ID != demoOrgID {
			orgs = append(orgs, org)
		}
	}

	// Objects are rolled up concurrently (each is independent and mostly
	// CPU-bound in the УЗЕ optimizer) and folded below in config order, so
	// the response does not depend on which finished first.
	type siteRollup struct {
		totals  economics.MonthlyTotals
		hasData bool
		year    economics.StoredYear
		capex   float64
		weights economics.EbitdaWeights
	}
	// ROCE divides by the capital standing at the end of the period.
	lastMonth := periodStr + "-12"
	switch {
	case scope == "month":
		lastMonth = monthStr
	case fromStr != "" || toStr != "":
		lastMonth = toStr
	}
	rollups := make([]siteRollup, len(orgs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, portfolioConcurrency)
	for i, org := range orgs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			capex := h.portfolioCapex(ctx, org.ID, lastMonth, view)
			if scope == "month" {
				t, err := svc.GetMonthTotals(ctx, org.ID, monthStr, loc.String())
				if err != nil {
					h.log.Warn("api_economics_portfolio_org", "organization_id", org.ID, "month", monthStr, "err", err)
					return
				}
				weights := h.portfolioWeights(ctx, svc, org.ID, monthStr, monthStr, loc)
				rollups[i] = siteRollup{totals: t, hasData: t.DaysWithData > 0, capex: capex, weights: weights}
				return
			}
			var y economics.StoredYear
			var err error
			if fromStr != "" || toStr != "" {
				y, err = svc.GetPeriod(ctx, org.ID, fromStr, toStr, loc.String())
			} else {
				y, err = svc.GetYear(ctx, org.ID, periodStr, loc.String())
			}
			if err != nil {
				h.log.Warn("api_economics_portfolio_org", "organization_id", org.ID, "period", periodStr, "err", err)
				return
			}
			weights := h.portfolioWeights(ctx, svc, org.ID, y.From, y.To, loc)
			rollups[i] = siteRollup{totals: y.Totals, hasData: y.MonthsWithData > 0, year: y, capex: capex, weights: weights}
		}()
	}
	wg.Wait()

	var totals economics.MonthlyTotals
	var weights economics.EbitdaWeights
	var maxMonthsWithData int
	var totalCapex float64
	trendAcc := make(map[string]*EconomicsPortfolioTrendMonth)
	var trendOrder []string

	for i, org := range orgs {
		t, hasData, y := rollups[i].totals, rollups[i].hasData, rollups[i].year
		if y.MonthsWithData > maxMonthsWithData {
			maxMonthsWithData = y.MonthsWithData
		}
		for _, mr := range y.Months {
			row := trendAcc[mr.Month]
			if row == nil {
				row = &EconomicsPortfolioTrendMonth{Month: mr.Month}
				trendAcc[mr.Month] = row
				trendOrder = append(trendOrder, mr.Month)
			}
			row.PvKwh += mr.Totals.PV
			row.LoadKwh += mr.Totals.Load
			row.GridImportKwh += mr.Totals.GridImport
			row.GridExportKwh += mr.Totals.GridExport
			row.EbitdaUah += mr.Totals.Ebitda
		}

		site := portfolioSiteFromTotals(org.ID, org.Name, t, hasData)
		site.CapexUah = rollups[i].capex
		switch {
		case !hasData:
		case scope == "month":
			site.MonthsWithData = 1
		default:
			site.MonthsWithData = y.MonthsWithData
		}
		if hasData && site.CapexUah > 0 {
			totalCapex += site.CapexUah
		}
		resp.Sites = append(resp.Sites, site)
		if hasData {
			weights.Add(rollups[i].weights)
			totals.Effect += t.Effect
			totals.Ebitda += t.Ebitda
			totals.EssNet += t.EssNet
			totals.PV += t.PV
			totals.Load += t.Load
			totals.GridImport += t.GridImport
			totals.GridExport += t.GridExport
			if t.EssReserve > 0 {
				totals.EssReserve += t.EssReserve
			}
			totals.EssDataQuality.AnomalousHours += t.EssDataQuality.AnomalousHours
			totals.EssDataQuality.AnomalousDays += t.EssDataQuality.AnomalousDays
			if len(t.EssDataQuality.ReasonCounts) > 0 {
				if totals.EssDataQuality.ReasonCounts == nil {
					totals.EssDataQuality.ReasonCounts = make(map[string]int)
				}
				for k, v := range t.EssDataQuality.ReasonCounts {
					totals.EssDataQuality.ReasonCounts[k] += v
				}
			}
			totals.EssDataQuality.Anomalies = append(totals.EssDataQuality.Anomalies, t.EssDataQuality.Anomalies...)
		}
	}

	// Sort sites by action reserve desc (biggest opportunity first), keeping
	// empty objects at the bottom.
	sort.SliceStable(resp.Sites, func(i, j int) bool {
		if resp.Sites[i].HasData != resp.Sites[j].HasData {
			return resp.Sites[i].HasData
		}
		return resp.Sites[i].ActionReserveUah > resp.Sites[j].ActionReserveUah
	})

	// Portfolio total row: effect/energy summed above; reserves summed from
	// the per-site rows so the schedule reserve matches the visible bars.
	total := EconomicsPortfolioSite{
		ID:                 "__total__",
		Name:               "Портфель",
		HasData:            true,
		EffectUah:          totals.Effect,
		EbitdaUah:          totals.Ebitda,
		BessReserveUah:     totals.EssReserve,
		BessDataOk:         totals.EssDataQuality.AnomalousHours == 0,
		BessAnomalousHours: totals.EssDataQuality.AnomalousHours,
		BessAnomalousDays:  totals.EssDataQuality.AnomalousDays,
		BessAnomalyReasons: reasonKeys(totals.EssDataQuality.ReasonCounts),
		PvKwh:              totals.PV,
		LoadKwh:           totals.Load,
		GridImportKwh:     totals.GridImport,
		GridExportKwh:     totals.GridExport,
		EssNetUah:         totals.EssNet,
		CapexUah:          totalCapex,
		MonthsWithData:    maxMonthsWithData,
	}
	if scope == "month" && totals.Ebitda != 0 {
		total.MonthsWithData = 1
	}
	for _, s := range resp.Sites {
		total.ScheduleReserveUah += s.ScheduleReserveUah
	}
	total.ActionReserveUah = total.ScheduleReserveUah + total.BessReserveUah
	resp.Totals = total
	resp.MonthsWithData = maxMonthsWithData

	resp.Trend = make([]EconomicsPortfolioTrendMonth, 0, len(trendOrder))
	for _, ym := range trendOrder {
		resp.Trend = append(resp.Trend, *trendAcc[ym])
	}
	return resp, weights
}

// portfolioWeights weighs an object's EBITDA over the months [from, to]
// for the portfolio's rate. A failed read leaves the object out of the
// rate rather than failing the whole rollup.
func (h *Handlers) portfolioWeights(ctx context.Context, svc *economics.Service, orgID, from, to string, loc *time.Location) economics.EbitdaWeights {
	first, last, ok := monthsWindow(from, to, loc)
	if !ok {
		return economics.EbitdaWeights{}
	}
	weights, err := svc.EbitdaWeights(ctx, orgID, first, last)
	if err != nil {
		h.log.Warn("api_economics_portfolio_fx", "organization_id", orgID, "from", from, "to", to, "err", err)
	}
	return weights
}

// maxRecomputeRange caps the recompute span. Unlike the on-read
// endpoints, a recompute iterates day-by-day with progress reporting and
// honors cancellation between days, so a multi-year full-history backfill
// is safe — the cap only guards against absurd inputs. It is wider than
// maxDAMRange because "recompute the whole available history" for a site
// can legitimately exceed a single year.
const maxRecomputeRange = 5 * 366 * 24 * time.Hour

// economicsRecompute recomputes (and persists) economics over a date
// range, streaming NDJSON progress (one line per day, then "done").
//
//	POST /api/v1/economics/recompute?organization_id=&from=&to=&tz=
func (h *Handlers) economicsRecompute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.economics == nil {
		http.Error(w, "economics service not configured", http.StatusServiceUnavailable)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromStr == "" || toStr == "" {
		http.Error(w, "from and to are required (YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		http.Error(w, "from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		http.Error(w, "to must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if to.Before(from) {
		http.Error(w, "to must be on or after from", http.StatusBadRequest)
		return
	}
	if to.Sub(from) > maxRecomputeRange {
		http.Error(w, fmt.Sprintf("range must be <= %s", maxRecomputeRange), http.StatusBadRequest)
		return
	}
	tzStr := strings.TrimSpace(r.URL.Query().Get("tz"))
	loc, err := loadLocation(tzStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if rc := http.NewResponseController(w); rc != nil {
		_ = rc.SetWriteDeadline(time.Time{})
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	emit := func(ev progressEvent) {
		_ = enc.Encode(ev)
		if flusher != nil {
			flusher.Flush()
		}
	}

	start := time.Now()
	result, rerr := h.economics.RecomputeRange(r.Context(), orgID, fromStr, toStr, loc.String(), func(done, total int, label string) {
		emit(progressEvent{Type: "progress", Done: done, Total: total, Label: label})
	})
	if rerr != nil {
		if r.Context().Err() != nil {
			h.log.Info("api_economics_recompute_cancelled", "organization_id", orgID, "from", fromStr, "to", toStr)
			emit(progressEvent{Type: "error", Error: "recompute cancelled"})
			return
		}
		h.log.Error("api_economics_recompute", "organization_id", orgID, "from", fromStr, "to", toStr, "err", rerr)
		emit(progressEvent{Type: "error", Error: rerr.Error()})
		return
	}
	h.log.Info("api_economics_recompute_ok",
		"organization_id", orgID,
		"from", fromStr,
		"to", toStr,
		"tz", loc.String(),
		"days", result.Days,
		"days_ok", result.DaysOK,
		"days_failed", result.DaysFailed,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	emit(progressEvent{Type: "done", Result: result})
}

// economicsDataRangeResponse reports the civil-date span (YYYY-MM-DD in
// the requested tz) covered by an organization's raw telemetry, backing
// the "recompute the whole available history" flow. HasData is false —
// and the dates empty — when the org has no samples yet.
type economicsDataRangeResponse struct {
	From    string `json:"from"`
	To      string `json:"to"`
	HasData bool   `json:"has_data"`
}

// economicsDataRange reports the earliest/latest telemetry dates for an
// organization so the recompute UI can auto-fill the full period.
//
//	GET /api/v1/economics/data-range?organization_id=&tz=
func (h *Handlers) economicsDataRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	loc, err := loadLocation(strings.TrimSpace(r.URL.Query().Get("tz")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	minTS, maxTS, ok, err := h.telemetryDataRange(r.Context(), orgID)
	if err != nil {
		h.log.Error("api_economics_data_range", "organization_id", orgID, "err", err)
		http.Error(w, "failed to read telemetry data range", http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, economicsDataRangeResponse{HasData: false})
		return
	}
	writeJSON(w, http.StatusOK, economicsDataRangeResponse{
		From:    minTS.In(loc).Format("2006-01-02"),
		To:      maxTS.In(loc).Format("2006-01-02"),
		HasData: true,
	})
}

// tariffScheduleEntryReq is the PUT body for one effective-dated tariff
// version.
type tariffScheduleEntryReq struct {
	EffectiveFrom string     `json:"effective_from"`
	Tariffs       OrgTariffs `json:"tariffs"`
}

// organizationTariffSchedule manages the date-versioned tariff schedule.
//
//	GET    /api/v1/organization-tariff-schedule?organization_id=
//	PUT    /api/v1/organization-tariff-schedule?organization_id=  (body: {effective_from, tariffs})
//	DELETE /api/v1/organization-tariff-schedule?organization_id=&effective_from=
func (h *Handlers) organizationTariffSchedule(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getTariffSchedule(w, r)
	case http.MethodPut:
		h.putTariffSchedule(w, r)
	case http.MethodDelete:
		h.deleteTariffSchedule(w, r)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handlers) getTariffSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	versions, err := h.tariffVersions(r.Context(), orgID)
	if err != nil {
		h.log.Error("api_tariff_schedule_get", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if versions == nil {
		versions = []TariffScheduleVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (h *Handlers) putTariffSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	if rejectGroupWrite(w, orgID) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req tariffScheduleEntryReq
	if err := dec.Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}
	eff, err := time.Parse("2006-01-02", strings.TrimSpace(req.EffectiveFrom))
	if err != nil {
		http.Error(w, "effective_from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	if err := validateOrgTariffs(req.Tariffs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.store.UpsertTariffScheduleVersion(r.Context(), orgID, eff, req.Tariffs); err != nil {
		h.log.Error("api_tariff_schedule_put", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.log.Info("api_tariff_schedule_put_ok", "organization_id", orgID, "effective_from", req.EffectiveFrom)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) deleteTariffSchedule(w http.ResponseWriter, r *http.Request) {
	orgID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if orgID == "" {
		http.Error(w, "organization_id is required", http.StatusBadRequest)
		return
	}
	if rejectGroupWrite(w, orgID) {
		return
	}
	effStr := strings.TrimSpace(r.URL.Query().Get("effective_from"))
	eff, err := time.Parse("2006-01-02", effStr)
	if err != nil {
		http.Error(w, "effective_from must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	n, err := h.store.DeleteTariffScheduleVersion(r.Context(), orgID, eff)
	if err != nil {
		h.log.Error("api_tariff_schedule_delete", "organization_id", orgID, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		http.Error(w, "tariff version not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetEconomicsService installs the economics compute/serve service that
// the /api/v1/economics/* routes dispatch to. Calling with nil removes
// it; the routes stay registered and respond 503 per-request.
func (h *Handlers) SetEconomicsService(svc *economics.Service) {
	h.economics = svc
}

// NewEconomicsBackend builds the economics.Backend adapter from the
// handlers (energy-flow allocator + store reads) and the concrete store
// (persistence + tariff schedule). Exposed so cmd/api/main.go can wire
// the service after constructing both.
func NewEconomicsBackend(h *Handlers, store *Store) economics.Backend {
	return newEconomicsBackend(h, store)
}
