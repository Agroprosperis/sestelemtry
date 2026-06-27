package api

import (
	"encoding/json"
	"fmt"
	"net/http"
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

// EconomicsMonthlyResponse is the body of GET /api/v1/economics/monthly.
type EconomicsMonthlyResponse struct {
	OrganizationID string                      `json:"organization_id"`
	Month          string                      `json:"month"`
	Tz             string                      `json:"tz"`
	DaysInMonth    int                         `json:"days_in_month"`
	Totals         EconomicsMonthlyTotals      `json:"totals"`
	Days           []EconomicsMonthlyDay       `json:"days"`
	HourlyMargin   []EconomicsMonthlyDayMargin `json:"hourly_margin"`
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
	}
	for _, d := range month.Days {
		resp.Days = append(resp.Days, monthlyDayToJSON(d))
	}
	for _, m := range month.HourlyMargin {
		resp.HourlyMargin = append(resp.HourlyMargin, EconomicsMonthlyDayMargin{Date: m.Date, Hours: m.Hours})
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
