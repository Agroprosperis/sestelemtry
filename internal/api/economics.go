package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/storage"
)

// economicsBackend adapts the api layer (energy-flow allocator + store
// queries + economics persistence) to the economics.Backend interface so
// the economics.Service stays free of pgx / HTTP wiring.
type economicsBackend struct {
	h     *Handlers
	store *Store
}

func newEconomicsBackend(h *Handlers, store *Store) *economicsBackend {
	return &economicsBackend{h: h, store: store}
}

func (b *economicsBackend) HourlyFlows(ctx context.Context, orgID string, dayStart time.Time) ([]economics.FlowRow, error) {
	resp, err := b.h.computeEnergyFlowHourly(ctx, orgID, dayStart, dayStart.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	out := make([]economics.FlowRow, len(resp.Hours))
	for i, hr := range resp.Hours {
		out[i] = economics.FlowRow{
			From:              hr.From,
			EssCharged:        hr.EssChargedKwh,
			EssDischarged:     hr.EssDischargedKwh,
			PVToEss:           hr.PVToESSKwh,
			GridToEss:         hr.GridToESSKwh,
			EssToLoad:         hr.ESSToLoadKwh,
			EssToGrid:         hr.ESSToGridKwh,
			EssPeakIntervalKw: hr.EssPeakIntervalKw,
		}
	}
	return out, nil
}

func (b *economicsBackend) Timeseries(ctx context.Context, orgID string, metricKeys []string, from, to time.Time, bucket, tz, aggregation string) ([]economics.Point, error) {
	resp, err := b.store.Timeseries(ctx, orgID, metricKeys, from, to, bucket, tz, TimeseriesAggregation(aggregation))
	if err != nil {
		return nil, err
	}
	out := make([]economics.Point, 0, len(resp.Points))
	for _, p := range resp.Points {
		out = append(out, economics.Point{Time: p.Time, MetricKey: p.MetricKey, Value: p.Value})
	}
	return out, nil
}

func (b *economicsBackend) DAMPrices(ctx context.Context, zone int, from, to time.Time) ([]economics.DAMHour, error) {
	resp, err := b.store.DAMPrices(ctx, zone, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]economics.DAMHour, 0, len(resp.Prices))
	for _, p := range resp.Prices {
		out = append(out, economics.DAMHour{
			DeliveryDate:   p.DeliveryDate,
			Hour:           p.Hour,
			Zone:           p.Zone,
			PriceUAHPerMWh: p.PriceUAHPerMWh,
		})
	}
	return out, nil
}

func (b *economicsBackend) TariffSchedule(ctx context.Context, orgID string) (economics.Schedule, error) {
	versions, err := b.store.GetTariffScheduleVersions(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make(economics.Schedule, 0, len(versions))
	for _, v := range versions {
		// effective_from is a civil date; parse in UTC (ResolveForDay
		// compares civil dates only, so the location is irrelevant).
		eff, err := time.Parse("2006-01-02", v.EffectiveFrom)
		if err != nil {
			continue
		}
		out = append(out, economics.ScheduleEntry{
			EffectiveFrom: eff,
			Tariffs:       orgTariffsToEconomics(v.Tariffs),
		})
	}
	return out, nil
}

func (b *economicsBackend) CanonicalDaily(ctx context.Context, orgID string, day time.Time) (economics.CanonicalDaily, bool, error) {
	// Key by the civil date (UTC midnight) so the lookup matches the
	// importer's Europe/Kyiv day keying regardless of the request tz.
	key := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	row, ok, err := b.store.GetFusionDailyKpi(ctx, orgID, key)
	if err != nil || !ok {
		return economics.CanonicalDaily{}, false, err
	}
	return economics.CanonicalDaily{
		PV:            row.PVYield,
		Load:          row.UsePower,
		GridImport:    row.BuyPower,
		GridExport:    row.OnGridPower,
		EssCharged:    row.ChargeCap,
		EssDischarged: row.DischargeCap,
	}, true, nil
}

func (b *economicsBackend) SaveDay(ctx context.Context, day economics.StoredDay) error {
	hourly := make([]storage.EconomicsHourlyRow, 0, len(day.Rows))
	for _, r := range day.Rows {
		if r == nil {
			continue
		}
		hourly = append(hourly, hourRowToStorage(day.OrganizationID, r))
	}
	if err := b.store.SaveEconomicsHourly(ctx, hourly); err != nil {
		return err
	}
	return b.store.SaveEconomicsDaily(ctx, dailyToStorage(day))
}

func (b *economicsBackend) LoadDay(ctx context.Context, orgID string, dayStart time.Time, tz string) (economics.StoredDay, bool, error) {
	daily, ok, err := b.store.GetEconomicsDaily(ctx, orgID, dayStart)
	if err != nil {
		return economics.StoredDay{}, false, err
	}
	if !ok {
		return economics.StoredDay{}, false, nil
	}
	hourRows, err := b.store.GetEconomicsHourly(ctx, orgID, dayStart, dayStart.AddDate(0, 0, 1))
	if err != nil {
		return economics.StoredDay{}, false, err
	}
	rows := make([]*economics.HourRow, 24)
	for i := range hourRows {
		hr := hourRows[i]
		offset := int(hr.HourStart.Sub(dayStart).Hours() + 0.5)
		if offset < 0 || offset >= 24 {
			continue
		}
		rows[offset] = storageToHourRow(offset, &hr)
	}
	return economics.StoredDay{
		OrganizationID: orgID,
		Day:            dayStart,
		Tz:             tz,
		Rows:           rows,
		Totals:         storageToDailyTotals(daily),
		IsFinal:        daily.IsFinal,
		ComputedAt:     daily.ComputedAt,
	}, true, nil
}

func (b *economicsBackend) LoadDailyRange(ctx context.Context, orgID string, from, to time.Time) ([]economics.DailyRecord, error) {
	rows, err := b.store.GetEconomicsDailyRange(ctx, orgID, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]economics.DailyRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, economics.DailyRecord{
			Day:        row.Day,
			Totals:     storageToDailyTotals(row),
			IsFinal:    row.IsFinal,
			ComputedAt: row.ComputedAt,
		})
	}
	return out, nil
}

func (b *economicsBackend) SumEbitdaBefore(ctx context.Context, orgID string, before time.Time) (float64, int, error) {
	return b.store.SumEconomicsEbitdaBefore(ctx, orgID, before)
}

func (b *economicsBackend) LoadHourlyRange(ctx context.Context, orgID string, from, to time.Time) ([]economics.HourlyRecord, error) {
	rows, err := b.store.GetEconomicsHourly(ctx, orgID, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]economics.HourlyRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, economics.HourlyRecord{
			HourStart:            row.HourStart,
			Rdn:                  row.Rdn,
			ImportPrice:          row.ImportPrice,
			ExportPrice:          row.ExportPrice,
			GridImport:           row.ImportTotal,
			PV:                   row.PVGeneration,
			PVToLoad:             row.PVToLoad,
			PVToGrid:             row.PVToGrid,
			PVToEss:              row.PVToEss,
			GridToEss:            row.GridToEss,
			GridToLoad:           row.GridToLoad,
			EssToLoad:            row.EssToLoad,
			EssToGrid:            row.EssToGrid,
			EssCharged:           row.EssCharged,
			EssDischarged:        row.EssDischarged,
			EssNet:               row.EssNet,
			EssRealizedProfitUah: row.EssRealizedProfit,
			EssWithdrawnCostUah:  row.EssWithdrawnCost,
			EssRemainingKwhStart: row.EssRemainingKwhStart,
			// Persisted during recompute (energy-flow per-day walk); read
			// cheaply here so the wide-window monthly/annual/portfolio path
			// never re-runs the raw allocator synchronously.
			EssPeakIntervalKw: row.EssPeakIntervalKw,
		})
	}
	return out, nil
}

// --- conversions ---

func orgTariffsToEconomics(t OrgTariffs) economics.Tariffs {
	return economics.Tariffs{
		DistributionUahPerKwh:   t.DistributionUahPerKwh,
		TransmissionUahPerKwh:   t.TransmissionUahPerKwh,
		SupplierMarginUahPerKwh: t.SupplierMarginUahPerKwh,
		SupplierMarginMode:      t.SupplierMarginMode,
		SupplierMarginPct:       t.SupplierMarginPct,
		OtherFeesUahPerKwh:      t.OtherFeesUahPerKwh,
		ExportDiscount:          t.ExportDiscount,
		DegradationUahPerKwh:    t.DegradationUahPerKwh,
		IncludeVat:              t.IncludeVat,
		VatRate:                 t.VatRate,
		EssCapacityKwh:          t.EssCapacityKwh,
		EssPowerLimitKw:         t.EssPowerLimitKw,
		RoundtripEfficiency:     t.RoundtripEfficiency,
	}
}

func hourRowToStorage(orgID string, r *economics.HourRow) storage.EconomicsHourlyRow {
	return storage.EconomicsHourlyRow{
		OrganizationID: orgID,
		HourStart:      r.HourStart,
		Rdn:            r.Rdn,
		ImportPrice:    r.Econ.ImportPrice,
		ExportPrice:    r.Econ.ExportPrice,
		PVGeneration:   r.Flow.PV,
		ImportTotal:    r.Flow.GridImport,
		ExportTotal:    r.Flow.GridExport,
		LoadTotal:      r.Econ.Load,
		PVToLoad:       r.Econ.PVToLoad,
		PVToGrid:       r.Econ.PVToGrid,
		GridToLoad:     r.Econ.GridToLoad,
		PVToEss:        r.Flow.PVToEss,
		GridToEss:      r.Flow.GridToEss,
		EssToLoad:      r.Flow.EssToLoad,
		EssToGrid:      r.Flow.EssToGrid,
		EssCharged:     r.Flow.EssCharged,
		EssDischarged:  r.Flow.EssDischarged,
		BaselineCost:   r.Econ.BaselineCost,
		ActualCost:     r.Econ.ActualCost,
		Effect:         r.Econ.Effect,
		EssNet:         r.Econ.EssNet,
		EssRemainingKwhStart: r.EssRemainingKwhStart,
		EssAvgCostStart:      r.EssAvgCostUahPerKwhStart,
		EssCostBasisStart:    r.EssCostBasisUahStart,
		EssWithdrawnCost:     r.EssWithdrawnCostUah,
		EssRealizedProfit:    r.EssRealizedProfitUah,
		EssAvgCostEnd:        r.EssAvgCostUahPerKwhEnd,
		EssCostBasisEnd:      r.EssCostBasisUahEnd,
		EssResidualEnd:       r.EssResidualKwhEnd,
		EssPeakIntervalKw:    r.EssPeakIntervalKw,
	}
}

func storageToHourRow(hour int, r *storage.EconomicsHourlyRow) *economics.HourRow {
	return &economics.HourRow{
		Hour:      hour,
		HourStart: r.HourStart,
		Rdn:       r.Rdn,
		Flow: economics.HourFlows{
			PV:            r.PVGeneration,
			GridImport:    r.ImportTotal,
			GridExport:    r.ExportTotal,
			EssCharged:    r.EssCharged,
			EssDischarged: r.EssDischarged,
			PVToEss:       r.PVToEss,
			GridToEss:     r.GridToEss,
			EssToLoad:     r.EssToLoad,
			EssToGrid:     r.EssToGrid,
		},
		Econ: economics.HourEconomics{
			Load:        r.LoadTotal,
			PVToLoad:    r.PVToLoad,
			PVToGrid:    r.PVToGrid,
			GridToLoad:  r.GridToLoad,
			ImportPrice: r.ImportPrice,
			ExportPrice: r.ExportPrice,
			BaselineCost: r.BaselineCost,
			ActualCost:   r.ActualCost,
			Effect:       r.Effect,
			EssNet:       r.EssNet,
		},
		EssRemainingKwhStart:     r.EssRemainingKwhStart,
		EssCostBasisUahStart:     r.EssCostBasisStart,
		EssAvgCostUahPerKwhStart: r.EssAvgCostStart,
		EssWithdrawnCostUah:      r.EssWithdrawnCost,
		EssRealizedProfitUah:     r.EssRealizedProfit,
		EssCostBasisUahEnd:       r.EssCostBasisEnd,
		EssAvgCostUahPerKwhEnd:   r.EssAvgCostEnd,
		EssResidualKwhEnd:        r.EssResidualEnd,
		EssPeakIntervalKw:        r.EssPeakIntervalKw,
	}
}

func dailyToStorage(day economics.StoredDay) storage.EconomicsDailyRow {
	t := day.Totals
	var reconciliation json.RawMessage
	if len(t.Reconciliation) > 0 {
		if buf, err := json.Marshal(t.Reconciliation); err == nil {
			reconciliation = buf
		}
	}
	return storage.EconomicsDailyRow{
		OrganizationID:    day.OrganizationID,
		Day:               day.Day,
		Tz:                day.Tz,
		BaselineCost:      t.BaselineCost,
		ActualCost:        t.ActualCost,
		Effect:            t.Effect,
		EssNet:            t.EssNet,
		Load:              t.Load,
		PV:                t.PV,
		GridImport:        t.GridImport,
		GridExport:        t.GridExport,
		EssCharged:        t.EssCharged,
		EssDischarged:     t.EssDischarged,
		PVToLoad:          t.PVToLoad,
		PVToEss:           t.PVToEss,
		PVToGrid:          t.PVToGrid,
		GridToLoad:        t.GridToLoad,
		GridToEss:         t.GridToEss,
		EssToLoad:         t.EssToLoad,
		EssToGrid:         t.EssToGrid,
		AvgImportPrice:    t.AvgImportPrice,
		AvgExportPrice:    t.AvgExportPrice,
		RevenuePvExport:   t.RevenuePvExport,
		RevenuePvSelf:     t.RevenuePvSelf,
		RevenueEssExport:  t.RevenueEssExport,
		RevenueEssSelf:    t.RevenueEssSelf,
		RevenueTotal:      t.RevenueTotal,
		ExpenseGridCharge: t.ExpenseGridCharge,
		ExpenseTotal:      t.ExpenseTotal,
		Ebitda:            t.Ebitda,
		EssWithdrawnCost:   t.EssWithdrawnCost,
		EssRealizedProfit:  t.EssRealizedProfit,
		EssDegradationCost: t.EssDegradationCost,
		EssAvgCostBasisEod: t.EssAvgCostBasisEod,
		EssResidualKwhEod:  t.EssResidualKwhEod,
		EssCostBasisUahEod: t.EssCostBasisUahEod,
		HoursWithData:      t.HoursWithData,
		HoursMissingPrice:  t.HoursMissingPrice,
		IsFinal:            day.IsFinal,
		Reconciled:         t.Reconciled,
		QualityFlags:       t.QualityFlags,
		Reconciliation:     reconciliation,
	}
}

func storageToDailyTotals(r storage.EconomicsDailyRow) economics.DailyTotals {
	var reconciliation map[string]economics.ReconcileField
	if len(r.Reconciliation) > 0 {
		_ = json.Unmarshal(r.Reconciliation, &reconciliation)
	}
	return economics.DailyTotals{
		BaselineCost:      r.BaselineCost,
		ActualCost:        r.ActualCost,
		Effect:            r.Effect,
		EssNet:            r.EssNet,
		Load:              r.Load,
		PV:                r.PV,
		GridImport:        r.GridImport,
		GridExport:        r.GridExport,
		EssCharged:        r.EssCharged,
		EssDischarged:     r.EssDischarged,
		PVToLoad:          r.PVToLoad,
		PVToEss:           r.PVToEss,
		PVToGrid:          r.PVToGrid,
		GridToLoad:        r.GridToLoad,
		GridToEss:         r.GridToEss,
		EssToLoad:         r.EssToLoad,
		EssToGrid:         r.EssToGrid,
		HoursWithData:     r.HoursWithData,
		HoursMissingPrice: r.HoursMissingPrice,
		AvgImportPrice:    r.AvgImportPrice,
		AvgExportPrice:    r.AvgExportPrice,
		RevenuePvExport:   r.RevenuePvExport,
		RevenuePvSelf:     r.RevenuePvSelf,
		RevenueEssExport:  r.RevenueEssExport,
		RevenueEssSelf:    r.RevenueEssSelf,
		RevenueTotal:      r.RevenueTotal,
		ExpenseGridCharge: r.ExpenseGridCharge,
		ExpenseTotal:      r.ExpenseTotal,
		Ebitda:            r.Ebitda,
		EssWithdrawnCost:   r.EssWithdrawnCost,
		EssRealizedProfit:  r.EssRealizedProfit,
		EssDegradationCost: r.EssDegradationCost,
		EssAvgCostBasisEod: r.EssAvgCostBasisEod,
		EssResidualKwhEod:  r.EssResidualKwhEod,
		EssCostBasisUahEod: r.EssCostBasisUahEod,
		Reconciled:         r.Reconciled,
		QualityFlags:       r.QualityFlags,
		Reconciliation:     reconciliation,
	}
}
