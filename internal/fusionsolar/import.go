package fusionsolar

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/storage"
)

// insertBatchSize bounds how many samples we hand to a single CopyFrom
// so a multi-day backfill streams into Postgres in chunks rather than
// buffering an entire year of rows before the first write.
const insertBatchSize = 5000

// ImportResult is the structured summary returned to the operator after
// a backfill run. It is JSON-tagged so the HTTP handler can hand it
// straight to the response.
type ImportResult struct {
	OrganizationID string         `json:"organization_id"`
	PlantCode      string         `json:"plant_code"`
	From           time.Time      `json:"from"`
	To             time.Time      `json:"to"`
	Windows        int            `json:"windows"`
	RowsWritten    int            `json:"rows_written"`
	DeletedRows    int64          `json:"deleted_rows"`
	// SkippedLiveWindows counts 24h windows the importer skipped wholesale
	// because every 5-minute slot already held real (live) data — those
	// days are left untouched so live telemetry is never overwritten.
	SkippedLiveWindows int `json:"skipped_live_windows"`
	// SkippedLiveSamples counts individual archive samples dropped because
	// their own 5-minute slot already held live data. This is non-zero on
	// transition days where live collection started mid-day: the empty
	// slots are filled from archive while the live slots are left intact.
	SkippedLiveSamples int            `json:"skipped_live_samples"`
	PerMetric          map[string]int `json:"per_metric"`
	KpiDaysWritten     int            `json:"kpi_days_written"`
	Warnings           []string       `json:"warnings,omitempty"`
}

// liveSlotSeconds is the granularity at which live-data protection resolves
// collisions. Archive is 5-minute data and the dashboard buckets at 5
// minutes, so an archive sample is dropped only when its own 5-minute slot
// already holds live data — every empty slot stays fillable.
const liveSlotSeconds = 300

// kpiKyiv is the timezone used to bucket getKpiStationDay rows into
// civil days, matching the economics dashboard's Europe/Kyiv day grid.
var kpiKyiv = mustLoadKyiv()

func mustLoadKyiv() *time.Location {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return time.UTC
	}
	return loc
}

// Importer writes FusionSolar device history into telemetry_samples.
// It holds only the DB pool / logger / host map; the API client (and
// therefore the access token + API base) is supplied per Import call so
// the operator can enter connection details on the import page rather
// than baking secrets into the environment.
type Importer struct {
	pool       *pgxpool.Pool
	log        *slog.Logger
	hostsByOrg map[string]OrgHosts
}

// NewImporter wires a ready-to-use importer. `hostsByOrg` maps an org id
// to the per-metric Modbus host labels the live collector stamps on its
// samples; the importer copies them onto `labels.device_host` so the
// energy-flow allocator classifies archive rows exactly as it would live
// data — including dual-SmartLogger sites where PV/grid/load and ESS
// counters live on different hosts.
//
// There is no date guard: instead of a configured cutoff, the importer
// checks per 24h window whether real (live) telemetry already exists and
// skips those windows, so a backfill can never overwrite live data.
func NewImporter(pool *pgxpool.Pool, log *slog.Logger, hostsByOrg map[string]OrgHosts) *Importer {
	if log == nil {
		log = slog.Default()
	}
	return &Importer{pool: pool, log: log, hostsByOrg: hostsByOrg}
}

// Import backfills [from, to) for one organization using the supplied
// client (carrying the per-request access token / API base): it loops
// 24h device windows, normalizes the cumulative SmartLogger / ESS
// counters into metric_keys, deletes any previously-imported rows in
// the window (idempotency), and bulk-inserts the fresh samples.
// onProgress, when non-nil, is invoked after each 24h window is fetched
// with (windowsDone, windowsTotal) so the HTTP handler can stream a
// progress feed to the operator during a long (e.g. year-long) backfill.
func (im *Importer) Import(ctx context.Context, client *Client, orgID string, from, to time.Time, onProgress func(done, total int)) (*ImportResult, error) {
	if client == nil {
		return nil, fmt.Errorf("fusionsolar: missing API client")
	}
	if !to.After(from) {
		return nil, fmt.Errorf("fusionsolar: to must be after from")
	}
	from = from.UTC()
	to = to.UTC()
	topo, ok := Topology[orgID]
	if !ok {
		return nil, fmt.Errorf("fusionsolar: no plant topology for organization %q", orgID)
	}

	hosts := im.hostsByOrg[orgID]
	result := &ImportResult{
		OrganizationID: orgID,
		PlantCode:      topo.PlantCode,
		From:           from,
		To:             to,
		PerMetric:      map[string]int{},
	}

	metricKeys := ImportableMetricKeys()

	// Total 24h windows the loop will cover, for the progress feed.
	totalWindows := int(to.Sub(from) / maxHistoryWindow)
	if to.Sub(from)%maxHistoryWindow != 0 {
		totalWindows++
	}

	for windowStart := from; windowStart.Before(to); windowStart = windowStart.Add(maxHistoryWindow) {
		// Honour cancellation between windows so an operator's "cancel"
		// (client disconnect → ctx cancel) stops promptly even if the
		// next upstream call would otherwise be quick.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		windowEnd := windowStart.Add(maxHistoryWindow)
		if windowEnd.After(to) {
			windowEnd = to
		}
		result.Windows++

		// Live-data protection at 5-minute granularity (replaces the old
		// whole-window skip): collect the slots in this window that already
		// hold real (non-archive) telemetry. Archive is written only into
		// the slots NOT in this set, so live data is never overwritten or
		// interleaved within a slot — but the empty slots of a partially
		// live day (e.g. the transition day when live collection started
		// mid-day) are still filled from archive, instead of discarding the
		// whole day's archive as before.
		liveSlots, err := storage.LiveSampleBucketsInRange(ctx, im.pool, orgID, SourceValue, windowStart, windowEnd, liveSlotSeconds)
		if err != nil {
			return nil, fmt.Errorf("fusionsolar: scan live slots [%s..%s]: %w", windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339), err)
		}
		// Fast path: when every 5-minute slot in the window already has
		// live data there is nothing to fill, so skip the upstream fetch
		// entirely (keeps a backfill that overlaps the live period cheap).
		windowSlots := int(windowEnd.Sub(windowStart) / (time.Duration(liveSlotSeconds) * time.Second))
		if windowSlots > 0 && len(liveSlots) >= windowSlots {
			result.SkippedLiveWindows++
			if onProgress != nil {
				onProgress(result.Windows, totalWindows)
			}
			continue
		}

		// A fresh accumulator per window keeps each day's archive rows
		// independent. The samples are absolute cumulative readings (not
		// cross-window deltas), so there is no continuity to preserve
		// between windows.
		acc := newSampleAccumulator(orgID, hosts, topo)

		// 1. Primary SmartLogger: PV / load / grid (+ ESS counters on
		//    single-logger sites).
		loggerSamples, err := client.DeviceHistory(ctx, topo.Logger.DevDn, topo.Logger.DevTypeID, windowStart, windowEnd)
		if err != nil {
			return nil, fmt.Errorf("fusionsolar: logger %s [%s..%s]: %w", topo.Logger.DevDn, windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339), err)
		}
		acc.addLogger(loggerSamples, windowStart, windowEnd)

		// 2. Dedicated ESS SmartLogger (dual-logger sites only):
		//    total_charge / total_discharge with AC fallback.
		if topo.EssLogger != nil {
			essLogger, err := client.DeviceHistory(ctx, topo.EssLogger.DevDn, topo.EssLogger.DevTypeID, windowStart, windowEnd)
			if err != nil {
				return nil, fmt.Errorf("fusionsolar: ess logger %s: %w", topo.EssLogger.DevDn, err)
			}
			acc.addEssLogger(essLogger, windowStart, windowEnd)
		}

		// 3. ESS battery packs: average battery_soc per timestamp.
		for _, ess := range topo.EssDevices {
			essSamples, err := client.DeviceHistory(ctx, ess.DevDn, ess.DevTypeID, windowStart, windowEnd)
			if err != nil {
				// SOC is diagnostic, not load-bearing for economics:
				// degrade gracefully so one offline pack doesn't abort
				// the whole backfill.
				im.log.Warn("fusionsolar_ess_history_failed", "organization_id", orgID, "dev_dn", ess.DevDn, "err", err)
				result.Warnings = append(result.Warnings, fmt.Sprintf("ESS %s history failed: %v", ess.DevDn, err))
				continue
			}
			acc.addEssDevice(essSamples, windowStart, windowEnd)
		}

		windowSamples := acc.samples()

		// Slot-level live-data protection: drop any archive sample whose
		// 5-minute slot already holds live data, so a transition day keeps
		// its live slots and gets archive only for the empty ones. Truncate
		// matches the UTC, epoch-aligned 5-minute boundaries time_bucket
		// uses in LiveSampleBucketsInRange.
		if len(liveSlots) > 0 {
			slot := time.Duration(liveSlotSeconds) * time.Second
			kept := windowSamples[:0]
			for _, s := range windowSamples {
				key := s.Time.UTC().Truncate(slot).UnixMilli()
				if _, live := liveSlots[key]; live {
					result.SkippedLiveSamples++
					continue
				}
				kept = append(kept, s)
			}
			windowSamples = kept
		}

		for _, s := range windowSamples {
			result.PerMetric[s.MetricKey]++
		}

		if len(windowSamples) > 0 {
			// Idempotency: drop only previously-imported ARCHIVE rows in
			// this window (labels.source = "fusionsolar") before writing
			// the fresh batch. Live Modbus samples carry no source label,
			// so they are never deleted — and we only get here for days
			// without live data anyway.
			deleted, err := storage.DeleteArchiveSamplesInRange(ctx, im.pool, orgID, metricKeys, SourceValue, windowStart, windowEnd)
			if err != nil {
				return nil, fmt.Errorf("fusionsolar: clear existing archive range: %w", err)
			}
			result.DeletedRows += deleted
			// FusionSolar outranks ASKOE: drop commercial-meter rows in
			// this window so the two archives never share a slot.
			extra, err := storage.DeleteArchiveSamplesInRange(ctx, im.pool, orgID, metricKeys, "askoe", windowStart, windowEnd)
			if err != nil {
				return nil, fmt.Errorf("fusionsolar: clear askoe range: %w", err)
			}
			result.DeletedRows += extra

			for start := 0; start < len(windowSamples); start += insertBatchSize {
				end := start + insertBatchSize
				if end > len(windowSamples) {
					end = len(windowSamples)
				}
				if err := storage.InsertSamples(ctx, im.pool, windowSamples[start:end]); err != nil {
					return nil, fmt.Errorf("fusionsolar: insert samples: %w", err)
				}
			}
			result.RowsWritten += len(windowSamples)
		}

		if onProgress != nil {
			onProgress(result.Windows, totalWindows)
		}
	}

	// Canonical daily KPIs (getKpiStationDay) for reconciliation. Best
	// effort: a failure here (e.g. the /thirdData endpoint rejecting the
	// OAuth bearer) must never abort the 5-min telemetry import.
	if kpiDays, err := im.fetchDailyKpi(ctx, client, orgID, topo.PlantCode, from, to); err != nil {
		im.log.Warn("fusionsolar_kpi_fetch_failed", "organization_id", orgID, "err", err)
		result.Warnings = append(result.Warnings, fmt.Sprintf("daily KPI reconciliation unavailable: %v", err))
	} else {
		result.KpiDaysWritten = kpiDays
	}

	if result.RowsWritten == 0 {
		switch {
		case result.SkippedLiveWindows == result.Windows && result.Windows > 0:
			result.Warnings = append(result.Warnings, "all days in the requested window already have live data — nothing imported")
		case result.SkippedLiveSamples > 0:
			result.Warnings = append(result.Warnings, "every returned sample fell on a 5-minute slot that already has live data — nothing imported")
		default:
			result.Warnings = append(result.Warnings, "no samples returned for the requested window")
		}
	}

	im.log.Info("fusionsolar_import_ok",
		"organization_id", orgID,
		"plant_code", topo.PlantCode,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"windows", result.Windows,
		"skipped_live_windows", result.SkippedLiveWindows,
		"skipped_live_samples", result.SkippedLiveSamples,
		"rows_written", result.RowsWritten,
		"deleted_rows", result.DeletedRows,
		"kpi_days_written", result.KpiDaysWritten,
	)
	return result, nil
}

// fetchDailyKpi pulls getKpiStationDay for each calendar month spanning
// [from, to) and upserts the canonical daily totals (keyed by Europe/Kyiv
// civil day) used to reconcile economics. Only days inside the import
// window are kept. Returns the number of day rows written.
func (im *Importer) fetchDailyKpi(ctx context.Context, client *Client, orgID, plantCode string, from, to time.Time) (int, error) {
	// Floor `from` to the first day of its month (UTC) and iterate
	// month by month; one call returns the whole month.
	monthStart := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	var collected []storage.FusionDailyKpiRow
	seen := map[string]bool{}
	for m := monthStart; m.Before(to); m = m.AddDate(0, 1, 0) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		days, err := client.StationDaily(ctx, plantCode, m, kpiKyiv)
		if err != nil {
			return 0, err
		}
		for _, d := range days {
			// Keep only days within the imported window (half-open).
			if d.Day.Before(dayFloorUTC(from)) || !d.Day.Before(to) {
				continue
			}
			key := d.Day.Format("2006-01-02")
			if seen[key] {
				continue
			}
			seen[key] = true
			raw, _ := json.Marshal(d)
			collected = append(collected, storage.FusionDailyKpiRow{
				OrganizationID: orgID,
				Day:            d.Day,
				PlantCode:      plantCode,
				PVYield:        d.PVYield,
				UsePower:       d.UsePower,
				BuyPower:       d.BuyPower,
				OnGridPower:    d.OnGridPower,
				ChargeCap:      d.ChargeCap,
				DischargeCap:   d.DischargeCap,
				SelfUsePower:   d.SelfUsePower,
				Raw:            raw,
			})
		}
	}
	if len(collected) == 0 {
		return 0, nil
	}
	return storage.UpsertFusionDailyKpi(ctx, im.pool, collected)
}

// dayFloorUTC returns the UTC midnight of t's calendar day.
func dayFloorUTC(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}
