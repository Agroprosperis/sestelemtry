package economics

import (
	"context"
	"errors"
	"math"
	"time"
)

// MoneyScale returns the factor that turns the UAH of the civil day
// containing t into a display currency (1 / that day's UAH-per-unit
// rate).
type MoneyScale func(t time.Time) float64

// errCurrencyView refuses everything a currency view must not do: compute
// a day from telemetry, or persist one. Economics are stored in UAH only.
var errCurrencyView = errors.New("economics: a currency view only reads stored days")

// InCurrency returns a read-only view of the service whose money comes out
// in another currency: every stored day and hour is multiplied by its own
// day's factor before the rollups add them up, so a month or a year is the
// sum of converted days. Only the read paths that never compute (months,
// years, periods, the portfolio) belong on it; a day is converted with
// ScaleDay after a UAH GetDay instead.
func (s *Service) InCurrency(scale MoneyScale) *Service {
	return &Service{
		backend:          &currencyBackend{inner: s.backend, scale: scale},
		freshTodayWindow: s.freshTodayWindow,
	}
}

// currencyBackend converts what the inner backend has persisted.
type currencyBackend struct {
	inner Backend
	scale MoneyScale
}

func (b *currencyBackend) factor(t time.Time) float64 {
	f := b.scale(t)
	if f <= 0 {
		return 1
	}
	return f
}

func (b *currencyBackend) HourlyFlows(context.Context, string, time.Time) ([]FlowRow, error) {
	return nil, errCurrencyView
}

func (b *currencyBackend) Timeseries(context.Context, string, []string, time.Time, time.Time, string, string, string) ([]Point, error) {
	return nil, errCurrencyView
}

func (b *currencyBackend) DAMPrices(context.Context, int, time.Time, time.Time) ([]DAMHour, error) {
	return nil, errCurrencyView
}

func (b *currencyBackend) CanonicalDaily(context.Context, string, time.Time) (CanonicalDaily, bool, error) {
	return CanonicalDaily{}, false, errCurrencyView
}

func (b *currencyBackend) SaveDay(context.Context, StoredDay) error {
	return errCurrencyView
}

// TariffSchedule stays in UAH: the per-kWh wear it carries is converted
// hour by hour through HourlyRecord.MoneyScale, because a schedule
// version spans days with different rates.
func (b *currencyBackend) TariffSchedule(ctx context.Context, orgID string) (Schedule, error) {
	return b.inner.TariffSchedule(ctx, orgID)
}

func (b *currencyBackend) LoadDay(ctx context.Context, orgID string, day time.Time, tz string) (StoredDay, bool, error) {
	stored, ok, err := b.inner.LoadDay(ctx, orgID, day, tz)
	if err != nil || !ok {
		return stored, ok, err
	}
	return ScaleDay(stored, b.factor(day)), true, nil
}

func (b *currencyBackend) LoadDailyRange(ctx context.Context, orgID string, from, to time.Time) ([]DailyRecord, error) {
	days, err := b.inner.LoadDailyRange(ctx, orgID, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]DailyRecord, len(days))
	for i, d := range days {
		f := b.factor(dayInstant(d.Day))
		d.Totals = scaleDailyTotals(d.Totals, f)
		d.MoneyScale = f
		out[i] = d
	}
	return out, nil
}

func (b *currencyBackend) LoadHourlyRange(ctx context.Context, orgID string, from, to time.Time) ([]HourlyRecord, error) {
	hours, err := b.inner.LoadHourlyRange(ctx, orgID, from, to)
	if err != nil {
		return nil, err
	}
	out := make([]HourlyRecord, len(hours))
	for i, h := range hours {
		f := b.factor(h.HourStart)
		h.Rdn = scalePtr(h.Rdn, f)
		h.ImportPrice *= f
		h.ExportPrice *= f
		h.EssNet *= f
		h.EssWithdrawnCostUah = scalePtr(h.EssWithdrawnCostUah, f)
		h.EssRealizedProfitUah = scalePtr(h.EssRealizedProfitUah, f)
		h.MoneyScale = f
		out[i] = h
	}
	return out, nil
}

// sumEbitdaFrom is earlier than any stored economics day.
var sumEbitdaFrom = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// SumEbitdaBefore converts each prior day at its own rate, so the opening
// balance follows the same rule as the window after it.
func (b *currencyBackend) SumEbitdaBefore(ctx context.Context, orgID string, before time.Time) (float64, int, error) {
	days, err := b.inner.LoadDailyRange(ctx, orgID, sumEbitdaFrom, before.AddDate(0, 0, -1))
	if err != nil {
		return 0, 0, err
	}
	var sum float64
	months := map[string]struct{}{}
	for _, d := range days {
		if d.Totals.HoursWithData <= 0 {
			continue
		}
		sum += d.Totals.Ebitda * b.factor(dayInstant(d.Day))
		months[d.Day.Format("2006-01")] = struct{}{}
	}
	return sum, len(months), nil
}

// dayInstant is a moment inside the civil day a daily record is dated.
// Stored dates come back as UTC midnight, which a zone west of UTC would
// place on the day before.
func dayInstant(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, time.UTC)
}

// FirstDay is the earliest stored day with economics data: the date a
// flat CAPEX with no version of its own is converted at.
func (s *Service) FirstDay(ctx context.Context, orgID string) (time.Time, bool, error) {
	days, err := s.backend.LoadDailyRange(ctx, orgID, sumEbitdaFrom, time.Now().AddDate(1, 0, 0))
	if err != nil {
		return time.Time{}, false, err
	}
	for _, d := range days {
		if d.Totals.HoursWithData > 0 {
			return d.Day, true, nil
		}
	}
	return time.Time{}, false, nil
}

// EbitdaWeights is the EBITDA of a span of stored days in UAH and as a
// currency view converted it, day by day: the sums the span's rate is
// read from. The portfolio adds its objects' weights up with Add.
type EbitdaWeights struct {
	uah, converted       float64
	absUah, absConverted float64
	minScale, maxScale   float64
	days                 int
}

// EbitdaWeights weighs the stored days in [from, to] at the factors the
// view's rollups convert them by. It reads the daily records only, never
// the hours. A UAH service converts nothing and has no weights.
func (s *Service) EbitdaWeights(ctx context.Context, orgID string, from, to time.Time) (EbitdaWeights, error) {
	cb, ok := s.backend.(*currencyBackend)
	if !ok {
		return EbitdaWeights{}, nil
	}
	days, err := cb.inner.LoadDailyRange(ctx, orgID, from, to)
	if err != nil {
		return EbitdaWeights{}, err
	}
	var w EbitdaWeights
	for _, d := range days {
		w.add(d.Totals.Ebitda, cb.factor(dayInstant(d.Day)))
	}
	return w, nil
}

func (w *EbitdaWeights) add(uah, scale float64) {
	if uah == 0 {
		return
	}
	w.Add(EbitdaWeights{
		uah: uah, converted: uah * scale,
		absUah: math.Abs(uah), absConverted: math.Abs(uah) * scale,
		minScale: scale, maxScale: scale, days: 1,
	})
}

// Add folds o into w.
func (w *EbitdaWeights) Add(o EbitdaWeights) {
	if o.days == 0 {
		return
	}
	if w.days == 0 {
		*w = o
		return
	}
	w.uah += o.uah
	w.converted += o.converted
	w.absUah += o.absUah
	w.absConverted += o.absConverted
	w.minScale = min(w.minScale, o.minScale)
	w.maxScale = max(w.maxScale, o.maxScale)
	w.days += o.days
}

// rateSlack lets a rate equal to an end of the days' range through
// despite rounding.
const rateSlack = 1e-9

// Rate is the UAH per unit the weighed EBITDA came out at: Σ UAH over Σ
// converted, so the UAH EBITDA divided by it is the converted one. Days
// of both signs can push that ratio outside the days' own rates; then
// each day weighs by the size of its EBITDA instead, which always lands
// between them. ok is false without any EBITDA to weigh.
func (w EbitdaWeights) Rate() (float64, bool) {
	if w.days == 0 || w.absConverted <= 0 {
		return 0, false
	}
	lo, hi := 1/w.maxScale, 1/w.minScale
	if w.converted != 0 {
		if r := w.uah / w.converted; r >= lo*(1-rateSlack) && r <= hi*(1+rateSlack) {
			return r, true
		}
	}
	return w.absUah / w.absConverted, true
}

// ScaleDay multiplies every amount of money and every price of one stored
// day by f; energy, SOC and quality fields stay as they are.
func ScaleDay(day StoredDay, f float64) StoredDay {
	out := day
	out.Totals = scaleDailyTotals(day.Totals, f)
	out.Rows = make([]*HourRow, len(day.Rows))
	for i, r := range day.Rows {
		if r == nil {
			continue
		}
		row := *r
		row.Rdn = scalePtr(r.Rdn, f)
		row.Econ.ImportPrice *= f
		row.Econ.ExportPrice *= f
		row.Econ.BaselineCost *= f
		row.Econ.ActualCost *= f
		row.Econ.Effect *= f
		row.Econ.EssNet *= f
		row.EssCostBasisUahStart = scalePtr(r.EssCostBasisUahStart, f)
		row.EssAvgCostUahPerKwhStart = scalePtr(r.EssAvgCostUahPerKwhStart, f)
		row.EssWithdrawnCostUah = scalePtr(r.EssWithdrawnCostUah, f)
		row.EssRealizedProfitUah = scalePtr(r.EssRealizedProfitUah, f)
		row.EssCostBasisUahEnd = scalePtr(r.EssCostBasisUahEnd, f)
		row.EssAvgCostUahPerKwhEnd = scalePtr(r.EssAvgCostUahPerKwhEnd, f)
		out.Rows[i] = &row
	}
	return out
}

func scaleDailyTotals(t DailyTotals, f float64) DailyTotals {
	t.BaselineCost *= f
	t.ActualCost *= f
	t.Effect *= f
	t.EssNet *= f
	t.AvgImportPrice *= f
	t.AvgExportPrice *= f
	t.RevenuePvExport *= f
	t.RevenuePvSelf *= f
	t.RevenueEssExport *= f
	t.RevenueEssSelf *= f
	t.RevenueTotal *= f
	t.ExpenseGridCharge *= f
	t.ExpenseTotal *= f
	t.Ebitda *= f
	t.EssWithdrawnCost *= f
	t.EssRealizedProfit *= f
	t.EssDegradationCost *= f
	t.EssAvgCostBasisEod *= f
	t.EssCostBasisUahEod *= f
	return t
}

func scalePtr(v *float64, f float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v * f
	return &x
}
