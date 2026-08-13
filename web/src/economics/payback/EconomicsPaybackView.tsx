import { useMemo, useState, type ReactNode } from 'react'
import {
  Area,
  Bar,
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
import type { EconomicsAnnualResponse } from '../../api'
import { OptimumInfo } from '../monthly/EconomicsMonthlyView'
import {
  formatMonthTitle,
  formatMonthShort,
  formatMwh,
  formatPercent,
  formatPrice,
  formatUah,
} from '../monthly/format'
import {
  addMonths,
  buildPaybackModel,
  type CapexPaybackRow,
  type CapexStep,
  moneyAxis,
  paybackAxis,
  paybackLabel,
} from '../payback'

// The standalone "Окупність проєкту" page: an investment report over
// the whole operating history. The page always fetches an all-time
// window, so `data` covers every month with telemetry and prior_*
// carries anything even earlier. Layout follows the operator-approved
// mockup (dashboard_okupnosti_SES).

type Props = {
  data: EconomicsAnnualResponse
  // capexUah is the flat value from the tariff form; capexSteps is the
  // dated CAPEX from the tariff schedule and wins whenever it is filled
  // in, so a project built in stages is measured against what was
  // invested by each month rather than against its final cost.
  capexUah: number
  capexSteps: CapexStep[]
  // plannedPaybackMonths is the business-plan payback period (months);
  // 0 hides the plan-vs-forecast comparison.
  plannedPaybackMonths: number
}

// monthTitleLower renders "травень 2035" (lowercase month + year) for
// inline notes and chart labels, per the mockup copy.
function monthTitleLower(monthKey: string): string {
  const title = formatMonthTitle(monthKey)
  return title.charAt(0).toLocaleLowerCase('uk-UA') + title.slice(1)
}

// --- Small inline icons for the KPI cards / sidebar rows ---

function Icon({ name }: { name: IconName }) {
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      {ICON_PATHS[name]}
    </svg>
  )
}

type IconName = 'bars' | 'wallet' | 'pie' | 'percent' | 'calendar' | 'trend' | 'clock' | 'sun' | 'tag' | 'coin'

const ICON_PATHS: Record<IconName, ReactNode> = {
  bars: (
    <>
      <path d="M4 20v-6" />
      <path d="M9 20V9" />
      <path d="M14 20v-8" />
      <path d="M19 20V5" />
    </>
  ),
  wallet: (
    <>
      <rect x="3" y="6" width="18" height="13" rx="2" />
      <path d="M3 10h18" />
      <path d="M16 15h2" />
    </>
  ),
  pie: (
    <>
      <path d="M12 3a9 9 0 1 0 9 9h-9z" />
      <path d="M15 3.5A9 9 0 0 1 20.5 9H15z" />
    </>
  ),
  percent: (
    <>
      <path d="M19 5 5 19" />
      <circle cx="7" cy="7" r="2.5" />
      <circle cx="17" cy="17" r="2.5" />
    </>
  ),
  calendar: (
    <>
      <rect x="3" y="5" width="18" height="16" rx="2" />
      <path d="M8 3v4M16 3v4M3 10h18" />
    </>
  ),
  trend: (
    <>
      <path d="M3 17l6-6 4 4 8-8" />
      <path d="M15 7h6v6" />
    </>
  ),
  clock: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 3" />
    </>
  ),
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1 7 17M17 7l2.1-2.1" />
    </>
  ),
  tag: (
    <>
      <path d="M3 12V4h8l9 9-8 8-9-9z" />
      <circle cx="7.5" cy="8.5" r="1.5" />
    </>
  ),
  coin: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M9.5 15c.6.8 1.6 1.3 2.7 1.3 1.7 0 3-.9 3-2.3 0-2.9-5.9-1.5-5.9-4.1 0-1.3 1.2-2.2 2.8-2.2 1 0 2 .4 2.6 1.1M12 6v1.7M12 16.3V18" />
    </>
  ),
}

type Tone = 'blue' | 'green' | 'orange' | 'violet'

function KpiCard({
  icon,
  tone,
  label,
  value,
  note,
  valueClass,
}: {
  icon: IconName
  tone: Tone
  label: string
  value: string
  note: string
  valueClass?: string
}) {
  return (
    <div className="economics-payback-kpi">
      <span className={`economics-payback-kpi-icon economics-payback-tone-${tone}`}>
        <Icon name={icon} />
      </span>
      <span className="economics-payback-kpi-body">
        <span className="economics-payback-kpi-label">{label}</span>
        <span className={`economics-payback-kpi-value ${valueClass ?? ''}`}>{value}</span>
        <span className="economics-payback-kpi-note">{note}</span>
      </span>
    </div>
  )
}

function SideRow({
  icon,
  tone,
  label,
  value,
  note,
}: {
  icon: IconName
  tone: Tone
  label: string
  value: string
  note?: string
}) {
  return (
    <div className="economics-payback-side-row">
      <span className={`economics-payback-side-icon economics-payback-tone-${tone}`}>
        <Icon name={icon} />
      </span>
      <span className="economics-payback-side-body">
        <span className="economics-payback-side-label">{label}</span>
        <span className="economics-payback-side-value">{value}</span>
        {note ? <span className="economics-payback-side-note">{note}</span> : null}
      </span>
    </div>
  )
}

// --- Cumulative payback chart tooltip ---

type CapexPaybackTooltipProps = {
  active?: boolean
  payload?: { payload: CapexPaybackRow }[]
  // staged adds the CAPEX row: with investments in stages, "накопичено"
  // alone does not say which target the month was measured against.
  staged?: boolean
}

function CapexPaybackTooltip({ active, payload, staged }: CapexPaybackTooltipProps) {
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
      {staged ? (
        <div className="economics-trend-tip-row">
          <i style={{ background: '#64748b' }} />
          <span>CAPEX на цей місяць</span>
          <b>{formatUah(row.capex)}</b>
        </div>
      ) : null}
      {row.kind === 'prior' ? (
        <div className="economics-month-muted">накопичений EBITDA до помісячної деталізації</div>
      ) : null}
    </div>
  )
}

// --- Monthly effect chart (fact + forecast to the year end) ---

type EffectGrain = 'month' | 'quarter' | 'year'

type EffectRow = {
  label: string
  periodFact: number | null
  periodForecast: number | null
  // cum is one continuous fact+forecast cumulative line (per the final
  // design); the split lives in the bars.
  cum: number
}

type EffectTooltipProps = {
  active?: boolean
  payload?: { payload: EffectRow }[]
}

function EffectTooltip({ active, payload }: EffectTooltipProps) {
  if (!active || !payload?.length) return null
  const row = payload[0].payload
  return (
    <div className="economics-trend-tip">
      <div className="economics-trend-tip-day">{row.label}</div>
      {row.periodFact !== null ? (
        <div className="economics-trend-tip-row">
          <i style={{ background: '#60a5fa' }} />
          <span>EBITDA (факт)</span>
          <b>{formatUah(row.periodFact)}</b>
        </div>
      ) : null}
      {row.periodForecast !== null ? (
        <div className="economics-trend-tip-row">
          <i style={{ background: '#c4b5fd' }} />
          <span>EBITDA (прогноз)</span>
          <b>{formatUah(row.periodForecast)}</b>
        </div>
      ) : null}
      <div className="economics-trend-tip-row">
        <i style={{ background: '#2f6fed' }} />
        <span>Накопичено</span>
        <b>{formatUah(row.cum)}</b>
      </div>
    </div>
  )
}

// uahShortHrn is the design's compact money label ("18,50 млн грн").
function uahShortHrn(v: number): string {
  const a = Math.abs(v)
  if (a >= 1_000_000) {
    return `${(v / 1_000_000).toLocaleString('uk-UA', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} млн грн`
  }
  if (a >= 1000) {
    return `${Math.round(v / 1000).toLocaleString('uk-UA')} тис. грн`
  }
  return formatUah(v)
}

// investmentStagesNote is the KPI caption for a staged CAPEX ("3 етапи
// інвестицій"), with the Ukrainian plural the count needs.
function investmentStagesNote(n: number): string {
  const tens = n % 100
  const last = n % 10
  const word =
    tens >= 12 && tens <= 14 ? 'етапів' : last === 1 ? 'етап' : last >= 2 && last <= 4 ? 'етапи' : 'етапів'
  return `${n} ${word} інвестицій`
}

const Q_ROMAN = ['I', 'II', 'III', 'IV']

export function EconomicsPaybackView({ data, capexUah, capexSteps, plannedPaybackMonths }: Props) {
  const [grain, setGrain] = useState<EffectGrain>('month')

  const model = useMemo(
    () =>
      buildPaybackModel({
        capexUah,
        capexSteps,
        months: data.months,
        ebitda: data.totals.ebitda_uah,
        priorEbitda: data.prior_ebitda_uah,
        priorMonthsWithData: data.prior_months_with_data,
      }),
    [capexUah, capexSteps, data],
  )
  const {
    monthsWithData,
    prior,
    hasPrior,
    allTimeEbitda,
    monthlyPace,
    seasonalFactors,
    capexNow,
    capexStages,
    paybackYears,
    coveredShare,
    remaining,
    operationYears,
    avgAnnualRoi,
    paidOff,
    rows: paybackRows,
    todayT,
    paybackT,
    paybackMonthKey,
    tMax,
    timeOffset,
    firstMonthKey,
    lastFactMonthKey,
  } = model

  // A staged CAPEX is drawn as a staircase (and explained in the
  // tooltip); a single-stage project keeps the plain horizontal line.
  const staged = capexStages > 1
  const capexMax = Math.max(capexNow, ...paybackRows.map((r) => r.capex))
  // The payback marker sits on the CAPEX line, which may have stepped up
  // by the month the projection crosses it.
  const paybackCapex = paybackRows.find((r) => r.monthKey === paybackMonthKey)?.capex ?? capexNow

  const { ticks: timeTicks, format: tickLabel } = paybackAxis(tMax, firstMonthKey, timeOffset)
  const yMax = Math.max(capexMax, allTimeEbitda, ...paybackRows.map((r) => r.forecastCum ?? r.factCum ?? 0), 0)
  const axis = moneyAxis(yMax)
  // Main-chart Y ticks carry the unit right in the label ("15 млн"),
  // like the final design.
  const axisUnitWord = axis.unit.split(' ')[0]
  const mainTick = (v: number) => (v === 0 ? '0' : `${axis.tick(v)} ${axisUnitWord}`)
  const startRow = paybackRows.find((r) => r.kind === 'start') ?? null

  // "Дані оновлено": the last day of the last fact month, capped at
  // today for the open (partial) month.
  const updatedLabel = useMemo(() => {
    if (!lastFactMonthKey) return '—'
    const [y, m] = lastFactMonthKey.split('-').map(Number)
    const endOfMonth = new Date(y, m, 0)
    const today = new Date()
    const d = endOfMonth < today ? endOfMonth : today
    return d.toLocaleDateString('uk-UA', { day: '2-digit', month: '2-digit', year: 'numeric' })
  }, [lastFactMonthKey])

  // Per data-covered month, so partial months don't drag the average.
  const avgMonthlyFact = model.effectiveMonths > 0 ? allTimeEbitda / model.effectiveMonths : NaN

  // Effect chart entries: every fact month plus a seasonal forecast for
  // the rest of the last fact month's calendar year (like the mockup's
  // "fact Jan–Jul, forecast Aug–Dec").
  const effectRows = useMemo<EffectRow[]>(() => {
    type Entry = { month: string; value: number; forecast: boolean }
    const entries: Entry[] = monthsWithData.map((m) => ({
      month: m.month,
      value: m.totals.ebitda_uah,
      forecast: false,
    }))
    if (lastFactMonthKey && !paidOff && monthlyPace > 0) {
      const year = lastFactMonthKey.slice(0, 4)
      let cursor = addMonths(lastFactMonthKey, 1)
      while (cursor.slice(0, 4) === year) {
        entries.push({
          month: cursor,
          value: monthlyPace * seasonalFactors[Number(cursor.slice(5, 7)) - 1],
          forecast: true,
        })
        cursor = addMonths(cursor, 1)
      }
    }
    if (entries.length === 0) return []

    type Bucket = { label: string; order: string; fact: number; forecast: number; hasFact: boolean; hasForecast: boolean }
    const map = new Map<string, Bucket>()
    for (const e of entries) {
      const y = e.month.slice(0, 4)
      const key =
        grain === 'month' ? e.month : grain === 'quarter' ? `${y}-Q${Math.ceil(Number(e.month.slice(5, 7)) / 3)}` : y
      const label =
        grain === 'month'
          ? e.month.slice(5, 7) === '01' || e.month === entries[0].month
            ? `${formatMonthShort(e.month)} ${y}`
            : formatMonthShort(e.month)
          : grain === 'quarter'
            ? `${Q_ROMAN[Math.ceil(Number(e.month.slice(5, 7)) / 3) - 1]} кв. ${y}`
            : y
      const cur = map.get(key) ?? { label, order: key, fact: 0, forecast: 0, hasFact: false, hasForecast: false }
      if (e.forecast) {
        cur.forecast += e.value
        cur.hasForecast = true
      } else {
        cur.fact += e.value
        cur.hasFact = true
      }
      map.set(key, cur)
    }
    const buckets = [...map.values()].sort((a, b) => a.order.localeCompare(b.order))

    let acc = prior
    return buckets.map((b) => {
      acc += b.fact + b.forecast
      return {
        label: b.label,
        periodFact: b.hasFact ? b.fact : null,
        periodForecast: b.hasForecast ? b.forecast : null,
        cum: acc,
      }
    })
  }, [monthsWithData, lastFactMonthKey, paidOff, monthlyPace, seasonalFactors, grain, prior])

  const effectAxis = moneyAxis(
    Math.max(...effectRows.map((r) => Math.abs(r.periodFact ?? 0) + Math.abs(r.periodForecast ?? 0)), 0),
  )
  const cumAxis = moneyAxis(Math.max(...effectRows.map((r) => Math.abs(r.cum)), 0))

  const paybackDurationLabel = paidOff ? 'окуплено' : paybackLabel(paybackYears)
  // A finite forecast without a crossing month means the projection runs
  // past the drawn horizon — the estimate exists, just no exact date.
  const paybackDateNote = paybackMonthKey
    ? monthTitleLower(paybackMonthKey)
    : Number.isFinite(paybackYears)
      ? 'за поточним темпом EBITDA'
      : 'недостатньо даних'

  // Share of the fact window actually covered by telemetry hours; below
  // ~95% the forecast is built on partial months.
  const coverageShare = model.totalMonthsWithData > 0 ? model.effectiveMonths / model.totalMonthsWithData : NaN

  // Plan-vs-forecast deviation (months); positive = slower than plan.
  const planMonths = Number.isFinite(plannedPaybackMonths) && plannedPaybackMonths > 0 ? plannedPaybackMonths : 0
  const forecastMonths = Number.isFinite(paybackYears) ? paybackYears * 12 : NaN
  const deviationMonths = planMonths > 0 && Number.isFinite(forecastMonths) ? Math.round(forecastMonths - planMonths) : null

  if (!(capexNow > 0)) {
    return (
      <section className="economics-card economics-month-section" aria-label="Окупність проєкту">
        <div className="economics-month-section-head">
          <h3 className="economics-month-section-title">Окупність проєкту</h3>
        </div>
        <p className="economics-month-muted">
          Вкажіть CAPEX проєкту в «Параметри тарифів» — або окремими сумами в колонці CAPEX у версіях
          тарифів, якщо інвестували етапами, — щоб побачити звіт про окупність інвестицій.
        </p>
      </section>
    )
  }

  return (
    <>
      <div className="economics-payback-head">
        <h2>
          Окупність проєкту СЕС
          <OptimumInfo tip="CAPEX береться з колонки CAPEX у версіях тарифів: якщо проєкт інвестували етапами, планка окупності зростає разом із вкладеннями, а місяць порівнюється з тією сумою, що була вкладена на той час. Коли у версіях CAPEX не заповнений, діє одне значення з «Параметрів тарифів». Повернуто = накопичений EBITDA з початку експлуатації; середньорічний ROI рахується від середніх вкладень за період. Прогноз окупності будується помісячно з урахуванням сезонності виробітку: темп EBITDA оцінюється за фактом із поправкою на сезон, а майбутні місяці отримують свій сезонний коефіцієнт (літо швидше, зима повільніше)." />
        </h2>
        <span className="economics-month-muted">Дані оновлено: {updatedLabel}</span>
      </div>

      <section className="economics-payback-kpis" aria-label="Ключові показники окупності">
        <KpiCard
          icon="bars"
          tone="blue"
          label="CAPEX проєкту"
          value={formatUah(capexNow)}
          note={staged ? investmentStagesNote(capexStages) : 'разові інвестиції'}
        />
        <KpiCard
          icon="wallet"
          tone="green"
          label="Повернуто інвестицій"
          value={formatUah(allTimeEbitda)}
          note="накопичений EBITDA"
          valueClass={allTimeEbitda >= 0 ? 'good' : ''}
        />
        <KpiCard
          icon="pie"
          tone="orange"
          label="Залишилось повернути"
          value={paidOff ? '0 грн' : formatUah(remaining)}
          note={paidOff ? 'капекс окуплено' : 'ще потрібно повернути'}
        />
        <KpiCard
          icon="percent"
          tone="green"
          label="Окуплено"
          value={formatPercent(coveredShare)}
          note="від CAPEX"
          valueClass="good"
        />
        <KpiCard
          icon="calendar"
          tone="blue"
          label="Прогноз окупності"
          value={paybackDurationLabel}
          note={paidOff ? 'інвестиції повернуто' : paybackDateNote}
        />
      </section>

      <div className="economics-payback-layout">
        <div className="economics-payback-main">
          <section className="economics-card economics-payback-progress" aria-label="Прогрес окупності">
            <div className="economics-payback-progress-line">
              <span className="economics-payback-side-icon economics-payback-tone-blue">
                <Icon name="coin" />
              </span>
              <span className="economics-payback-progress-caption">
                Повернуто <b>{uahShortHrn(allTimeEbitda)}</b> із <b>{uahShortHrn(capexNow)}</b>
              </span>
              <div className="economics-capex-bar economics-payback-bar">
                <span className="economics-capex-bar-fill" style={{ width: `${coveredShare * 100}%` }} />
              </div>
              <span className="economics-payback-progress-pct">{formatPercent(coveredShare)}</span>
              <span className="economics-payback-progress-rest">
                {paidOff ? 'Капекс окуплено' : `Залишилось повернути ${uahShortHrn(remaining)}`}
              </span>
            </div>
          </section>

          <section className="economics-card economics-month-section" aria-label="Накопичена окупність проєкту">
            <div className="economics-capex-chart-head">
              <span className="economics-capex-chart-title">
                Накопичена окупність проєкту
                <span className="economics-capex-chart-unit">{axis.unit}</span>
                <OptimumInfo
                  tip={
                    staged
                      ? 'Суцільна лінія — фактичний накопичений EBITDA від початку експлуатації. Пунктир — сезонний прогноз: кожен майбутній місяць додає свій очікуваний EBITDA (влітку більше, взимку менше). Сіра сходинка — CAPEX, що діяв у кожному місяці: проєкт інвестували етапами, тому планка окупності підіймається разом із вкладеннями.'
                      : 'Суцільна лінія — фактичний накопичений EBITDA від початку експлуатації. Пунктир — сезонний прогноз: кожен майбутній місяць додає свій очікуваний EBITDA (влітку більше, взимку менше), тому лінія хвиляста, як і факт. Вертикаль «Сьогодні» — остання фактична точка; точка окупності — прогнозований місяць повного повернення інвестицій.'
                  }
                />
              </span>
              <span className="economics-month-legend">
                <span><i style={{ background: '#2f6fed' }} />Фактичний накопичений EBITDA</span>
                <span><i style={{ background: '#60a5fa' }} />Прогноз накопиченого EBITDA</span>
                <span>
                  <i style={{ background: '#64748b' }} />
                  CAPEX ({uahShortHrn(capexNow)}
                  {staged ? ', етапами' : ''})
                </span>
                <span><i style={{ background: '#7c3aed' }} />Точка окупності (прогноз)</span>
              </span>
            </div>
          <ResponsiveContainer width="100%" height={320}>
            <ComposedChart data={paybackRows} margin={{ top: 18, right: 18, bottom: 0, left: 0 }}>
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
                width={56}
                tickLine={false}
                axisLine={false}
                tickFormatter={mainTick}
                domain={[0, (dataMax: number) => Math.max(dataMax, capexMax) * 1.08]}
              />
              <Tooltip content={<CapexPaybackTooltip staged={staged} />} cursor={{ stroke: '#cbd5e1' }} />
              {staged ? (
                <Line
                  type="stepAfter"
                  dataKey="capex"
                  stroke="#64748b"
                  strokeWidth={1.4}
                  dot={false}
                  activeDot={false}
                  isAnimationActive={false}
                />
              ) : (
                <ReferenceLine
                  y={capexNow}
                  stroke="#64748b"
                  strokeWidth={1.4}
                  label={{ value: `CAPEX ${uahShortHrn(capexNow)}`, position: 'insideTopLeft', fontSize: 11, fill: '#475569' }}
                />
              )}
              {todayT !== null && todayT > 0 ? (
                <ReferenceLine
                  x={todayT}
                  stroke="#64748b"
                  strokeWidth={1.2}
                  strokeDasharray="4 4"
                  // The fact window is often a thin slice at the left of a
                  // multi-year axis, so the caption goes to the right of
                  // the line where there is room (for a vertical reference
                  // line "insideTopLeft" anchors the text start at the
                  // line, extending rightwards).
                  label={{ value: 'Сьогодні', position: 'insideTopLeft', fontSize: 11, fill: '#475569', dx: 4 }}
                />
              ) : null}
              {paybackT !== null ? (
                <ReferenceLine x={paybackT} stroke="#7c3aed" strokeDasharray="4 4" />
              ) : null}
              <Area type="monotone" dataKey="factCum" stroke="none" fill="#2f6fed" fillOpacity={0.09} />
              <Line
                type="monotone"
                dataKey="factCum"
                stroke="#2f6fed"
                strokeWidth={2.2}
                dot={{ r: 2.4, fill: '#2f6fed', strokeWidth: 0 }}
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
              {startRow ? (
                <ReferenceDot
                  x={startRow.t}
                  y={startRow.factCum ?? 0}
                  r={3.5}
                  fill="#12b76a"
                  stroke="#fff"
                  strokeWidth={1.5}
                  label={{ value: 'Початок експлуатації', position: 'right', fontSize: 10, fill: '#12b76a' }}
                />
              ) : null}
              {todayT !== null && todayT > 0 ? (
                <ReferenceDot
                  x={todayT}
                  y={allTimeEbitda}
                  r={4}
                  fill="#2f6fed"
                  stroke="#fff"
                  strokeWidth={1.5}
                  label={{ value: `Повернуто ${uahShortHrn(allTimeEbitda)}`, position: 'top', fontSize: 11, fill: '#2f6fed', dy: -6 }}
                />
              ) : null}
              {paybackT !== null ? (
                <ReferenceDot x={paybackT} y={paybackCapex} r={4.5} fill="#7c3aed" stroke="#fff" strokeWidth={1.5} />
              ) : null}
              {paybackT !== null ? (
                // Invisible anchor that hangs the "Точка окупності" caption
                // at the bottom of the payback line, to its left (the line
                // usually sits at the right edge of the chart).
                <ReferenceDot
                  x={paybackT}
                  y={0}
                  r={0}
                  fill="none"
                  stroke="none"
                  label={{
                    value: paybackMonthKey
                      ? `Точка окупності · ${monthTitleLower(paybackMonthKey)}`
                      : 'Точка окупності',
                    position: 'left',
                    fontSize: 11,
                    fill: '#7c3aed',
                  }}
                />
              ) : null}
            </ComposedChart>
          </ResponsiveContainer>

          <div className="economics-payback-chart-foot">
            {hasPrior ? (
              <SideRow
                icon="clock"
                tone="blue"
                label="Накопичений EBITDA до періоду"
                value={formatUah(prior)}
                note={`до початку помісячних даних (${model.priorMonths} міс.)`}
              />
            ) : null}
            <SideRow
              icon="calendar"
              tone="green"
              label="Період експлуатації"
              value={paybackLabel(operationYears)}
              note={
                Number.isFinite(coverageShare)
                  ? `покриття даними ${formatPercent(coverageShare)}`
                  : 'з початку експлуатації'
              }
            />
            <SideRow
              icon="coin"
              tone="orange"
              label="Середньомісячний EBITDA (факт)"
              value={Number.isFinite(avgMonthlyFact) ? formatUah(avgMonthlyFact) : '—'}
              note="за фактичний період"
            />
          </div>
          </section>

          <section className="economics-card economics-month-section" aria-label="Щомісячний економічний ефект">
          <div className="economics-capex-chart-head">
            <span className="economics-capex-chart-title">
              Щомісячний економічний ефект
              <span className="economics-capex-chart-unit">{effectAxis.unit}</span>
              <OptimumInfo tip="Стовпці — EBITDA за період: сині — факт, фіолетові — сезонний прогноз до кінця поточного року. Лінія — накопичений EBITDA від початку експлуатації (права шкала), факт + прогноз." />
            </span>
            <div className="economics-payback-effect-tools">
              <span className="economics-month-legend">
                <span><i style={{ background: '#60a5fa' }} />EBITDA за місяць (факт)</span>
                <span><i style={{ background: '#c4b5fd' }} />EBITDA за місяць (прогноз)</span>
                <span><i style={{ background: '#2f6fed' }} />Накопичений EBITDA (факт + прогноз)</span>
              </span>
              <label className="economics-payback-grain">
                Відображення:
                <select value={grain} onChange={(e) => setGrain(e.target.value as EffectGrain)}>
                  <option value="month">Місяці</option>
                  <option value="quarter">Квартали</option>
                  <option value="year">Роки</option>
                </select>
              </label>
            </div>
          </div>
          <ResponsiveContainer width="100%" height={240}>
            <ComposedChart data={effectRows} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
              <CartesianGrid strokeDasharray="2 5" stroke="#e7ecf2" vertical={false} />
              <XAxis
                dataKey="label"
                tick={{ fontSize: 11, fill: '#8a94a6' }}
                tickLine={false}
                axisLine={false}
                interval={effectRows.length > 16 ? Math.ceil(effectRows.length / 16) - 1 : 0}
              />
              <YAxis
                yAxisId="period"
                tick={{ fontSize: 11, fill: '#98a2b3' }}
                width={40}
                tickLine={false}
                axisLine={false}
                tickFormatter={effectAxis.tick}
              />
              <YAxis
                yAxisId="cum"
                orientation="right"
                tick={{ fontSize: 11, fill: '#98a2b3' }}
                width={40}
                tickLine={false}
                axisLine={false}
                tickFormatter={cumAxis.tick}
              />
              <Tooltip content={<EffectTooltip />} cursor={{ fill: 'rgba(148, 163, 184, 0.12)' }} />
              <Bar
                yAxisId="period"
                dataKey="periodFact"
                stackId="effect"
                fill="#60a5fa"
                fillOpacity={0.85}
                maxBarSize={26}
                radius={[3, 3, 0, 0]}
              />
              <Bar
                yAxisId="period"
                dataKey="periodForecast"
                stackId="effect"
                fill="#c4b5fd"
                fillOpacity={0.9}
                maxBarSize={26}
                radius={[3, 3, 0, 0]}
              />
              <Line
                yAxisId="cum"
                type="monotone"
                dataKey="cum"
                stroke="#2f6fed"
                strokeWidth={1.8}
                dot={{ r: 2.4, fill: '#fff', stroke: '#2f6fed', strokeWidth: 1.4 }}
              />
            </ComposedChart>
          </ResponsiveContainer>
          </section>
        </div>

        <div className="economics-payback-side-stack">
          <aside className="economics-card economics-month-section economics-payback-side" aria-label="Ефективність проєкту">
            <h3 className="economics-payback-side-title">Ефективність проєкту</h3>
            <SideRow
              icon="trend"
              tone="green"
              label="Середньорічний ROI (факт)"
              value={Number.isFinite(avgAnnualRoi) ? formatPercent(avgAnnualRoi) : '—'}
              note={`за ${paybackLabel(operationYears)} експлуатації`}
            />
            <SideRow
              icon="sun"
              tone="blue"
              label="Виробіток за період"
              value={formatMwh(data.totals.pv_kwh)}
              note="за місяці з даними телеметрії"
            />
            <SideRow
              icon="tag"
              tone="orange"
              label="Середній тариф (екв.)"
              value={`${formatPrice(data.totals.avg_import_price_uah_per_kwh)} грн/кВт·год`}
            />
            <SideRow
              icon="wallet"
              tone="violet"
              label="Витрати на придбання електроенергії з мережі"
              value={formatUah(data.totals.expense_total_uah)}
              note="заряд УЗЕ з мережі за період"
            />
            <SideRow icon="percent" tone="green" label="Чистий ефект (EBITDA)" value={formatUah(allTimeEbitda)} />
          </aside>

          <aside
            className="economics-card economics-month-section economics-payback-side"
            aria-label="Відхилення від бізнес-плану"
          >
            <h3 className="economics-payback-side-title">
              Відхилення від бізнес-плану
              <OptimumInfo tip="Порівняння поточного прогнозу окупності з плановим терміном із бізнес-плану (поле «Бізнес-план окупності» в параметрах тарифів)." />
            </h3>
            {planMonths > 0 && deviationMonths !== null ? (
              <>
                <div className="economics-payback-dev-row">
                  <span>Початкова окупність (план)</span>
                  <b>{paybackLabel(planMonths / 12)}</b>
                </div>
                <div className="economics-payback-dev-row">
                  <span>Поточний прогноз окупності</span>
                  <b>{paybackDurationLabel}</b>
                </div>
                <div
                  className={`economics-payback-dev-verdict ${deviationMonths > 0 ? 'bad' : deviationMonths < 0 ? 'good' : ''}`}
                >
                  <span>Відхилення</span>
                  <span className="economics-payback-dev-verdict-value">
                    <b>
                      {deviationMonths === 0
                        ? 'за планом'
                        : `${deviationMonths > 0 ? '+' : '−'}${Math.abs(deviationMonths)} міс.`}
                    </b>
                    {deviationMonths !== 0 ? (
                      <em>{deviationMonths > 0 ? 'погіршення від плану' : 'покращення від плану'}</em>
                    ) : null}
                  </span>
                </div>
              </>
            ) : (
              <p className="economics-month-muted">
                Вкажіть «Бізнес-план окупності» в параметрах тарифів, щоб порівняти прогноз із планом.
              </p>
            )}
          </aside>

          <aside className="economics-card economics-month-section economics-payback-side" aria-label="Примітка">
            <h3 className="economics-payback-side-title">
              <span className="economics-payback-note-icon">i</span>
              Примітка
            </h3>
            <p className="economics-payback-note">
              Прогноз базується на фактичних даних за {model.totalMonthsWithData} міс. експлуатації з
              урахуванням сезонності сонячного виробітку та може змінюватися залежно від фактичного виробітку
              електроенергії, цін РДН, тарифів, операційних витрат та інших факторів.
            </p>
          </aside>
        </div>
      </div>
    </>
  )
}
