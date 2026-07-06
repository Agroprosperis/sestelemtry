package economics

import (
	"context"
	"fmt"
	"time"
)

// pvGridMetricKeys are the accumulator deltas the assembly needs for
// pv / grid-import / grid-export (the four ESS directional flows come
// from HourlyFlows instead).
var pvGridMetricKeys = []string{
	"accumulated_pv_energy_yield_kwh",
	"accumulated_electricity_purchased_kwh",
	"accumulated_electricity_sold_kwh",
}

const socMetricKey = "soc_percent"

// DefaultTariffs mirrors the frontend DEFAULT_TARIFFS — the fallback
// used when an org has no tariff schedule yet.
var DefaultTariffs = Tariffs{
	DistributionUahPerKwh:   2.75218,
	TransmissionUahPerKwh:   0.74291,
	SupplierMarginUahPerKwh: 0,
	OtherFeesUahPerKwh:      0,
	ExportDiscount:          0.05,
	DegradationUahPerKwh:    0.6,
	IncludeVat:              false,
	VatRate:                 0.2,
	EssCapacityKwh:          215,
}

// StoredDay is one day's computed (and persisted) economics: the 24
// hourly rows plus the daily summary and finality flag.
type StoredDay struct {
	OrganizationID string
	Day            time.Time // local midnight
	Tz             string
	Rows           []*HourRow
	Totals         DailyTotals
	IsFinal        bool
	// ComputedAt is when the persisted row was last written. Zero for a
	// freshly computed (not-yet-reloaded) day. GetDay uses it to decide
	// whether a cached non-final day is recent enough to serve.
	ComputedAt time.Time
}

// Backend is the data + persistence surface the Service depends on. The
// api package implements it (reusing the energy-flow allocator, the
// timeseries / DAM store queries, and the new economics storage funcs)
// so the economics package stays free of pgx / HTTP wiring.
type Backend interface {
	// HourlyFlows returns up to 24 directional-flow rows for the day
	// starting at dayStart (its location is the request tz).
	HourlyFlows(ctx context.Context, orgID string, dayStart time.Time) ([]FlowRow, error)
	// Timeseries returns bucketed points for the metric keys over
	// [from, to) with the given bucket / tz / aggregation.
	Timeseries(ctx context.Context, orgID string, metricKeys []string, from, to time.Time, bucket, tz, aggregation string) ([]Point, error)
	// DAMPrices returns hourly DAM rows for the zone over the inclusive
	// delivery-date range [from, to].
	DAMPrices(ctx context.Context, zone int, from, to time.Time) ([]DAMHour, error)
	// TariffSchedule returns the org's date-versioned tariff schedule.
	TariffSchedule(ctx context.Context, orgID string) (Schedule, error)
	// CanonicalDaily returns the canonical FusionSolar daily KPIs for
	// the day (local midnight). The bool is false when none are stored
	// (then the day is computed without reconciliation).
	CanonicalDaily(ctx context.Context, orgID string, day time.Time) (CanonicalDaily, bool, error)
	// SaveDay persists a computed day (hourly rows + daily summary).
	SaveDay(ctx context.Context, day StoredDay) error
	// LoadDay returns a previously-persisted day. The bool is false
	// when nothing is stored yet.
	LoadDay(ctx context.Context, orgID string, day time.Time, tz string) (StoredDay, bool, error)
	// LoadDailyRange returns the persisted per-day summaries for the
	// inclusive civil-date span [from, to]. Used by the monthly rollup.
	LoadDailyRange(ctx context.Context, orgID string, from, to time.Time) ([]DailyRecord, error)
	// LoadHourlyRange returns the slim per-hour records for the half-open
	// window [from, to) — the RDN stats and ESS marginality heatmap.
	LoadHourlyRange(ctx context.Context, orgID string, from, to time.Time) ([]HourlyRecord, error)
	// SumEbitdaBefore returns the cumulative EBITDA of every stored day
	// strictly before `before` plus whether any such day exists. It backs
	// the annual ROI opening balance ("залишок з початку експлуатації").
	SumEbitdaBefore(ctx context.Context, orgID string, before time.Time) (sum float64, hasData bool, err error)
}

// Service computes and persists economics, and serves them read-through
// (final days from cache, non-final days recomputed on read).
type Service struct {
	backend Backend
	// freshTodayWindow, when > 0, lets GetDay serve a cached non-final
	// day (i.e. today) from storage instead of recomputing it live, as
	// long as it was written within this window. It is set when the
	// economics-recompute daemon keeps the current day warm, so the
	// dashboard reads the cache instead of paying the slow live
	// recompute on every request. 0 (the default) preserves the
	// always-recompute-non-final behaviour.
	freshTodayWindow time.Duration
}

// NewService wires the economics service to a Backend.
func NewService(backend Backend) *Service {
	return &Service{backend: backend}
}

// SetFreshTodayWindow enables serving cached non-final days that were
// written within d. Pass 0 to disable (always recompute non-final days).
func (s *Service) SetFreshTodayWindow(d time.Duration) {
	if d < 0 {
		d = 0
	}
	s.freshTodayWindow = d
}

// ComputeDay computes economics for one calendar day, persists the
// result, and returns it. date is YYYY-MM-DD in tz.
func (s *Service) ComputeDay(ctx context.Context, orgID, date, tz string) (StoredDay, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return StoredDay{}, err
	}
	parsed, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return StoredDay{}, fmt.Errorf("date must be YYYY-MM-DD")
	}
	dayStart := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	schedule, err := s.backend.TariffSchedule(ctx, orgID)
	if err != nil {
		return StoredDay{}, fmt.Errorf("tariff schedule: %w", err)
	}
	tariffs, ok := schedule.ResolveForDay(dayStart)
	if !ok {
		tariffs = DefaultTariffs
	}

	todayFlows, err := s.backend.HourlyFlows(ctx, orgID, dayStart)
	if err != nil {
		return StoredDay{}, fmt.Errorf("hourly flows: %w", err)
	}
	// History flows degrade gracefully — a fresh org / collector gap
	// just means the cost-basis anchor falls back to ZERO state.
	yesterdayFlows, _ := s.backend.HourlyFlows(ctx, orgID, dayStart.AddDate(0, 0, -1))
	dayBeforeFlows, _ := s.backend.HourlyFlows(ctx, orgID, dayStart.AddDate(0, 0, -2))

	deltaPoints, err := s.backend.Timeseries(ctx, orgID, pvGridMetricKeys, dayStart, dayEnd, "1 hour", tz, "delta")
	if err != nil {
		return StoredDay{}, fmt.Errorf("today deltas: %w", err)
	}
	historyStart := dayStart.Add(-historyHours * time.Hour)
	historyDeltaPoints, _ := s.backend.Timeseries(ctx, orgID, pvGridMetricKeys, historyStart, dayStart, "1 hour", tz, "delta")

	socStart := dayStart.Add(-(historyHours + 1) * time.Hour)
	socEnd := dayEnd.Add(-time.Hour)
	socPoints, _ := s.backend.Timeseries(ctx, orgID, []string{socMetricKey}, socStart, socEnd, "1 hour", tz, "last")

	damToday, err := s.backend.DAMPrices(ctx, DAMZone, dayStart, dayStart)
	if err != nil {
		return StoredDay{}, fmt.Errorf("dam prices: %w", err)
	}
	damHistory, _ := s.backend.DAMPrices(ctx, DAMZone, dayStart.AddDate(0, 0, -2), dayStart.AddDate(0, 0, -1))

	// Canonical daily KPIs for reconciliation (best-effort: absence
	// just means the day is computed straight from the allocator).
	var canonical *CanonicalDaily
	if c, ok, cerr := s.backend.CanonicalDaily(ctx, orgID, dayStart); cerr == nil && ok {
		cc := c
		canonical = &cc
	}

	rows, recon := AssembleDay(DayInput{
		DayStart:           dayStart,
		Tariffs:            tariffs,
		TodayFlows:         todayFlows,
		YesterdayFlows:     yesterdayFlows,
		DayBeforeFlows:     dayBeforeFlows,
		DeltaPoints:        deltaPoints,
		HistoryDeltaPoints: historyDeltaPoints,
		SocPoints:          socPoints,
		DamToday:           damToday,
		DamHistory:         damHistory,
		Canonical:          canonical,
	})
	totals := ComputeDailyTotals(rows)
	totals.Reconciled = recon.Applied
	totals.QualityFlags = recon.Flags
	totals.Reconciliation = recon.Detail

	// A day is final once it has fully elapsed in tz — no more
	// telemetry is expected, so the cache can be served verbatim.
	isFinal := !dayEnd.After(time.Now().In(loc))

	day := StoredDay{
		OrganizationID: orgID,
		Day:            dayStart,
		Tz:             loc.String(),
		Rows:           rows,
		Totals:         totals,
		IsFinal:        isFinal,
	}
	if err := s.backend.SaveDay(ctx, day); err != nil {
		return StoredDay{}, fmt.Errorf("save day: %w", err)
	}
	return day, nil
}

// GetDay returns economics for one day, read-through: a stored final day
// is served from cache; a missing or non-final day is recomputed (and
// persisted) on read.
func (s *Service) GetDay(ctx context.Context, orgID, date, tz string) (StoredDay, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return StoredDay{}, err
	}
	parsed, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return StoredDay{}, fmt.Errorf("date must be YYYY-MM-DD")
	}
	dayStart := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	stored, ok, err := s.backend.LoadDay(ctx, orgID, dayStart, loc.String())
	if err != nil {
		return StoredDay{}, fmt.Errorf("load day: %w", err)
	}
	// A final, fully-priced day is immutable — serve it verbatim. We
	// deliberately do NOT short-circuit a final day that still has
	// missing DAM prices: those prices may have been ingested after the
	// day was finalized (late OREE publication / collector backfill), so
	// it must fall through to a fresh recompute to self-heal.
	if ok && stored.IsFinal && stored.Totals.HoursMissingPrice == 0 {
		return stored, nil
	}
	// A still-open day is normally recomputed on read so the dashboard
	// never shows stale intraday numbers. But when the
	// economics-recompute daemon keeps today warm, serve its recent
	// cache instead — that turns the slow live recompute into a fast
	// cache hit. We only trust the cache inside freshTodayWindow so a
	// stopped daemon transparently falls back to live recompute, and
	// only when every hour is priced — a cache computed before the day's
	// RDN prices landed would otherwise pin the "ціни РДН відсутні"
	// warning until the nightly finalize pass.
	if ok && stored.Totals.HoursMissingPrice == 0 && s.freshTodayWindow > 0 &&
		!stored.ComputedAt.IsZero() && time.Since(stored.ComputedAt) < s.freshTodayWindow {
		return stored, nil
	}
	return s.ComputeDay(ctx, orgID, date, tz)
}

// DayError records a single failed day in a range recompute.
type DayError struct {
	Date  string `json:"date"`
	Error string `json:"error"`
}

// RangeResult summarizes a recompute over a date range.
type RangeResult struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	Days       int        `json:"days"`
	DaysOK     int        `json:"days_ok"`
	DaysFailed int        `json:"days_failed"`
	Errors     []DayError `json:"errors,omitempty"`
}

// maxRangeErrors bounds the error list so a fully-broken range can't
// balloon the response.
const maxRangeErrors = 50

// RecomputeRange recomputes (and persists) economics for every day in
// the inclusive [from, to] date span. onProgress (optional) is invoked
// after each day. Honors ctx cancellation between days.
func (s *Service) RecomputeRange(ctx context.Context, orgID, from, to, tz string, onProgress func(done, total int, label string)) (RangeResult, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return RangeResult{}, err
	}
	fromDay, err := time.ParseInLocation("2006-01-02", from, loc)
	if err != nil {
		return RangeResult{}, fmt.Errorf("from must be YYYY-MM-DD")
	}
	toDay, err := time.ParseInLocation("2006-01-02", to, loc)
	if err != nil {
		return RangeResult{}, fmt.Errorf("to must be YYYY-MM-DD")
	}
	if toDay.Before(fromDay) {
		return RangeResult{}, fmt.Errorf("to must be on or after from")
	}
	totalDays := int(toDay.Sub(fromDay)/(24*time.Hour)) + 1
	result := RangeResult{From: from, To: to}
	for day := fromDay; !day.After(toDay); day = day.AddDate(0, 0, 1) {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Days++
		label := day.Format("2006-01-02")
		if _, err := s.ComputeDay(ctx, orgID, label, tz); err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.DaysFailed++
			if len(result.Errors) < maxRangeErrors {
				result.Errors = append(result.Errors, DayError{Date: label, Error: err.Error()})
			}
		} else {
			result.DaysOK++
		}
		if onProgress != nil {
			onProgress(result.Days, totalDays, label)
		}
	}
	return result, nil
}

// loadLocation resolves an IANA tz name; empty / "UTC" → UTC.
func loadLocation(name string) (*time.Location, error) {
	if name == "" || name == "UTC" || name == "Z" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("tz must be a valid IANA timezone (got %q)", name)
	}
	return loc, nil
}
