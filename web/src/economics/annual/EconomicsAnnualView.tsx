import { useMemo, useState } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceDot,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type {
  EconomicsAnnualMonthMargin,
  EconomicsAnnualMonthRollup,
  EconomicsAnnualQuarter,
  EconomicsAnnualResponse,
  EconomicsMonthlyTotals,
} from '../../api'
import { formatOrganizationLabel } from '../../dashboard/config'
import { makeTrendCap, TREND_NEG_ORDER, TREND_POS_ORDER } from '../trendBarCap'
import {
  AiLead,
  EssDataQualityNote,
  MonthlyBalance,
  MonthlyFinance,
  MonthlyKpis,
  MonthlyWaterfall,
  OptimumInfo,
} from '../monthly/EconomicsMonthlyView'
import {
  buildAiPanel,
  HOURS,
  heatTier,
  type PeriodScope,
  reserveSplit,
  signClass,
  uahShort,
} from '../monthly/rollup'
import {
  buildPaybackModel,
  type CapexPaybackRow,
  formatMonthYearShort,
  moneyAxis,
  paybackAxis,
  paybackLabel,
} from '../payback'
import {
  formatCycles,
  formatKwh,
  formatMonthName,
  formatMonthShort,
  formatMonthTitle,
  formatMwh,
  formatMwhNumber,
  formatPercent,
  formatPeriodTitle,
  formatPrice,
  formatUah,
  formatYearTitle,
} from '../monthly/format'

type Props = {
  data: EconomicsAnnualResponse
  organizationID: string
  // capexUah is the one-time project capital expenditure (UAH) from the
  // org tariff config. 0 hides the payback/ROI panel.
  capexUah: number
  // onSelectMonth drills down to the month view of the clicked YYYY-MM.
  onSelectMonth: (month: string) => void
}

export function EconomicsAnnualView({ data, organizationID, capexUah, onSelectMonth }: Props) {
  const t = data.totals
  const withData = useMemo(
    () => data.months.filter((m) => m.totals.hours_with_data > 0),
    [data.months],
  )
  const periodTitle = formatPeriodTitle(data.from, data.to) || formatYearTitle(data.period)
  const exportSlug = data.from && data.to ? `${data.from}_${data.to}` : data.period
  // A full Jan→Dec span of one calendar year reads "за рік"; any other
  // sliding window reads "за період".
  const isCalendarYear =
    data.from.slice(5) === '01' &&
    data.to.slice(5) === '12' &&
    data.from.slice(0, 4) === data.to.slice(0, 4)
  const scope: PeriodScope = isCalendarYear ? 'year' : 'period'
  return (
    <>
      <MonthlyKpis totals={t} scope={scope} />
      <QuarterCards quarters={data.quarters} />
      <AnnualCapex
        capexUah={capexUah}
        months={data.months}
        ebitda={t.ebitda_uah}
        priorEbitda={data.prior_ebitda_uah}
        priorMonthsWithData={data.prior_months_with_data}
      />
      <div className="economics-month-grid2">
        <MonthlyFinance totals={t} scope={scope} />
        <MonthlyWaterfall totals={t} scope={scope} />
      </div>
      <AnnualAiAnalysis
        totals={t}
        months={withData}
        organizationID={organizationID}
        periodTitle={periodTitle}
        scope={scope}
      />
      <div className="economics-month-grid2">
        <AnnualTrend months={data.months} totals={t} onSelectMonth={onSelectMonth} />
        <MonthlyBalance totals={t} scope={scope} />
      </div>
      <MonthHourHeatmap margins={data.monthly_margin} />
      <AnnualMonthlyTable
        months={withData}
        totals={t}
        organizationID={organizationID}
        exportSlug={exportSlug}
        onSelectMonth={onSelectMonth}
      />
    </>
  )
}

// --- Quarter cards (SPEC §3.3) ---

const Q_ROMAN = ['I', 'II', 'III', 'IV']
const Q_COLORS = ['#2f6fed', '#12b76a', '#f59e0b', '#7c3aed']

function QuarterCards({ quarters }: { quarters: EconomicsAnnualQuarter[] }) {
  return (
    <section className="economics-quarter-grid" aria-label="Квартальні підсумки">
      {quarters.map((q) => {
        const i = (q.quarter - 1) % 4
        return (
          <article
            key={`${q.year}-${q.quarter}`}
            className="economics-card economics-quarter-card"
            style={{ borderTop: `3px solid ${Q_COLORS[i]}` }}
          >
            <div className="economics-quarter-label">{Q_ROMAN[i]} кв. {q.year}</div>
            <div className={`economics-quarter-value ${signClass(q.ebitda_uah)}`}>{formatUah(q.ebitda_uah)}</div>
            <div className="economics-quarter-note">EBITDA · {formatMwh(q.pv_kwh)} СЕС</div>
            <div className="economics-quarter-sub">ефект {formatUah(q.effect_uah)}</div>
          </article>
        )
      })}
    </section>
  )
}

// --- CAPEX / payback ---
//
// Display-only panel: CAPEX from tariff config vs the all-time
// cumulative EBITDA. The main chart plots the accumulated UAH effect on
// a numeric month axis counted from the start of operation, extends it
// with a linear run-rate forecast and marks today, the CAPEX level and
// the projected payback point. Seasonality lives in a secondary chart.
// The math lives in ../payback.ts, shared with the standalone
// "Окупність проєкту" page.

type CapexPaybackTooltipProps = {
  active?: boolean
  payload?: { payload: CapexPaybackRow }[]
}

// Exported for the standalone payback page, which renders the same
// chart rows (mirrors the OptimumInfo export from the monthly view).
export function CapexPaybackTooltip({ active, payload }: CapexPaybackTooltipProps) {
  if (!active || !payload?.length) return null
  const row = payload[0].payload
  const cum = row.factCum ?? row.forecastCum
  const isForecast = row.kind === 'forecast'
  const title =
    row.kind === 'start'
      ? 'Початок експлуатації'
      : row.monthKey
        ? formatMonthTitle(row.monthKey)
        : ''
  return (
    <div className="economics-trend-tip">
      <div className="economics-trend-tip-day">
        {title}
        {isForecast ? ' · прогноз' : ''}
      </div>
      <div className="economics-trend-tip-row">
        <i style={{ background: isForecast ? '#60a5fa' : '#2f6fed' }} />
        <span>{isForecast ? 'Прогноз накопичено' : 'Накопичено'}</span>
        <b>{cum === null ? '—' : formatUah(cum)}</b>
      </div>
      {row.monthEbitda !== null ? (
        <div className="economics-trend-tip-row">
          <i style={{ background: '#12b76a' }} />
          <span>EBITDA за місяць</span>
          <b>{formatUah(row.monthEbitda)}</b>
        </div>
      ) : null}
      {row.kind === 'prior' ? (
        <div className="economics-month-muted">накопичений EBITDA до вибраного періоду</div>
      ) : null}
    </div>
  )
}

type SeasonalGrain = 'month' | 'quarter' | 'year'

type SeasonalRow = {
  label: string
  period: number
  cum: number
}

type SeasonalTooltipProps = {
  active?: boolean
  payload?: { payload: SeasonalRow }[]
}

function SeasonalTooltip({ active, payload }: SeasonalTooltipProps) {
  if (!active || !payload?.length) return null
  const row = payload[0].payload
  return (
    <div className="economics-trend-tip">
      <div className="economics-trend-tip-day">{row.label}</div>
      <div className="economics-trend-tip-row">
        <i style={{ background: '#12b76a' }} />
        <span>Ефект за період</span>
        <b>{formatUah(row.period)}</b>
      </div>
      <div className="economics-trend-tip-row">
        <i style={{ background: '#2f6fed' }} />
        <span>Накопичено</span>
        <b>{formatUah(row.cum)}</b>
      </div>
    </div>
  )
}

function AnnualCapex({
  capexUah,
  months,
  ebitda,
  priorEbitda,
  priorMonthsWithData,
}: {
  capexUah: number
  months: EconomicsAnnualMonthRollup[]
  ebitda: number
  // priorEbitda is the cumulative EBITDA earned before the window start
  // (the opening balance since the start of operation).
  priorEbitda: number
  // priorMonthsWithData is how many months with data precede the window,
  // used to annualise all-time EBITDA for the payback estimate.
  priorMonthsWithData: number
}) {
  const [seasonGrain, setSeasonGrain] = useState<SeasonalGrain>('month')

  const model = useMemo(
    () => buildPaybackModel({ capexUah, months, ebitda, priorEbitda, priorMonthsWithData }),
    [capexUah, months, ebitda, priorEbitda, priorMonthsWithData],
  )
  const {
    monthsWithData,
    prior,
    hasPrior,
    allTimeEbitda,
    paybackYears,
    coveredShare,
    remaining,
    operationYears,
    paidOff,
    rows: paybackRows,
    todayT,
    paybackT,
    paybackMonthKey,
    tMax,
    timeOffset,
    firstMonthKey,
  } = model

  const { ticks: timeTicks, format: tickLabel } = paybackAxis(tMax, firstMonthKey, timeOffset)
  const yMax = Math.max(capexUah, allTimeEbitda, ...paybackRows.map((r) => r.forecastCum ?? r.factCum ?? 0), 0)
  const axis = moneyAxis(yMax)

  const seasonalRows = useMemo<SeasonalRow[]>(() => {
    type Bucket = { label: string; period: number; order: string }
    const buckets: Bucket[] = []
    if (seasonGrain === 'month') {
      for (const m of monthsWithData) {
        buckets.push({ label: formatMonthShort(m.month), period: m.totals.ebitda_uah, order: m.month })
      }
    } else if (seasonGrain === 'quarter') {
      const map = new Map<string, Bucket>()
      for (const m of monthsWithData) {
        const y = m.month.slice(0, 4)
        const q = Math.ceil(Number(m.month.slice(5, 7)) / 3)
        const key = `${y}-Q${q}`
        const cur = map.get(key) ?? { label: `${Q_ROMAN[q - 1]} кв. ${y}`, period: 0, order: key }
        cur.period += m.totals.ebitda_uah
        map.set(key, cur)
      }
      buckets.push(...[...map.values()].sort((a, b) => a.order.localeCompare(b.order)))
    } else {
      const map = new Map<string, Bucket>()
      for (const m of monthsWithData) {
        const y = m.month.slice(0, 4)
        const cur = map.get(y) ?? { label: y, period: 0, order: y }
        cur.period += m.totals.ebitda_uah
        map.set(y, cur)
      }
      buckets.push(...[...map.values()].sort((a, b) => a.order.localeCompare(b.order)))
    }
    let acc = prior
    return buckets.map((b) => {
      acc += b.period
      return { label: b.label, period: b.period, cum: acc }
    })
  }, [monthsWithData, seasonGrain, prior])

  const seasonAxis = moneyAxis(Math.max(...seasonalRows.map((r) => Math.abs(r.period)), ...seasonalRows.map((r) => Math.abs(r.cum)), 0))

  if (!(capexUah > 0)) return null

  return (
    <section className="economics-card economics-month-section economics-capex" aria-label="Окупність проєкту">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">
          Окупність проєкту
          <OptimumInfo tip="CAPEX — разові капітальні інвестиції з налаштувань об'єкта. Повернуто = накопичений EBITDA з початку експлуатації. Прогноз окупності = CAPEX / річний темп EBITDA (накопичений EBITDA × 12 / місяців з даними). Середньорічний фактичний ROI = накопичений EBITDA / CAPEX / роки експлуатації." />
        </h3>
        <span className="economics-month-muted">CAPEX із налаштувань об'єкта</span>
      </div>

      <div className="economics-capex-cards">
        <div className="economics-month-mini">
          <span className="economics-month-mini-label">CAPEX проєкту</span>
          <span className="economics-month-mini-value">{formatUah(capexUah)}</span>
          <span className="economics-month-mini-note">разові інвестиції</span>
        </div>
        <div className="economics-month-mini">
          <span className="economics-month-mini-label">Повернуто інвестицій</span>
          <span className={`economics-month-mini-value ${allTimeEbitda >= 0 ? 'good' : ''}`}>{formatUah(allTimeEbitda)}</span>
          <span className="economics-month-mini-note">накопичений ефект</span>
        </div>
        <div className="economics-month-mini">
          <span className="economics-month-mini-label">Залишилось повернути</span>
          <span className="economics-month-mini-value">{paidOff ? '0 грн' : formatUah(remaining)}</span>
          <span className="economics-month-mini-note">{paidOff ? 'капекс окуплено' : 'до повної окупності'}</span>
        </div>
        <div className="economics-month-mini">
          <span className="economics-month-mini-label">
            Покрито CAPEX
            <OptimumInfo tip={`Середньорічний фактичний ROI за весь період експлуатації = накопичений EBITDA / CAPEX / роки експлуатації. Зараз: ${formatPercent(Number.isFinite(operationYears) && operationYears > 0 ? allTimeEbitda / capexUah / operationYears : NaN)} за ${paybackLabel(operationYears)}.`} />
          </span>
          <span className={`economics-month-mini-value ${coveredShare >= 0 ? 'good' : ''}`}>{formatPercent(coveredShare)}</span>
          <span className="economics-month-mini-note">повернуто / CAPEX</span>
        </div>
        <div className="economics-month-mini">
          <span className="economics-month-mini-label">Прогноз окупності</span>
          <span className="economics-month-mini-value">{paidOff ? 'окуплено' : paybackLabel(paybackYears)}</span>
          <span className="economics-month-mini-note">
            {paybackMonthKey ? `≈ ${formatMonthYearShort(paybackMonthKey)}` : 'від початку експлуатації'}
          </span>
        </div>
      </div>

      <div className="economics-capex-progress">
        <div className="economics-capex-progress-head">
          <span>
            Повернуто {uahShort(allTimeEbitda)} із {uahShort(capexUah)}.
            {paidOff ? ' Капекс окуплено.' : ` Залишилось повернути ${uahShort(remaining)}.`}
          </span>
          {hasPrior ? (
            <span className="economics-month-muted">
              Накопичений EBITDA до вибраного періоду: {formatUah(prior)}
            </span>
          ) : null}
        </div>
        <div className="economics-capex-bar">
          <span className="economics-capex-bar-fill" style={{ width: `${coveredShare * 100}%` }} />
        </div>
      </div>

      {paybackRows.length > 1 ? (
        <div className="economics-month-chart economics-capex-chart">
          <div className="economics-capex-chart-head">
            <span className="economics-capex-chart-title">
              Накопичена окупність проєкту
              <span className="economics-capex-chart-unit">{axis.unit}</span>
              <OptimumInfo tip="Суцільна лінія — фактичний накопичений EBITDA від початку експлуатації. Пунктир — базовий прогноз тим самим річним темпом до перетину з CAPEX. Вертикаль «Сьогодні» — остання фактична точка; червона точка — прогнозований місяць повного повернення інвестицій." />
            </span>
            <span className="economics-month-legend">
              <span><i style={{ background: '#2f6fed' }} />факт</span>
              <span><i style={{ background: '#60a5fa' }} />прогноз</span>
              <span><i style={{ background: '#94a3b8' }} />CAPEX</span>
            </span>
          </div>
          <ResponsiveContainer width="100%" height={220}>
            <ComposedChart data={paybackRows} margin={{ top: 16, right: 16, bottom: 0, left: 0 }}>
              <CartesianGrid strokeDasharray="2 5" stroke="#e7ecf2" vertical={false} />
              <XAxis
                type="number"
                dataKey="t"
                domain={[0, tMax]}
                ticks={timeTicks}
                tickFormatter={tickLabel}
                tick={{ fontSize: 11, fill: '#8a94a6' }}
                tickLine={false}
                axisLine={false}
              />
              <YAxis
                tick={{ fontSize: 11, fill: '#98a2b3' }}
                width={36}
                tickLine={false}
                axisLine={false}
                tickFormatter={axis.tick}
                domain={[0, (dataMax: number) => Math.max(dataMax, capexUah) * 1.08]}
              />
              <Tooltip content={<CapexPaybackTooltip />} cursor={{ stroke: '#cbd5e1' }} />
              <ReferenceLine
                y={capexUah}
                stroke="#94a3b8"
                strokeDasharray="5 4"
                label={{ value: `CAPEX ${uahShort(capexUah)}`, position: 'insideBottomLeft', fontSize: 11, fill: '#64748b' }}
              />
              {todayT !== null && todayT > 0 ? (
                <ReferenceLine
                  x={todayT}
                  stroke="#cbd5e1"
                  strokeDasharray="3 3"
                  label={{ value: 'Сьогодні', position: 'insideTopRight', fontSize: 11, fill: '#94a3b8' }}
                />
              ) : null}
              <Line
                type="monotone"
                dataKey="factCum"
                stroke="#2f6fed"
                strokeWidth={2.2}
                dot={false}
                connectNulls={false}
              />
              <Line
                type="monotone"
                dataKey="forecastCum"
                stroke="#60a5fa"
                strokeWidth={2}
                strokeDasharray="6 4"
                dot={false}
                connectNulls={false}
              />
              {paybackT !== null ? (
                <ReferenceDot
                  x={paybackT}
                  y={capexUah}
                  r={4}
                  fill="#dc2626"
                  stroke="#fff"
                  strokeWidth={1.5}
                  label={{
                    value: paybackMonthKey ? `окупність · ${formatMonthYearShort(paybackMonthKey)}` : 'окупність',
                    position: 'left',
                    fontSize: 11,
                    fill: '#dc2626',
                  }}
                />
              ) : null}
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      ) : null}

      {seasonalRows.length > 0 ? (
        <div className="economics-month-chart economics-capex-chart economics-capex-seasonal">
          <div className="economics-capex-chart-head">
            <span className="economics-capex-chart-title">
              Щомісячний економічний ефект
              <span className="economics-capex-chart-unit">{seasonAxis.unit}</span>
              <OptimumInfo tip="Стовпці — EBITDA (економічний ефект) за вибрану гранулярність у межах поточного вікна. Лінія — накопичений EBITDA від початку експлуатації (із залишком до вибраного періоду)." />
            </span>
            <div className="economics-capex-season-tools">
              <span className="economics-month-legend">
                <span><i style={{ background: '#12b76a' }} />ефект</span>
                <span><i style={{ background: '#2f6fed' }} />накопичено</span>
              </span>
              <div className="economics-range-switch economics-capex-grain" role="group" aria-label="Гранулярність ефекту">
                {(
                  [
                    ['month', 'місяці'],
                    ['quarter', 'квартали'],
                    ['year', 'роки'],
                  ] as const
                ).map(([key, label]) => (
                  <button
                    key={key}
                    type="button"
                    className={seasonGrain === key ? 'active' : undefined}
                    onClick={() => setSeasonGrain(key)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>
          </div>
          <ResponsiveContainer width="100%" height={170}>
            <ComposedChart data={seasonalRows} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
              <CartesianGrid strokeDasharray="2 5" stroke="#e7ecf2" vertical={false} />
              <XAxis dataKey="label" tick={{ fontSize: 11, fill: '#8a94a6' }} tickLine={false} axisLine={false} />
              <YAxis
                yAxisId="period"
                tick={{ fontSize: 11, fill: '#98a2b3' }}
                width={36}
                tickLine={false}
                axisLine={false}
                tickFormatter={seasonAxis.tick}
              />
              <YAxis yAxisId="cum" orientation="right" hide width={0} />
              <Tooltip content={<SeasonalTooltip />} cursor={{ fill: 'rgba(148, 163, 184, 0.12)' }} />
              <Bar
                yAxisId="period"
                dataKey="period"
                fill="#12b76a"
                fillOpacity={0.35}
                stroke="#12b76a"
                strokeWidth={1}
                maxBarSize={28}
                radius={[3, 3, 0, 0]}
              />
              <Line
                yAxisId="cum"
                type="monotone"
                dataKey="cum"
                stroke="#2f6fed"
                strokeWidth={2}
                dot={false}
              />
            </ComposedChart>
          </ResponsiveContainer>
        </div>
      ) : null}
    </section>
  )
}

// --- Annual energy trend (12 month stacked bars, SPEC §3.6) ---

type AnnualTrendRow = {
  month: string
  label: string
  hasData: boolean
  gridImport: number
  essDischarge: number
  pv: number
  gridExport: number
  essCharge: number
  load: number
}

const trendNumberFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })

const TREND_SERIES = [
  { key: 'gridImport', name: 'з мережі', color: '#12b76a' },
  { key: 'essDischarge', name: 'розряд УЗЕ', color: '#5fc993' },
  { key: 'pv', name: 'виробіток СЕС', color: '#91d9aa' },
  { key: 'gridExport', name: 'експорт у мережу', color: '#f97316' },
  { key: 'essCharge', name: 'заряд УЗЕ', color: '#fb923c' },
  { key: 'load', name: 'споживання', color: '#fdba74' },
] as const

type TrendTooltipProps = {
  active?: boolean
  payload?: { payload: AnnualTrendRow }[]
}

function TrendTooltip({ active, payload }: TrendTooltipProps) {
  if (!active || !payload?.length) return null
  const row = payload[0].payload
  return (
    <div className="economics-trend-tip">
      <div className="economics-trend-tip-day">{formatMonthTitle(row.month)}</div>
      {TREND_SERIES.map((s) => (
        <div className="economics-trend-tip-row" key={s.key}>
          <i style={{ background: s.color }} />
          <span>{s.name}</span>
          <b>{trendNumberFmt.format(Math.abs(row[s.key]))}</b>
        </div>
      ))}
      {row.hasData ? <div className="economics-month-muted">Натисніть, щоб відкрити місяць</div> : null}
    </div>
  )
}

// chartClickState is the loose shape recharts hands the click callback;
// activePayload[0].payload is the row of the clicked category.
type ChartClickState = { activePayload?: Array<{ payload?: AnnualTrendRow }> }

function AnnualTrend({
  months,
  totals,
  onSelectMonth,
}: {
  months: EconomicsAnnualMonthRollup[]
  totals: EconomicsMonthlyTotals
  onSelectMonth: (month: string) => void
}) {
  const rows = useMemo<AnnualTrendRow[]>(
    () =>
      months.map((m) => ({
        month: m.month,
        label: formatMonthShort(m.month),
        hasData: m.totals.hours_with_data > 0,
        gridImport: m.totals.grid_import_kwh / 1000,
        essDischarge: m.totals.ess_discharged_kwh / 1000,
        pv: m.totals.pv_kwh / 1000,
        gridExport: -m.totals.grid_export_kwh / 1000,
        essCharge: -m.totals.ess_charged_kwh / 1000,
        load: -m.totals.load_kwh / 1000,
      })),
    [months],
  )

  const pvSelf = totals.pv_to_load_kwh
  const pvOther = totals.pv_to_grid_kwh + totals.pv_to_ess_kwh
  const pvSelfShare = totals.pv_kwh > 0 ? pvSelf / totals.pv_kwh : 0
  const loadFromRenewable = totals.pv_to_load_kwh + totals.ess_to_load_kwh
  const loadFromGrid = totals.grid_to_load_kwh
  const consumption = loadFromRenewable + loadFromGrid
  const loadRenewableShare = consumption > 0 ? loadFromRenewable / consumption : 0

  // recharts types the click arg as its internal MouseHandlerDataParam;
  // we only need activePayload[0].payload, so read it through a cast.
  const handleClick = (state: unknown) => {
    const row = (state as ChartClickState | undefined)?.activePayload?.[0]?.payload
    if (row?.hasData) onSelectMonth(row.month)
  }

  return (
    <section className="economics-card economics-month-section" aria-label="Енергетичний тренд по місяцях">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Енергетичний тренд по місяцях</h3>
        <span className="economics-month-muted">МВт·год/місяць</span>
      </div>
      <div className="economics-trend-summary">
        <div className="economics-trend-metric">
          <div>
            <strong>Вироблено СЕС: {formatMwhNumber(totals.pv_kwh)}</strong>
            <span className="economics-trend-mwh">МВт·год</span>
          </div>
          <div className="economics-ratio-line">
            <span>{formatPercent(pvSelfShare)}</span>
            <div className="economics-ratio-bar">
              <span style={{ width: `${pvSelfShare * 100}%`, background: '#12b76a' }} />
              <span style={{ width: `${(1 - pvSelfShare) * 100}%`, background: '#91d9aa' }} />
            </div>
            <span>{formatPercent(1 - pvSelfShare)}</span>
          </div>
          <div className="economics-month-muted">спожито {formatMwh(pvSelf)} / експорт + заряд УЗЕ {formatMwh(pvOther)}</div>
        </div>
        <div className="economics-trend-metric">
          <div>
            <strong>Споживання об'єкта: {formatMwhNumber(consumption)}</strong>
            <span className="economics-trend-mwh">МВт·год</span>
          </div>
          <div className="economics-ratio-line">
            <span>{formatPercent(loadRenewableShare)}</span>
            <div className="economics-ratio-bar">
              <span style={{ width: `${loadRenewableShare * 100}%`, background: '#f97316' }} />
              <span style={{ width: `${(1 - loadRenewableShare) * 100}%`, background: '#fdba74' }} />
            </div>
            <span>{formatPercent(1 - loadRenewableShare)}</span>
          </div>
          <div className="economics-month-muted">від СЕС+УЗЕ {formatMwh(loadFromRenewable)} / з мережі {formatMwh(loadFromGrid)}</div>
        </div>
      </div>
      <div className="economics-month-chart">
        <ResponsiveContainer width="100%" height={300}>
          <BarChart
            data={rows}
            margin={{ top: 8, right: 8, bottom: 0, left: 0 }}
            stackOffset="sign"
            barCategoryGap="18%"
            onClick={handleClick}
            style={{ cursor: 'pointer' }}
          >
            <CartesianGrid strokeDasharray="2 5" stroke="#e7ecf2" vertical={false} />
            <XAxis dataKey="label" tick={{ fontSize: 10, fill: '#8a94a6' }} interval={0} tickLine={false} axisLine={false} />
            <YAxis tick={{ fontSize: 11, fill: '#98a2b3' }} width={40} tickLine={false} axisLine={false} />
            <Tooltip content={<TrendTooltip />} cursor={{ fill: 'rgba(148, 163, 184, 0.12)' }} />
            <ReferenceLine y={0} stroke="#98a2b3" />
            <Bar dataKey="pv" name="виробіток СЕС" stackId="pos" fill="#91d9aa" maxBarSize={36} shape={makeTrendCap('top', 'pv', TREND_POS_ORDER)} />
            <Bar dataKey="essDischarge" name="розряд УЗЕ" stackId="pos" fill="#5fc993" maxBarSize={36} shape={makeTrendCap('top', 'essDischarge', TREND_POS_ORDER)} />
            <Bar dataKey="gridImport" name="з мережі" stackId="pos" fill="#12b76a" maxBarSize={36} shape={makeTrendCap('top', 'gridImport', TREND_POS_ORDER)} />
            <Bar dataKey="load" name="споживання" stackId="neg" fill="#fdba74" maxBarSize={36} shape={makeTrendCap('bottom', 'load', TREND_NEG_ORDER)} />
            <Bar dataKey="essCharge" name="заряд УЗЕ" stackId="neg" fill="#fb923c" maxBarSize={36} shape={makeTrendCap('bottom', 'essCharge', TREND_NEG_ORDER)} />
            <Bar dataKey="gridExport" name="експорт у мережу" stackId="neg" fill="#f97316" maxBarSize={36} shape={makeTrendCap('bottom', 'gridExport', TREND_NEG_ORDER)} />
          </BarChart>
        </ResponsiveContainer>
      </div>
      <div className="economics-trend-legend">
        {TREND_SERIES.map((s) => (
          <span key={s.key}>
            <i style={{ background: s.color }} />
            {s.name}
          </span>
        ))}
      </div>
    </section>
  )
}

// --- Month x hour-of-day marginality heatmap (SPEC §3.7) ---

function MonthHourHeatmap({ margins }: { margins: EconomicsAnnualMonthMargin[] }) {
  return (
    <section className="economics-card economics-month-section" aria-label="Маржинальність УЗЕ по місяцях">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Heatmap: маржинальність УЗЕ (місяць × година)</h3>
        <span className="economics-month-muted">грн/кВт·год розряду</span>
      </div>
      <div className="economics-heatmap-scroll">
        <table className="economics-heatmap">
          <thead>
            <tr>
              <th className="economics-heatmap-corner">Місяць</th>
              {HOURS.map((h) => (
                <th key={h}>{String(h).padStart(2, '0')}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {margins.map((row) => (
              <tr key={row.month}>
                <th scope="row">{formatMonthShort(row.month)}</th>
                {HOURS.map((h) => {
                  const v = row.hours[h] ?? null
                  return (
                    <td key={h} className={heatTier(v)}>
                      {v === null ? '' : Math.round(v)}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="economics-month-explain">
        Колір показує середню маржу розряду УЗЕ за годину доби, усереднену по днях місяця: сірий 0–1,
        світло-зелений 2–5, зелений 6–11, темно-зелений понад 12 грн/кВт·год.
      </p>
    </section>
  )
}

// --- Annual AI analysis ---
//
// Same deterministic management panel as the month view (lead + result),
// with the two headline opportunities (elevator schedule, battery timing)
// rendered as accordion items that each expand into a per-month list.

function AnnualAiAnalysis({
  totals,
  months,
  organizationID,
  periodTitle,
  scope,
}: {
  totals: EconomicsMonthlyTotals
  months: EconomicsAnnualMonthRollup[]
  organizationID: string
  periodTitle: string
  scope: PeriodScope
}) {
  const heading = `${formatOrganizationLabel(organizationID)} · ${periodTitle}`
  const panel = useMemo(
    () =>
      buildAiPanel(totals, {
        heading,
        scope,
        periodLabel: periodTitle,
        monthsCount: months.length,
      }),
    [totals, heading, periodTitle, scope, months.length],
  )

  // Per-month elevator-schedule reserve (PV exported vs grid-served load),
  // largest first — the breakdown behind the headline schedule reserve.
  const scheduleMonths = useMemo(
    () =>
      months
        .map((m) => ({ month: m.month, reserve: reserveSplit(m.totals).elevator }))
        .filter((x) => x.reserve > 0)
        .sort((a, b) => b.reserve - a.reserve),
    [months],
  )

  // Per-month ESS dispatch reserve (optimum − fact), largest first.
  const bessMonths = useMemo(
    () =>
      months
        .filter((m) => m.totals.ess_optimum_uah > 0)
        .sort((a, b) => b.totals.ess_reserve_uah - a.totals.ess_reserve_uah),
    [months],
  )

  const [plan, warn] = panel.cards

  return (
    <section className="economics-card economics-month-section economics-ai" aria-label="AI-аналіз року">
      <div className="economics-ai-head">
        <div className="economics-ai-title">
          <span className="economics-ai-mark" aria-hidden="true">AI</span>
          <h3 className="economics-month-section-title">AI-аналіз року</h3>
        </div>
        <span className="economics-ai-badge">річний управлінський висновок</span>
      </div>

      <AiLead panel={panel} />

      <EssDataQualityNote dq={totals.ess_data_quality} />

      <div className="economics-ai-accordion">
        {plan ? (
          <details className="economics-ai-acc-item plan" open>
            <summary>
              <span className="economics-ai-status">{plan.status}</span>
              <span className="economics-ai-card-title">{plan.title}</span>
              <span className="economics-ai-impact">{plan.impact}</span>
            </summary>
            <div className="economics-ai-acc-body">
              <p className="economics-ai-action">{plan.action}</p>
              {scheduleMonths.length > 0 ? (
                <>
                  <div className="economics-ai-top-months-head">Резерв переносу робіт по місяцях</div>
                  <div className="economics-ai-days">
                    {scheduleMonths.map((x) => (
                      <span key={x.month}>
                        {formatMonthShort(x.month)} · {uahShort(x.reserve)}
                      </span>
                    ))}
                  </div>
                </>
              ) : null}
            </div>
          </details>
        ) : null}

        {warn ? (
          <details className="economics-ai-acc-item warn">
            <summary>
              <span className="economics-ai-status">{warn.status}</span>
              <span className="economics-ai-card-title">{warn.title}</span>
              <span className="economics-ai-impact">{warn.impact}</span>
            </summary>
            <div className="economics-ai-acc-body">
              <p className="economics-ai-action">{warn.action}</p>
              {bessMonths.length > 0 ? (
                <div className="economics-ai-reserve">
                  <div className="economics-ai-reserve-head">
                    <span className="economics-ai-label amber">
                      Резерв таймінгу УЗЕ по місяцях
                      <OptimumInfo tip="Оптимум − Факт за місяць. Це не збиток, а оцінка недовикористаної можливості УЗЕ (модельний максимум у межах фактичних потужності, SOC, ККД та зносу)." />
                    </span>
                    <div className="economics-optimum-legend">
                      <span><i style={{ background: '#7c3aed' }} />фактичний ефект</span>
                      <span><i style={{ background: '#f59e0b' }} />недовикористано</span>
                      <span><i style={{ background: '#e5e7eb' }} />оптимум</span>
                    </div>
                  </div>
                  <div className="economics-optimum-list">
                    <div className="economics-optimum-row head">
                      <span>Місяць</span>
                      <span>захоплення</span>
                      <span>опт.</span>
                      <span>факт</span>
                      <span>резерв</span>
                    </div>
                    {bessMonths.map((m) => {
                      const o = m.totals
                      const factShare = o.ess_optimum_uah > 0 ? Math.max(0, Math.min(1, o.ess_fact_uah / o.ess_optimum_uah)) : 0
                      return (
                        <div key={m.month} className="economics-optimum-row">
                          <strong>{formatMonthTitle(m.month)}</strong>
                          <div className="economics-capture-bar">
                            <span className="fact" style={{ width: `${factShare * 100}%` }} />
                            <span className="missed" style={{ width: `${(1 - factShare) * 100}%` }} />
                          </div>
                          <span>{formatUah(o.ess_optimum_uah)}</span>
                          <span className="good">{formatUah(o.ess_fact_uah)}</span>
                          <span className="amber">{formatUah(o.ess_reserve_uah)}</span>
                        </div>
                      )
                    })}
                  </div>
                  <p className="economics-month-muted economics-optimum-note">
                    Оптимум — найкращий диспетчинг у межах фактично продемонстрованих
                    можливостей УЗЕ (потужність, діапазон SOC, ККД виведені з даних кожного місяця).
                  </p>
                </div>
              ) : (
                <p className="economics-month-empty-note">Недостатньо активності УЗЕ за рік для оцінки оптимуму.</p>
              )}
            </div>
          </details>
        ) : null}
      </div>
    </section>
  )
}

// --- Per-month detail table + Excel export (SPEC §3.8) ---

const COLUMNS = [
  'Місяць',
  'РДН сер. (грн/кВт·год)',
  'СЕС (кВт·год)',
  'Споживання (кВт·год)',
  'Імпорт (кВт·год)',
  'Експорт (кВт·год)',
  'Самоспож. (%)',
  'УЗЕ цикли (екв.)',
  'EBITDA (грн)',
  'Ефект (грн)',
  'УЗЕ ефект (грн)',
]

function monthRowValues(m: EconomicsAnnualMonthRollup): string[] {
  const o = m.totals
  const pvSelf = o.pv_to_load_kwh + o.pv_to_ess_kwh
  const selfShare = o.pv_kwh > 0 ? pvSelf / o.pv_kwh : 0
  return [
    formatMonthName(m.month),
    formatPrice(o.rdn_avg_uah_per_kwh),
    formatKwh(o.pv_kwh),
    formatKwh(o.load_kwh),
    formatKwh(o.grid_import_kwh),
    formatKwh(o.grid_export_kwh),
    formatPercent(selfShare),
    formatCycles(o.equivalent_cycles),
    formatUah(o.ebitda_uah),
    formatUah(o.effect_uah),
    formatUah(o.ess_net_uah),
  ]
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function exportToExcel(months: EconomicsAnnualMonthRollup[], slug: string) {
  const head = `<tr>${COLUMNS.map((c) => `<th>${escapeHtml(c)}</th>`).join('')}</tr>`
  const body = months
    .map((m) => `<tr>${monthRowValues(m).map((v) => `<td>${escapeHtml(v)}</td>`).join('')}</tr>`)
    .join('')
  const html = `<!doctype html><html><head><meta charset="utf-8"></head><body><table>${head}${body}</table></body></html>`
  const blob = new Blob([html], { type: 'application/vnd.ms-excel;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `economics-annual-${slug}.xls`
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

function AnnualMonthlyTable({
  months,
  totals,
  organizationID,
  exportSlug,
  onSelectMonth,
}: {
  months: EconomicsAnnualMonthRollup[]
  totals: EconomicsMonthlyTotals
  organizationID: string
  exportSlug: string
  onSelectMonth: (month: string) => void
}) {
  const pvSelf = totals.pv_to_load_kwh + totals.pv_to_ess_kwh
  const selfShare = totals.pv_kwh > 0 ? pvSelf / totals.pv_kwh : 0
  return (
    <section id="economics-detail-table" className="economics-table-wrap" aria-label="Помісячна деталізація року">
      <div className="economics-month-section-head">
        <h3>
          Помісячна деталізація року
          <span className="economics-table-context"> · {formatOrganizationLabel(organizationID)}</span>
        </h3>
        <button type="button" className="economics-export-btn" onClick={() => exportToExcel(months, exportSlug)}>
          Вивантажити в Excel
        </button>
      </div>
      <div className="economics-table-scroll">
        <table className="economics-table economics-month-table">
          <thead>
            <tr>
              {COLUMNS.map((c) => (
                <th key={c}>{c}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {months.map((m) => {
              const o = m.totals
              return (
                <tr
                  key={m.month}
                  className="economics-row-clickable"
                  onClick={() => onSelectMonth(m.month)}
                  title="Відкрити місяць"
                >
                  <td className="economics-month-table-left">{formatMonthName(m.month)}</td>
                  <td>{formatPrice(o.rdn_avg_uah_per_kwh)}</td>
                  <td>{formatKwh(o.pv_kwh)}</td>
                  <td>{formatKwh(o.load_kwh)}</td>
                  <td>{formatKwh(o.grid_import_kwh)}</td>
                  <td>{formatKwh(o.grid_export_kwh)}</td>
                  <td>{formatPercent(o.pv_kwh > 0 ? (o.pv_to_load_kwh + o.pv_to_ess_kwh) / o.pv_kwh : 0)}</td>
                  <td>{formatCycles(o.equivalent_cycles)}</td>
                  <td className={signClass(o.ebitda_uah)}>{formatUah(o.ebitda_uah)}</td>
                  <td className={signClass(o.effect_uah)}>{formatUah(o.effect_uah)}</td>
                  <td>{formatUah(o.ess_net_uah)}</td>
                </tr>
              )
            })}
            <tr className="economics-table-summary-row">
              <td className="economics-month-table-left">Разом</td>
              <td>{formatPrice(totals.rdn_avg_uah_per_kwh)}</td>
              <td>{formatKwh(totals.pv_kwh)}</td>
              <td>{formatKwh(totals.load_kwh)}</td>
              <td>{formatKwh(totals.grid_import_kwh)}</td>
              <td>{formatKwh(totals.grid_export_kwh)}</td>
              <td>{formatPercent(selfShare)}</td>
              <td>{formatCycles(totals.equivalent_cycles)}</td>
              <td className={signClass(totals.ebitda_uah)}>{formatUah(totals.ebitda_uah)}</td>
              <td className={signClass(totals.effect_uah)}>{formatUah(totals.effect_uah)}</td>
              <td>{formatUah(totals.ess_net_uah)}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  )
}
