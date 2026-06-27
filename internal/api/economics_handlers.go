package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
)

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
//
//	GET /api/v1/economics/daily?organization_id=&date=YYYY-MM-DD&tz=
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
	if _, err := time.ParseInLocation("2006-01-02", dateStr, loc); err != nil {
		http.Error(w, "date must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	day, err := h.economics.GetDay(r.Context(), orgID, dateStr, loc.String())
	if err != nil {
		h.log.Error("api_economics_daily", "organization_id", orgID, "date", dateStr, "tz", loc.String(), "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := EconomicsDailyResponse{
		OrganizationID: orgID,
		Date:           dateStr,
		Tz:             loc.String(),
		IsFinal:        day.IsFinal,
		Hours:          make([]*EconomicsHour, len(day.Rows)),
		Reconciled:     day.Totals.Reconciled,
		QualityFlags:   day.Totals.QualityFlags,
		Reconciliation: day.Totals.Reconciliation,
	}
	for i, row := range day.Rows {
		resp.Hours[i] = economicsHourToJSON(row)
		if row != nil && row.Rdn == nil {
			resp.HoursMissingPrice++
		}
	}
	writeJSON(w, http.StatusOK, resp)
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
// anomalous days (physically impossible charge/discharge readings) are
// excluded from the fact/optimum/reserve above.
type EconomicsDataQuality struct {
	DataOk                     bool     `json:"data_ok"`
	TotalDays                  int      `json:"total_days"`
	AnomalousDays              int      `json:"anomalous_days"`
	AnomalousDates             []string `json:"anomalous_dates"`
	MaxChargeKwhPerInterval    float64  `json:"max_charge_kwh_per_interval"`
	MaxDischargeKwhPerInterval float64  `json:"max_discharge_kwh_per_interval"`
	PowerLimitKwhPerInterval   float64  `json:"power_limit_kwh_per_interval"`
}

func dataQualityToJSON(q economics.DataQuality) EconomicsDataQuality {
	return EconomicsDataQuality{
		DataOk:                     q.DataOK,
		TotalDays:                  q.TotalDays,
		AnomalousDays:              q.AnomalousDays,
		AnomalousDates:             q.AnomalousDates,
		MaxChargeKwhPerInterval:    q.MaxChargeKwhPerInterval,
		MaxDischargeKwhPerInterval: q.MaxDischargeKwhPerInterval,
		PowerLimitKwhPerInterval:   q.PowerLimitKwhPerInterval,
	}
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
}

// EconomicsMonthlyDayMargin is one heatmap row: 24 hourly ESS margins
// (UAH per kWh discharged; null when the hour had no discharge/price).
type EconomicsMonthlyDayMargin struct {
	Date  string     `json:"date"`
	Hours []*float64 `json:"hours"`
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
	}
}

// economicsMonthly serves a month rollup of the per-day economics,
// read-through (final days from cache, the open tail recomputed on read).
//
//	GET /api/v1/economics/monthly?organization_id=&month=YYYY-MM&tz=
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
	if _, err := time.ParseInLocation("2006-01", monthStr, loc); err != nil {
		http.Error(w, "month must be YYYY-MM", http.StatusBadRequest)
		return
	}

	month, err := h.economics.GetMonth(r.Context(), orgID, monthStr, loc.String())
	if err != nil {
		h.log.Error("api_economics_monthly", "organization_id", orgID, "month", monthStr, "tz", loc.String(), "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
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
	}
	for _, d := range month.Days {
		resp.Days = append(resp.Days, monthlyDayToJSON(d))
	}
	for _, m := range month.HourlyMargin {
		resp.HourlyMargin = append(resp.HourlyMargin, EconomicsMonthlyDayMargin{Date: m.Date, Hours: m.Hours})
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
// margins (UAH per kWh discharged) averaged across the month; null
// when that hour had no discharge all month.
type EconomicsAnnualMonthMargin struct {
	Month string     `json:"month"`
	Hours []*float64 `json:"hours"`
}

// EconomicsAnnualResponse is the body of GET /api/v1/economics/annual.
type EconomicsAnnualResponse struct {
	OrganizationID string                       `json:"organization_id"`
	Period         string                       `json:"period"`
	From           string                       `json:"from"`
	To             string                       `json:"to"`
	Tz             string                       `json:"tz"`
	MonthsWithData int                          `json:"months_with_data"`
	Totals         EconomicsMonthlyTotals       `json:"totals"`
	Months         []EconomicsAnnualMonthRollup `json:"months"`
	Quarters       []EconomicsAnnualQuarter     `json:"quarters"`
	MonthlyMargin  []EconomicsAnnualMonthMargin `json:"monthly_margin"`
}

// economicsAnnual serves a calendar-year rollup of the per-month
// economics, read-through (the economics-recompute daemon keeps the
// underlying daily/hourly records warm; this path never recomputes).
//
//	GET /api/v1/economics/annual?organization_id=&period=YYYY&tz=
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
		year, err = h.economics.GetPeriod(r.Context(), orgID, fromStr, toStr, loc.String())
	} else {
		if periodStr == "" {
			http.Error(w, "period is required (YYYY)", http.StatusBadRequest)
			return
		}
		if _, perr := time.ParseInLocation("2006", periodStr, loc); perr != nil {
			http.Error(w, "period must be YYYY", http.StatusBadRequest)
			return
		}
		year, err = h.economics.GetYear(r.Context(), orgID, periodStr, loc.String())
	}
	if err != nil {
		h.log.Error("api_economics_annual", "organization_id", orgID, "period", periodStr, "from", fromStr, "to", toStr, "tz", loc.String(), "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := EconomicsAnnualResponse{
		OrganizationID: orgID,
		Period:         year.Period,
		From:           year.From,
		To:             year.To,
		Tz:             loc.String(),
		MonthsWithData: year.MonthsWithData,
		Totals:         monthlyTotalsToJSON(year.Totals),
		Months:         make([]EconomicsAnnualMonthRollup, 0, len(year.Months)),
		Quarters:       make([]EconomicsAnnualQuarter, 0, len(year.Quarters)),
		MonthlyMargin:  make([]EconomicsAnnualMonthMargin, 0, len(year.MonthlyMargin)),
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
		resp.MonthlyMargin = append(resp.MonthlyMargin, EconomicsAnnualMonthMargin{Month: m.Month, Hours: m.Hours})
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
	BessDataOk         bool    `json:"bess_data_ok"`
	BessAnomalousDays  int     `json:"bess_anomalous_days"`
	PvKwh              float64 `json:"pv_kwh"`
	LoadKwh            float64 `json:"load_kwh"`
	GridImportKwh      float64 `json:"grid_import_kwh"`
	GridExportKwh      float64 `json:"grid_export_kwh"`
	EssNetUah          float64 `json:"ess_net_uah"`
}

// EconomicsPortfolioTrendMonth is one month of the portfolio energy trend
// (year scope): the YYYY-MM key plus the sum across all objects.
type EconomicsPortfolioTrendMonth struct {
	Month         string  `json:"month"`
	PvKwh         float64 `json:"pv_kwh"`
	LoadKwh       float64 `json:"load_kwh"`
	GridImportKwh float64 `json:"grid_import_kwh"`
	GridExportKwh float64 `json:"grid_export_kwh"`
	EffectUah     float64 `json:"effect_uah"`
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
		BessAnomalousDays:  t.EssDataQuality.AnomalousDays,
		PvKwh:              t.PV,
		LoadKwh:            t.Load,
		GridImportKwh:      t.GridImport,
		GridExportKwh:      t.GridExport,
		EssNetUah:          t.EssNet,
	}
}

// economicsPortfolio serves a portfolio (all-objects) rollup for a month
// or a year/window: per-object project effect + work-schedule and УЗЕ
// reserves (project_net) + УЗЕ data-quality flags, plus a portfolio total.
//
//	GET /api/v1/economics/portfolio?month=YYYY-MM&tz=
//	GET /api/v1/economics/portfolio?period=YYYY&tz=
//	GET /api/v1/economics/portfolio?from=YYYY-MM&to=YYYY-MM&tz=
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

	resp := EconomicsPortfolioResponse{
		Scope: scope,
		Label: label,
		Tz:    loc.String(),
		Sites: make([]EconomicsPortfolioSite, 0, len(h.organizations)),
	}
	var totals economics.MonthlyTotals
	var maxMonthsWithData int
	trendAcc := make(map[string]*EconomicsPortfolioTrendMonth)
	var trendOrder []string

	for _, org := range h.organizations {
		var t economics.MonthlyTotals
		hasData := false
		if scope == "month" {
			m, err := h.economics.GetMonth(r.Context(), org.ID, monthStr, loc.String())
			if err != nil {
				h.log.Warn("api_economics_portfolio_org", "organization_id", org.ID, "month", monthStr, "err", err)
			} else {
				t = m.Totals
				hasData = t.DaysWithData > 0
			}
		} else {
			var y economics.StoredYear
			if fromStr != "" || toStr != "" {
				y, err = h.economics.GetPeriod(r.Context(), org.ID, fromStr, toStr, loc.String())
			} else {
				y, err = h.economics.GetYear(r.Context(), org.ID, periodStr, loc.String())
			}
			if err != nil {
				h.log.Warn("api_economics_portfolio_org", "organization_id", org.ID, "period", periodStr, "err", err)
			} else {
				t = y.Totals
				hasData = y.MonthsWithData > 0
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
					row.EffectUah += mr.Totals.Effect
				}
			}
		}

		resp.Sites = append(resp.Sites, portfolioSiteFromTotals(org.ID, org.Name, t, hasData))
		if hasData {
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
			totals.EssDataQuality.AnomalousDays += t.EssDataQuality.AnomalousDays
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
		ID:                "__total__",
		Name:              "Портфель",
		HasData:           true,
		EffectUah:         totals.Effect,
		EbitdaUah:         totals.Ebitda,
		BessReserveUah:    totals.EssReserve,
		BessDataOk:        totals.EssDataQuality.AnomalousDays == 0,
		BessAnomalousDays: totals.EssDataQuality.AnomalousDays,
		PvKwh:             totals.PV,
		LoadKwh:           totals.Load,
		GridImportKwh:     totals.GridImport,
		GridExportKwh:     totals.GridExport,
		EssNetUah:         totals.EssNet,
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

	writeJSON(w, http.StatusOK, resp)
}

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
	if to.Sub(from) > maxDAMRange {
		http.Error(w, fmt.Sprintf("range must be <= %s", maxDAMRange), http.StatusBadRequest)
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
	versions, err := h.store.GetTariffScheduleVersions(r.Context(), orgID)
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
