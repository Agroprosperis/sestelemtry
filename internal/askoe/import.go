package askoe

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/storage"
)

const insertBatchSize = 5000

// ImportResult is the JSON summary returned to the operator.
type ImportResult struct {
	OrganizationID        string         `json:"organization_id"`
	From                  string         `json:"from,omitempty"`
	To                    string         `json:"to,omitempty"`
	FilesRead             int            `json:"files_read"`
	DaysComplete          int            `json:"days_complete"`
	DaysWritten           int            `json:"days_written"`
	DaysSkippedOccupied   int            `json:"days_skipped_occupied"`
	DaysSkippedIncomplete int            `json:"days_skipped_incomplete"`
	RowsWritten           int            `json:"rows_written"`
	DeletedRows           int64          `json:"deleted_rows"`
	PerMetric             map[string]int `json:"per_metric"`
	EconomicsDaysOK       int            `json:"economics_days_ok,omitempty"`
	EconomicsDaysFailed   int            `json:"economics_days_failed,omitempty"`
	Warnings              []string       `json:"warnings,omitempty"`
}

// Importer writes ASKOE hourly meters into telemetry_samples.
type Importer struct {
	pool       *pgxpool.Pool
	log        *slog.Logger
	hostsByOrg map[string]OrgHosts
}

func NewImporter(pool *pgxpool.Pool, log *slog.Logger, hostsByOrg map[string]OrgHosts) *Importer {
	if log == nil {
		log = slog.Default()
	}
	return &Importer{pool: pool, log: log, hostsByOrg: hostsByOrg}
}

// Import parses the workbooks, writes 5-minute cumulative counters for
// every complete day that does not already hold live or FusionSolar
// data, and never deletes those preferred sources. Previously imported
// ASKOE rows in a rewritten day are replaced (source=askoe only).
func (im *Importer) Import(ctx context.Context, orgID string, files []WorkbookFile, onProgress func(done, total int, label string)) (*ImportResult, error) {
	if orgID == "" {
		return nil, fmt.Errorf("askoe: organization_id is required")
	}
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		loc = time.UTC
	}
	grid, warnings, err := ParseWorkbooks(files)
	if err != nil {
		return nil, err
	}
	complete := grid.CompleteDays()
	incomplete := uniqueDayCount(grid) - len(complete)
	result := &ImportResult{
		OrganizationID:        orgID,
		FilesRead:             len(files),
		DaysComplete:          len(complete),
		DaysSkippedIncomplete: incomplete,
		PerMetric:             map[string]int{},
		Warnings:              warnings,
	}
	if len(complete) == 0 {
		result.Warnings = append(result.Warnings, "no complete days (need A+ РУ-10, A− РУ-10 and A− СЕС for the same date)")
		return result, nil
	}
	result.From = complete[0].String()
	result.To = complete[len(complete)-1].String()

	hosts := im.hostsByOrg[orgID]
	metricKeys := ImportableMetricKeys()
	var seed Counters

	for i, day := range complete {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if onProgress != nil {
			onProgress(i, len(complete), day.String())
		}
		dayStart := day.Time(loc)
		dayEnd := dayStart.AddDate(0, 0, 1)
		occupied, err := storage.HasLiveSamplesInRange(ctx, im.pool, orgID, SourceValue, dayStart, dayEnd)
		if err != nil {
			return nil, fmt.Errorf("askoe: occupied check %s: %w", day, err)
		}
		if occupied {
			result.DaysSkippedOccupied++
			continue
		}
		samples := BuildDaySamples(orgID, hosts, loc, day, grid, seed)
		samples = append(SeedAtMidnight(orgID, hosts, dayStart, seed), samples...)
		deleted, err := storage.DeleteArchiveSamplesInRange(ctx, im.pool, orgID, metricKeys, SourceValue, dayStart, dayEnd)
		if err != nil {
			return nil, fmt.Errorf("askoe: clear %s: %w", day, err)
		}
		result.DeletedRows += deleted
		for start := 0; start < len(samples); start += insertBatchSize {
			end := start + insertBatchSize
			if end > len(samples) {
				end = len(samples)
			}
			if err := storage.InsertSamples(ctx, im.pool, samples[start:end]); err != nil {
				return nil, fmt.Errorf("askoe: insert %s: %w", day, err)
			}
		}
		result.RowsWritten += len(samples)
		result.DaysWritten++
		for _, s := range samples {
			result.PerMetric[s.MetricKey]++
		}
		seed = EndCounters(samples)
	}
	if onProgress != nil {
		onProgress(len(complete), len(complete), "телеметрія")
	}
	if result.DaysWritten == 0 {
		result.Warnings = append(result.Warnings, "every complete day already has live or FusionSolar data — nothing imported")
	}
	im.log.Info("askoe_import_ok",
		"organization_id", orgID,
		"days_written", result.DaysWritten,
		"days_skipped_occupied", result.DaysSkippedOccupied,
		"rows_written", result.RowsWritten,
	)
	return result, nil
}

func uniqueDayCount(g HourGrid) int {
	seen := map[civilDay]struct{}{}
	for d := range g.Import {
		seen[d] = struct{}{}
	}
	for d := range g.Export {
		seen[d] = struct{}{}
	}
	for d := range g.PV {
		seen[d] = struct{}{}
	}
	return len(seen)
}
