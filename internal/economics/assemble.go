package economics

import (
	"math"
	"time"
)

// Model constants — mirror the TS pipeline exactly.
const (
	// DAMZone 2 is the unified UA grid (the daily-economics target).
	DAMZone = 2
	// historyHours is the cost-basis lookback window.
	historyHours = 48
	// socResetThresholdPercent defines "deeply discharged": at or
	// below this fraction of capacity the residual is treated as a
	// free leftover and the WAC ledger restarts. It doubles as the
	// lower bound of the usable SOC window (see usableKwhFromSOC).
	socResetThresholdPercent = 10.0
	// socUsableMaxPercent is the upper bound of the usable SOC window.
	// The device reports SOC on the full-pack 0–100% scale, but only
	// the 10–90% band is usable, so it maps linearly onto the usable
	// EssCapacityKwh.
	socUsableMaxPercent = 90.0
)

// usableKwhFromSOC converts a device SOC percentage (full-pack 0–100
// scale) into usable residual energy in kWh. The 10–90% window maps
// linearly onto [0, capacityKwh], where capacityKwh is the *usable*
// capacity entered in the tariffs (SOC 10% → 0 kWh, 90% → capacityKwh).
// SOC outside the window clamps to the nearest edge, so the result is
// always within [0, capacityKwh].
func usableKwhFromSOC(socPercent, capacityKwh float64) float64 {
	frac := (socPercent - socResetThresholdPercent) / (socUsableMaxPercent - socResetThresholdPercent)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return frac * capacityKwh
}

// Point mirrors a /timeseries point (Time, MetricKey, Value).
type Point struct {
	Time      time.Time
	MetricKey string
	Value     float64
}

// FlowRow is one hour of directional flows from the energy-flow
// allocator (the EnergyFlowHourly response row). From is the hour start.
type FlowRow struct {
	From          time.Time
	EssCharged    float64
	EssDischarged float64
	PVToEss       float64
	GridToEss     float64
	EssToLoad     float64
	EssToGrid     float64
	// EssPeakIntervalKw is the largest sub-hourly (~5-min) implied ESS
	// charge/discharge power (kW) seen within the hour, carried verbatim
	// from the allocator's per-interval walk so the anomaly filter can
	// catch spikes the hourly sums hide. 0 when no sub-hourly signal.
	EssPeakIntervalKw float64
}

// DAMHour mirrors a /dam-prices row (delivery date, hour 1..24, zone,
// nullable price UAH/MWh).
type DAMHour struct {
	DeliveryDate time.Time
	Hour         int
	Zone         int
	PriceUAHPerMWh *float64
}

// HourRow is one assembled hour: input flows + computed economics +
// cost-basis snapshot. Pointer fields are nullable (no RDN price,
// missing SOC anchor). Port of TS `HourEconomicsRow`.
type HourRow struct {
	Hour      int
	HourStart time.Time
	Rdn       *float64
	Flow      HourFlows
	Econ      HourEconomics

	EssRemainingKwhStart   *float64
	EssCostBasisUahStart   *float64
	EssAvgCostUahPerKwhStart *float64
	EssWithdrawnCostUah    *float64
	EssRealizedProfitUah   *float64
	EssCostBasisUahEnd     *float64
	EssAvgCostUahPerKwhEnd *float64
	EssResidualKwhEnd      *float64

	// EssPeakIntervalKw is the sub-hourly (~5-min) implied ESS power peak
	// (kW) for the hour, carried through from the flow row unscaled (it is
	// a diagnostic power reading, not a flow subject to reconciliation).
	// Persisted so the wide-window monthly/annual anomaly filter can read
	// it without re-running the raw allocator.
	EssPeakIntervalKw float64
}

// DayInput carries everything AssembleDay needs for one target day. The
// orchestrator (the Service) gathers these via the Backend; the
// assembly stays pure so it can be unit-tested against the TS fixtures.
type DayInput struct {
	DayStart time.Time // local midnight in the target tz
	Tariffs  Tariffs

	TodayFlows     []FlowRow // up to 24
	YesterdayFlows []FlowRow
	DayBeforeFlows []FlowRow

	DeltaPoints        []Point // today pv/import/export hourly deltas
	HistoryDeltaPoints []Point // 48h history deltas
	SocPoints          []Point // last-per-hour SOC over history+today

	DamToday   []DAMHour
	DamHistory []DAMHour // 2-day history window

	// Canonical is the FusionSolar daily KPI for the target day. When
	// set, today's flows are scaled to match it (reconciliation);
	// nil leaves the computed flows untouched (parity with live).
	Canonical *CanonicalDaily
}

// AssembleDay builds the 24-element HourRow slice for the target day.
// Port of TS `assembleHourlyRows` (+ the SOC-residual and cost-basis
// passes). nil entries mean the hour had no flow row. The second return
// value carries the reconciliation diagnostics (Applied=false when no
// canonical KPI was supplied).
func AssembleDay(in DayInput) ([]*HourRow, ReconcileResult) {
	dayStart := in.DayStart
	pvByHour := bucketByHourOfDay(filterMetric(in.DeltaPoints, "accumulated_pv_energy_yield_kwh"), dayStart)
	importByHour := bucketByHourOfDay(filterMetric(in.DeltaPoints, "accumulated_electricity_purchased_kwh"), dayStart)
	exportByHour := bucketByHourOfDay(filterMetric(in.DeltaPoints, "accumulated_electricity_sold_kwh"), dayStart)
	socByOffset := bucketSocByOffset(filterMetric(in.SocPoints, "soc_percent"), dayStart)

	priceMap := buildPriceMap(in.DamToday)

	// Build the per-hour flow envelopes first so reconciliation can
	// scale them before any economics / cost-basis math runs.
	out := make([]*HourRow, 24)
	flows := make([]*HourFlows, 24)
	rdns := make([]*float64, 24)
	starts := make([]time.Time, 24)
	peaks := make([]float64, 24)
	for h := 0; h < 24; h++ {
		if h >= len(in.TodayFlows) {
			continue
		}
		flowRow := in.TodayFlows[h]
		peaks[h] = flowRow.EssPeakIntervalKw
		f := HourFlows{
			PV:            pvByHour[h],
			GridImport:    importByHour[h],
			GridExport:    exportByHour[h],
			EssCharged:    flowRow.EssCharged,
			EssDischarged: flowRow.EssDischarged,
			PVToEss:       flowRow.PVToEss,
			GridToEss:     flowRow.GridToEss,
			EssToLoad:     flowRow.EssToLoad,
			EssToGrid:     flowRow.EssToGrid,
		}
		flows[h] = &f
		starts[h] = flowRow.From
		if p, ok := priceMap[h]; ok {
			v := p
			rdns[h] = &v
		}
	}

	recon := reconcileFlows(flows, in.Canonical)

	for h := 0; h < 24; h++ {
		if flows[h] == nil {
			out[h] = nil
			continue
		}
		flow := *flows[h]
		rdn := rdns[h]
		var econ HourEconomics
		if rdn == nil {
			econ = HourEconomicsFor(0, flow, in.Tariffs)
		} else {
			econ = HourEconomicsFor(*rdn, flow, in.Tariffs)
		}
		out[h] = &HourRow{
			Hour:              h,
			HourStart:         starts[h],
			Rdn:               rdn,
			Flow:              flow,
			Econ:              econ,
			EssPeakIntervalKw: peaks[h],
		}
	}

	// Залишок УЗЕ second pass: anchor hour 0 from SOC[0] mapped through
	// the usable 10–90% window onto the usable capacity, then roll the
	// running residual forward by net charge − discharge.
	var running *float64
	if soc0, ok := socByOffset[0]; ok && !math.IsInf(soc0, 0) && !math.IsNaN(soc0) {
		v := usableKwhFromSOC(soc0, in.Tariffs.EssCapacityKwh)
		running = &v
	}
	for h := 0; h < 24; h++ {
		cur := out[h]
		if cur != nil {
			if running != nil {
				v := *running
				cur.EssRemainingKwhStart = &v
			} else {
				cur.EssRemainingKwhStart = nil
			}
		}
		if h == 23 {
			break
		}
		if running == nil || cur == nil {
			running = nil
		} else {
			v := *running + cur.Flow.PVToEss + cur.Flow.GridToEss - cur.Flow.EssToLoad - cur.Flow.EssToGrid
			running = &v
		}
	}

	// Cost-basis third pass: find the anchor in the 48h history,
	// pre-roll to today's 00:00, then roll each priced hour forward.
	history := buildHistoryRecords(in, socByOffset)
	soc0Ptr := (*float64)(nil)
	if soc0, ok := socByOffset[0]; ok {
		soc0Ptr = &soc0
	}
	state := findAnchorAndPreRoll(history, soc0Ptr, in.Tariffs)

	for h := 0; h < 24; h++ {
		cur := out[h]
		if cur == nil {
			continue
		}
		if cur.Rdn == nil {
			continue
		}
		result := RollHour(state, cur.Flow, cur.Econ.ImportPrice, cur.Econ.ExportPrice, in.Tariffs.DegradationUahPerKwh)
		startUah := state.Uah
		cur.EssCostBasisUahStart = &startUah
		avgStart := result.AvgCostStart
		cur.EssAvgCostUahPerKwhStart = &avgStart
		withdrawn := result.WithdrawnCostUah
		cur.EssWithdrawnCostUah = &withdrawn
		realized := result.RealizedProfitUah
		cur.EssRealizedProfitUah = &realized
		endUah := result.Next.Uah
		cur.EssCostBasisUahEnd = &endUah
		avgEnd := result.AvgCostEnd
		cur.EssAvgCostUahPerKwhEnd = &avgEnd
		endKwh := result.Next.Kwh
		cur.EssResidualKwhEnd = &endKwh
		state = result.Next
	}
	return out, recon
}

// hourHistoryRecord is one hour of pre-today data: flow envelope, RDN
// price (nil when unpriced), SOC at the start of the hour (nil when
// missing). Port of TS `HourHistoryRecord`.
type hourHistoryRecord struct {
	flow            HourFlows
	rdnUahPerKwh    *float64
	socPercentStart *float64
}

// buildHistoryRecords stitches the day-before + yesterday flows, the 48h
// history deltas, the 2-day DAM, and the SOC-by-offset map into a 48-long
// chronological array. Port of TS `buildHistoryRecords`.
func buildHistoryRecords(in DayInput, socByOffset map[int]float64) []hourHistoryRecord {
	dayStart := in.DayStart
	historyStart := dayStart.Add(-historyHours * time.Hour)
	pvByOffset := bucketByOffsetFromStart(filterMetric(in.HistoryDeltaPoints, "accumulated_pv_energy_yield_kwh"), historyStart, historyHours)
	importByOffset := bucketByOffsetFromStart(filterMetric(in.HistoryDeltaPoints, "accumulated_electricity_purchased_kwh"), historyStart, historyHours)
	exportByOffset := bucketByOffsetFromStart(filterMetric(in.HistoryDeltaPoints, "accumulated_electricity_sold_kwh"), historyStart, historyHours)

	loc := dayStart.Location()
	yesterdayDate := dayStart.AddDate(0, 0, -1).In(loc).Format("2006-01-02")
	dayBeforeDate := dayStart.AddDate(0, 0, -2).In(loc).Format("2006-01-02")

	var dy2, dy1 []DAMHour
	for _, p := range in.DamHistory {
		key := p.DeliveryDate.Format("2006-01-02")
		if key == dayBeforeDate {
			dy2 = append(dy2, p)
		} else if key == yesterdayDate {
			dy1 = append(dy1, p)
		}
	}
	dy2PriceMap := buildPriceMap(dy2)
	dy1PriceMap := buildPriceMap(dy1)

	out := make([]hourHistoryRecord, 0, historyHours)
	for i := 0; i < historyHours; i++ {
		var flowRow *FlowRow
		var rdn *float64
		if i < 24 {
			if i < len(in.DayBeforeFlows) {
				fr := in.DayBeforeFlows[i]
				flowRow = &fr
			}
			if v, ok := dy2PriceMap[i]; ok {
				vv := v
				rdn = &vv
			}
		} else {
			yh := i - 24
			if yh < len(in.YesterdayFlows) {
				fr := in.YesterdayFlows[yh]
				flowRow = &fr
			}
			if v, ok := dy1PriceMap[yh]; ok {
				vv := v
				rdn = &vv
			}
		}
		var flow HourFlows
		if flowRow != nil {
			flow = HourFlows{
				PV:            pvByOffset[i],
				GridImport:    importByOffset[i],
				GridExport:    exportByOffset[i],
				EssCharged:    flowRow.EssCharged,
				EssDischarged: flowRow.EssDischarged,
				PVToEss:       flowRow.PVToEss,
				GridToEss:     flowRow.GridToEss,
				EssToLoad:     flowRow.EssToLoad,
				EssToGrid:     flowRow.EssToGrid,
			}
		}
		var soc *float64
		socOffset := i - historyHours
		if v, ok := socByOffset[socOffset]; ok && !math.IsInf(v, 0) && !math.IsNaN(v) {
			vv := v
			soc = &vv
		}
		out = append(out, hourHistoryRecord{flow: flow, rdnUahPerKwh: rdn, socPercentStart: soc})
	}
	return out
}

// findAnchorAndPreRoll scans history right-to-left for the most recent
// hour with start-of-hour SOC <= socResetThresholdPercent, seeds the WAC
// ledger there (residual at zero cost), and rolls forward to today's
// 00:00. Port of TS `findAnchorAndPreRoll`.
func findAnchorAndPreRoll(history []hourHistoryRecord, todayHour0SocPercent *float64, t Tariffs) EssState {
	if todayHour0SocPercent != nil && !math.IsInf(*todayHour0SocPercent, 0) && !math.IsNaN(*todayHour0SocPercent) &&
		*todayHour0SocPercent <= socResetThresholdPercent {
		kwh := usableKwhFromSOC(*todayHour0SocPercent, t.EssCapacityKwh)
		return EssState{Kwh: kwh, Uah: 0}
	}

	anchorIdx := -1
	for i := len(history) - 1; i >= 0; i-- {
		soc := history[i].socPercentStart
		if soc != nil && *soc <= socResetThresholdPercent {
			anchorIdx = i
			break
		}
	}
	if anchorIdx < 0 {
		for i := 0; i < len(history); i++ {
			if history[i].socPercentStart != nil {
				anchorIdx = i
				break
			}
		}
	}
	if anchorIdx < 0 {
		return EssState{}
	}

	anchorSoc := *history[anchorIdx].socPercentStart
	state := EssState{Kwh: usableKwhFromSOC(anchorSoc, t.EssCapacityKwh), Uah: 0}
	for i := anchorIdx + 1; i < len(history); i++ {
		rec := history[i]
		if rec.rdnUahPerKwh == nil {
			continue
		}
		econ := HourEconomicsFor(*rec.rdnUahPerKwh, rec.flow, t)
		result := RollHour(state, rec.flow, econ.ImportPrice, econ.ExportPrice, t.DegradationUahPerKwh)
		state = result.Next
	}
	return state
}

// --- bucketing helpers (ports of the TS helpers) ---

func filterMetric(points []Point, key string) []Point {
	out := make([]Point, 0, len(points))
	for _, p := range points {
		if p.MetricKey == key {
			out = append(out, p)
		}
	}
	return out
}

// bucketByHourOfDay folds today's delta points into hour-of-day 0..23.
func bucketByHourOfDay(points []Point, dayStart time.Time) map[int]float64 {
	base := dayStart.UnixNano()
	out := make(map[int]float64)
	for _, p := range points {
		if p.Time.IsZero() {
			continue
		}
		offset := int(math.Floor(float64(p.Time.UnixNano()-base) / float64(time.Hour)))
		if offset < 0 || offset >= 24 {
			continue
		}
		out[offset] += p.Value
	}
	return out
}

// bucketSocByOffset maps each SOC sample to the hour OFFSET (relative to
// today's 00:00) it represents the start of. base is dayStart − 1h.
func bucketSocByOffset(points []Point, dayStart time.Time) map[int]float64 {
	base := dayStart.Add(-time.Hour).UnixNano()
	out := make(map[int]float64)
	for _, p := range points {
		if p.Time.IsZero() {
			continue
		}
		offset := int(math.Floor(float64(p.Time.UnixNano()-base) / float64(time.Hour)))
		out[offset] = p.Value
	}
	return out
}

// bucketByOffsetFromStart folds points into offset 0..windowHours from
// windowStart.
func bucketByOffsetFromStart(points []Point, windowStart time.Time, windowHours int) map[int]float64 {
	base := windowStart.UnixNano()
	out := make(map[int]float64)
	for _, p := range points {
		if p.Time.IsZero() {
			continue
		}
		offset := int(math.Floor(float64(p.Time.UnixNano()-base) / float64(time.Hour)))
		if offset < 0 || offset >= windowHours {
			continue
		}
		out[offset] += p.Value
	}
	return out
}

// buildPriceMap unpacks DAM rows into a 0-indexed hour → UAH/kWh map
// (zone == DAMZone, price /1000, hour-1). Port of TS `buildPriceMap`.
func buildPriceMap(dam []DAMHour) map[int]float64 {
	out := make(map[int]float64)
	for _, p := range dam {
		if p.Zone != DAMZone {
			continue
		}
		if p.PriceUAHPerMWh == nil {
			continue
		}
		idx := p.Hour - 1
		if idx < 0 || idx >= 24 {
			continue
		}
		out[idx] = *p.PriceUAHPerMWh / 1000
	}
	return out
}
