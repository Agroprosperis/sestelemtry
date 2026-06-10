package fusionsolar

import (
	"context"
	"encoding/json"
	"errors"
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

// ErrAfterCutoff is returned (and wrapped) when an import would reach
// into the live-data region: `to` is after the organization's live-data
// start instant. The import range is half-open [from, to), so a `to`
// equal to the cutoff is allowed (it covers up to but excluding the
// cutoff); anything past it is rejected. The HTTP handler maps it to
// 400 so the operator sees a clear validation error rather than an
// upstream 502.
var ErrAfterCutoff = errors.New("fusionsolar: import window reaches live data on/after the organization's live-data start")

// ErrNoCutoff is returned when the organization has no live-data start
// date configured. Per policy the importer refuses to run rather than
// risk overwriting live telemetry, so the site's go-live date must be
// configured (live_data_start in the org YAML) before any archive
// import is allowed.
var ErrNoCutoff = errors.New("fusionsolar: no live-data start date configured for organization")

// ErrBeforeStart is returned (and wrapped) when an import window starts
// before the organization's operation-start date (the earliest date
// with any data). The HTTP handler maps it to 400.
var ErrBeforeStart = errors.New("fusionsolar: import window starts before the organization's operation-start date")

// ArchiveBounds is the per-organization importable date range for the
// FusionSolar archive backfill. Cutoff is the live-data start (the
// exclusive upper bound: a window may reach up to but not past it);
// a zero Cutoff means archive import is disabled for the org. Start is
// the operation-start (the inclusive lower bound: a window may not
// begin before it); a zero Start means no lower bound is enforced.
type ArchiveBounds struct {
	Start  time.Time
	Cutoff time.Time
}

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
	PerMetric      map[string]int `json:"per_metric"`
	KpiDaysWritten int            `json:"kpi_days_written"`
	Warnings       []string       `json:"warnings,omitempty"`
}

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
	pool      *pgxpool.Pool
	log       *slog.Logger
	hostByOrg map[string]string
	// boundsByOrg maps an org id to its importable archive date range
	// (operation-start lower bound + live-data-start upper bound). An
	// org with no entry (or a zero Cutoff) has archive import disabled
	// (ErrNoCutoff).
	boundsByOrg map[string]ArchiveBounds
}

// NewImporter wires a ready-to-use importer. `hostByOrg` maps an org id
// to the Modbus host label the live collector stamps on its samples;
// the importer copies it onto `labels.device_host` so the energy-flow
// allocator classifies archive rows exactly as it would live data.
// `boundsByOrg` maps an org id to its importable archive date range
// (operation-start .. live-data-start); orgs missing from the map (or
// with a zero Cutoff) are refused (ErrNoCutoff) so live data is never
// overwritten.
func NewImporter(pool *pgxpool.Pool, log *slog.Logger, hostByOrg map[string]string, boundsByOrg map[string]ArchiveBounds) *Importer {
	if log == nil {
		log = slog.Default()
	}
	return &Importer{pool: pool, log: log, hostByOrg: hostByOrg, boundsByOrg: boundsByOrg}
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
	// Safety guard: never write or delete in the live-data region. The
	// boundary is per-organization (its live_data_start); an org with no
	// configured date is refused outright. Half-open [from, to) means
	// to == cutoff is fine. The optional operation_start lower bound
	// rejects windows that begin before the site had any data.
	bounds, ok := im.boundsByOrg[orgID]
	if !ok || bounds.Cutoff.IsZero() {
		return nil, fmt.Errorf("%w: %q", ErrNoCutoff, orgID)
	}
	if to.After(bounds.Cutoff) {
		return nil, fmt.Errorf("%w: to=%s cutoff=%s", ErrAfterCutoff, to.Format(time.RFC3339), bounds.Cutoff.Format(time.RFC3339))
	}
	if !bounds.Start.IsZero() && from.Before(bounds.Start) {
		return nil, fmt.Errorf("%w: from=%s start=%s", ErrBeforeStart, from.Format(time.RFC3339), bounds.Start.Format(time.RFC3339))
	}
	topo, ok := Topology[orgID]
	if !ok {
		return nil, fmt.Errorf("fusionsolar: no plant topology for organization %q", orgID)
	}

	host := im.hostByOrg[orgID]
	result := &ImportResult{
		OrganizationID: orgID,
		PlantCode:      topo.PlantCode,
		From:           from,
		To:             to,
		PerMetric:      map[string]int{},
	}

	acc := newSampleAccumulator(orgID, host, topo)

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

	samples := acc.samples()
	for _, s := range samples {
		result.PerMetric[s.MetricKey]++
	}

	if len(samples) == 0 {
		result.Warnings = append(result.Warnings, "no samples returned for the requested window")
		return result, nil
	}

	// Idempotency: drop only previously-imported ARCHIVE rows in the
	// window (labels.source = "fusionsolar") before writing the fresh
	// batch. Live Modbus samples carry no source label, so they are
	// never deleted — a re-import rewrites strictly its own rows.
	deleted, err := storage.DeleteArchiveSamplesInRange(ctx, im.pool, orgID, ImportableMetricKeys(), SourceValue, from, to)
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: clear existing archive range: %w", err)
	}
	result.DeletedRows = deleted

	for start := 0; start < len(samples); start += insertBatchSize {
		end := start + insertBatchSize
		if end > len(samples) {
			end = len(samples)
		}
		if err := storage.InsertSamples(ctx, im.pool, samples[start:end]); err != nil {
			return nil, fmt.Errorf("fusionsolar: insert samples: %w", err)
		}
	}
	result.RowsWritten = len(samples)

	im.log.Info("fusionsolar_import_ok",
		"organization_id", orgID,
		"plant_code", topo.PlantCode,
		"from", from.Format(time.RFC3339),
		"to", to.Format(time.RFC3339),
		"windows", result.Windows,
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
