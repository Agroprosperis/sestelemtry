package fusionsolar

import (
	"context"
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

// ArchiveCutoff is the hard upper bound for archive imports: real live
// telemetry runs from this instant onward (2026-05-01 inclusive), so
// the importer refuses any window that would write or delete at or
// after it. The import range is half-open [from, to), so a `to` equal
// to the cutoff is allowed (it covers up to but excluding the cutoff);
// anything past it is rejected.
var ArchiveCutoff = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

// ErrAfterCutoff is returned (and wrapped) when an import would reach
// into the live-data region. The HTTP handler maps it to 400 so the
// operator sees a clear validation error rather than an upstream 502.
var ErrAfterCutoff = errors.New("fusionsolar: import window reaches live data on/after the archive cutoff")

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
	Warnings       []string       `json:"warnings,omitempty"`
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
}

// NewImporter wires a ready-to-use importer. `hostByOrg` maps an org id
// to the Modbus host label the live collector stamps on its samples;
// the importer copies it onto `labels.device_host` so the energy-flow
// allocator classifies archive rows exactly as it would live data.
func NewImporter(pool *pgxpool.Pool, log *slog.Logger, hostByOrg map[string]string) *Importer {
	if log == nil {
		log = slog.Default()
	}
	return &Importer{pool: pool, log: log, hostByOrg: hostByOrg}
}

// Import backfills [from, to) for one organization using the supplied
// client (carrying the per-request access token / API base): it loops
// 24h device windows, normalizes the cumulative SmartLogger / ESS
// counters into metric_keys, deletes any previously-imported rows in
// the window (idempotency), and bulk-inserts the fresh samples.
func (im *Importer) Import(ctx context.Context, client *Client, orgID string, from, to time.Time) (*ImportResult, error) {
	if client == nil {
		return nil, fmt.Errorf("fusionsolar: missing API client")
	}
	if !to.After(from) {
		return nil, fmt.Errorf("fusionsolar: to must be after from")
	}
	from = from.UTC()
	to = to.UTC()
	// Safety guard: never write or delete in the live-data region.
	// Half-open [from, to) means to == ArchiveCutoff is fine.
	if to.After(ArchiveCutoff) {
		return nil, fmt.Errorf("%w: to=%s cutoff=%s", ErrAfterCutoff, to.Format(time.RFC3339), ArchiveCutoff.Format(time.RFC3339))
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

	for windowStart := from; windowStart.Before(to); windowStart = windowStart.Add(maxHistoryWindow) {
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
	)
	return result, nil
}
