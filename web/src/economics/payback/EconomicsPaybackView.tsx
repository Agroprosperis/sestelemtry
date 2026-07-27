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
import { CapexPaybackTooltip } from '../annual/EconomicsAnnualView'
import { OptimumInfo } from '../monthly/EconomicsMonthlyView'
import {
  formatKwh,
  formatMonthTitle,
  formatMonthShort,
  formatPercent,
  formatPrice,
  formatUah,
} from '../monthly/format'
import { uahShort } from '../monthly/rollup'
import {
  addMonths,
  buildPaybackModel,
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
  capexUah: number
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

// KvRow is the compact one-line "label … value" row of the
// "Додаткові показники" panel.
function KvRow({ icon, tone, label, value }: { icon: IconName; tone: Tone; label: string; value: string }) {
  return (
    <div className="economics-payback-kv-row">
      <span className="economics-payback-kv-label">
        <span className={`economics-payback-kv-icon economics-payback-tone-${tone}`}>
          <Icon name={icon} />
        </span>
        {label}
      </span>
      <b>{value}</b>
    </div>
  )
}

// --- Monthly effect chart (fact + forecast to the year end) ---

type EffectGrain = 'month' | 'quarter' | 'year'

type EffectRow = {
  label: string
  periodFact: number | null
  periodForecast: number | null
  // cumFact / cumForecast split the cumulative line so the forecast part
  // renders in the forecast colour; the bucket at the seam carries both.
  cumFact: number | null
  cumForecast: number | null
}

type EffectTooltipProps = {
  active?: boolean
  payload?: { payload: EffectRow }[]
}

function EffectTooltip({ active, payload }: EffectTooltipProps) {
  if (!active || !payload?.length) return null
  const row = payload[0].payload
  const cum = row.cumForecast ?? row.cumFact
  const isForecast = row.periodForecast !== null
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
        <i style={{ background: isForecast ? '#7c3aed' : '#2f6fed' }} />
        <span>Накопичено</span>
        <b>{cum === null ? '—' : formatUah(cum)}</b>
      </div>
    </div>
  )
}

const Q_ROMAN = ['I', 'II', 'III', 'IV']

export function EconomicsPaybackView({ data, capexUah, plannedPaybackMonths }: Props) {
  const [grain, setGrain] = useState<EffectGrain>('month')

  const model = useMemo(
    () =>
      buildPaybackModel({
        capexUah,
        months: data.months,
        ebitda: data.totals.ebitda_uah,
        priorEbitda: data.prior_ebitda_uah,
        priorMonthsWithData: data.prior_months_with_data,
      }),
    [capexUah, data],
  )
  const {
    monthsWithData,
    prior,
    hasPrior,
    allTimeEbitda,
    monthlyPace,
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
    scenario,
  } = model

  const { ticks: timeTicks, format: tickLabel } = paybackAxis(tMax, firstMonthKey, timeOffset)
  const yMax = Math.max(capexUah, allTimeEbitda, ...paybackRows.map((r) => r.forecastCum ?? r.factCum ?? 0), 0)
  const axis = moneyAxis(yMax)
  const startRow = paybackRows.find((r) => r.kind === 'start') ?? null

  // Effect chart entries: every fact month plus a run-rate forecast for
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
        entries.push({ month: cursor, value: monthlyPace, forecast: true })
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
        cumFact: !b.hasForecast ? acc : null,
        cumForecast: b.hasForecast ? acc : null,
      }
    })
  }, [monthsWithData, lastFactMonthKey, paidOff, monthlyPace, grain, prior])

  // Seed the forecast cumulative line from the last fact bucket so the
  // two line segments join without a gap.
  const effectRowsBridged = useMemo(() => {
    const rows = effectRows.map((r) => ({ ...r }))
    const firstForecast = rows.findIndex((r) => r.cumForecast !== null)
    if (firstForecast > 0) rows[firstForecast - 1].cumForecast = rows[firstForecast - 1].cumFact
    return rows
  }, [effectRows])

  const effectAxis = moneyAxis(
    Math.max(
      ...effectRowsBridged.map((r) => Math.abs(r.periodFact ?? 0) + Math.abs(r.periodForecast ?? 0)),
      0,
    ),
  )
  const cumAxis = moneyAxis(Math.max(...effectRowsBridged.map((r) => Math.abs(r.cumForecast ?? r.cumFact ?? 0)), 0))

  const paybackDurationLabel = paidOff ? 'окуплено' : paybackLabel(paybackYears)
  const paybackDateNote = paybackMonthKey ? monthTitleLower(paybackMonthKey) : 'недостатньо даних'

  // Plan-vs-forecast deviation (months); positive = slower than plan.
  const planMonths = Number.isFinite(plannedPaybackMonths) && plannedPaybackMonths > 0 ? plannedPaybackMonths : 0
  const forecastMonths = Number.isFinite(paybackYears) ? paybackYears * 12 : NaN
  const deviationMonths = planMonths > 0 && Number.isFinite(forecastMonths) ? Math.round(forecastMonths - planMonths) : null

  if (!(capexUah > 0)) {
    return (
      <section className="economics-card economics-month-section" aria-label="Окупність проєкту">
        <div className="economics-month-section-head">
          <h3 className="economics-month-section-title">Окупність проєкту</h3>
        </div>
        <p className="economics-month-muted">
          Вкажіть CAPEX проєкту в «Параметри тарифів», щоб побачити звіт про окупність інвестицій.
        </p>
      </section>
    )
  }

  return (
    <>
      <div className="economics-payback-head">
        <h2>
          Окупність проєкту СЕС
          <OptimumInfo tip="CAPEX — разові капітальні інвестиції з налаштувань об'єкта. Повернуто = накопичений EBITDA з початку експлуатації. Прогноз окупності = CAPEX / річний темп EBITDA (накопичений EBITDA × 12 / місяців з даними)." />
        </h2>
        <span className="economics-month-muted">
          Дані оновлено: {lastFactMonthKey ? formatMonthTitle(lastFactMonthKey) : '—'}
        </span>
      </div>

      <section className="economics-payback-kpis" aria-label="Ключові показники окупності">
        <KpiCard
          icon="bars"
          tone="blue"
          label="CAPEX проєкту"
          value={formatUah(capexUah)}
          note="разові інвестиції"
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

      <section className="economics-card economics-payback-progress" aria-label="Прогрес окупності">
        <div className="economics-payback-progress-line">
          <span className="economics-payback-side-icon economics-payback-tone-blue">
            <Icon name="coin" />
          </span>
          <span className="economics-payback-progress-caption">
            Повернуто <b>{uahShort(allTimeEbitda)}</b> із <b>{uahShort(capexUah)}</b>
          </span>
          <div className="economics-capex-bar economics-payback-bar">
            <span className="economics-capex-bar-fill" style={{ width: `${coveredShare * 100}%` }} />
          </div>
          <span className="economics-payback-progress-pct">{formatPercent(coveredShare)}</span>
          <span className="economics-payback-progress-rest">
            {paidOff ? 'Капекс окуплено' : `Залишилось повернути ${uahShort(remaining)}`}
          </span>
        </div>
        {scenario ? (
          <div className="economics-payback-range">
            <span className="economics-payback-range-label">
              Прогноз окупності (діапазон)
              <OptimumInfo tip="Діапазон = базовий темп EBITDA ± похибка середнього за спостереженими місяцями. Оптимістичний сценарій — швидший темп, консервативний — повільніший. Діапазон звужується з накопиченням даних." />
            </span>
            <span className="economics-payback-range-value">
              {paybackLabel(scenario.optYears)} – {paybackLabel(scenario.consYears)}
            </span>
            <span className="economics-payback-range-note">оптимістичний – консервативний сценарій</span>
          </div>
        ) : null}
      </section>

      <div className="economics-payback-grid">
        <section className="economics-card economics-month-section" aria-label="Накопичена окупність проєкту">
          <div className="economics-capex-chart-head">
            <span className="economics-capex-chart-title">
              Накопичена окупність проєкту
              <span className="economics-capex-chart-unit">{axis.unit}</span>
              <OptimumInfo tip="Суцільна лінія — фактичний накопичений EBITDA від початку експлуатації. Пунктир — базовий прогноз тим самим річним темпом до перетину з CAPEX. Вертикаль «Сьогодні» — остання фактична точка; точка окупності — прогнозований місяць повного повернення інвестицій." />
            </span>
            <span className="economics-month-legend">
              <span><i style={{ background: '#2f6fed' }} />Факт (накопичений EBITDA)</span>
              <span><i style={{ background: '#60a5fa' }} />Прогноз (накопичений EBITDA)</span>
              <span><i style={{ background: '#94a3b8' }} />CAPEX ({uahShort(capexUah)})</span>
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
                width={40}
                tickLine={false}
                axisLine={false}
                tickFormatter={axis.tick}
                domain={[0, (dataMax: number) => Math.max(dataMax, capexUah) * 1.08]}
              />
              <Tooltip content={<CapexPaybackTooltip />} cursor={{ stroke: '#cbd5e1' }} />
              <ReferenceLine
                y={capexUah}
                stroke="#64748b"
                strokeWidth={1.4}
                label={{ value: `CAPEX ${uahShort(capexUah)}`, position: 'insideTopLeft', fontSize: 11, fill: '#475569' }}
              />
              {todayT !== null && todayT > 0 ? (
                <ReferenceLine
                  x={todayT}
                  stroke="#cbd5e1"
                  strokeDasharray="3 3"
                  label={{ value: 'Сьогодні', position: 'insideTopRight', fontSize: 11, fill: '#94a3b8' }}
                />
              ) : null}
              <Area type="monotone" dataKey="factCum" stroke="none" fill="#2f6fed" fillOpacity={0.09} />
              <Line type="monotone" dataKey="factCum" stroke="#2f6fed" strokeWidth={2.2} dot={false} connectNulls={false} />
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
                  label={{ value: `Повернуто ${uahShort(allTimeEbitda)}`, position: 'top', fontSize: 11, fill: '#2f6fed' }}
                />
              ) : null}
              {paybackT !== null ? (
                <ReferenceDot
                  x={paybackT}
                  y={capexUah}
                  r={4.5}
                  fill="#7c3aed"
                  stroke="#fff"
                  strokeWidth={1.5}
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
        </section>

        <aside className="economics-card economics-month-section economics-payback-side" aria-label="Ключові показники">
          <h3 className="economics-payback-side-title">Ключові показники</h3>
          <SideRow icon="bars" tone="blue" label="Накопичений EBITDA" value={formatUah(allTimeEbitda)} />
          <SideRow icon="percent" tone="green" label="Окуплено" value={formatPercent(coveredShare)} />
          <SideRow
            icon="pie"
            tone="orange"
            label="Залишилось повернути"
            value={paidOff ? '0 грн' : formatUah(remaining)}
          />
          <SideRow icon="calendar" tone="blue" label="Прогноз окупності" value={paybackDurationLabel} note={paidOff ? undefined : paybackDateNote} />
          <SideRow
            icon="trend"
            tone="violet"
            label="Середньорічний фактичний ROI"
            value={Number.isFinite(avgAnnualRoi) ? formatPercent(avgAnnualRoi) : '—'}
            note={`за ${paybackLabel(operationYears)} експлуатації`}
          />
          {hasPrior ? (
            <SideRow
              icon="clock"
              tone="violet"
              label="Накопичений EBITDA до періоду"
              value={formatUah(prior)}
              note={`у т.ч. до початку помісячних даних (${model.priorMonths} міс.)`}
            />
          ) : null}
        </aside>
      </div>

      <div className="economics-payback-grid">
        <section className="economics-card economics-month-section" aria-label="Щомісячний економічний ефект">
          <div className="economics-capex-chart-head">
            <span className="economics-capex-chart-title">
              Щомісячний економічний ефект
              <span className="economics-capex-chart-unit">{effectAxis.unit}</span>
              <OptimumInfo tip="Стовпці — EBITDA за період: сині — факт, фіолетові — базовий прогноз до кінця поточного року. Пунктирні лінії — накопичений EBITDA від початку експлуатації (права шкала): синя — факт, фіолетова — прогноз." />
            </span>
            <div className="economics-payback-effect-tools">
              <span className="economics-month-legend">
                <span><i style={{ background: '#60a5fa' }} />EBITDA за місяць (факт)</span>
                <span><i style={{ background: '#2f6fed' }} />накопичено (факт)</span>
                <span><i style={{ background: '#c4b5fd' }} />EBITDA за місяць (прогноз)</span>
                <span><i style={{ background: '#7c3aed' }} />накопичено (прогноз)</span>
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
            <ComposedChart data={effectRowsBridged} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
              <CartesianGrid strokeDasharray="2 5" stroke="#e7ecf2" vertical={false} />
              <XAxis
                dataKey="label"
                tick={{ fontSize: 11, fill: '#8a94a6' }}
                tickLine={false}
                axisLine={false}
                interval={effectRowsBridged.length > 16 ? Math.ceil(effectRowsBridged.length / 16) - 1 : 0}
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
                dataKey="cumFact"
                stroke="#2f6fed"
                strokeWidth={1.8}
                strokeDasharray="5 4"
                dot={{ r: 2.4, fill: '#fff', stroke: '#2f6fed', strokeWidth: 1.4 }}
                connectNulls={false}
              />
              <Line
                yAxisId="cum"
                type="monotone"
                dataKey="cumForecast"
                stroke="#7c3aed"
                strokeWidth={1.8}
                strokeDasharray="5 4"
                dot={{ r: 2.4, fill: '#fff', stroke: '#7c3aed', strokeWidth: 1.4 }}
                connectNulls={false}
              />
            </ComposedChart>
          </ResponsiveContainer>
        </section>

        <div className="economics-payback-side-stack">
          <aside className="economics-card economics-month-section economics-payback-side" aria-label="Додаткові показники">
            <h3 className="economics-payback-side-title">Додаткові показники · вікно даних</h3>
            <KvRow icon="sun" tone="blue" label="Виробіток за період" value={formatKwh(data.totals.pv_kwh)} />
            <KvRow
              icon="tag"
              tone="violet"
              label="Середній тариф (екв.)"
              value={`${formatPrice(data.totals.avg_import_price_uah_per_kwh)} грн/кВт·год`}
            />
            <KvRow icon="coin" tone="orange" label="Економія / дохід" value={formatUah(data.totals.revenue_total_uah)} />
            <KvRow icon="pie" tone="orange" label="Операційні витрати" value={formatUah(data.totals.expense_total_uah)} />
            <KvRow icon="percent" tone="green" label="Чистий ефект (EBITDA)" value={formatUah(data.totals.ebitda_uah)} />
          </aside>

          <aside
            className="economics-card economics-month-section economics-payback-side economics-payback-deviation"
            aria-label="Відхилення прогнозу окупності"
          >
            <h3 className="economics-payback-side-title">
              Відхилення прогнозу окупності
              <OptimumInfo tip="Порівняння поточного прогнозу окупності з плановим терміном із бізнес-плану (поле «Бізнес-план окупності» в параметрах тарифів)." />
            </h3>
            {planMonths > 0 && deviationMonths !== null ? (
              <>
                <div className="economics-payback-dev-row">
                  <span>Початковий бізнес-план</span>
                  <b>{paybackLabel(planMonths / 12)}</b>
                </div>
                <div className="economics-payback-dev-row">
                  <span>Поточний прогноз</span>
                  <b>{paybackDurationLabel}</b>
                </div>
                <div className="economics-payback-dev-row">
                  <span>Відхилення</span>
                  <b className={deviationMonths > 0 ? 'bad' : 'good'}>
                    {deviationMonths === 0
                      ? 'за планом'
                      : `${deviationMonths > 0 ? '+' : '−'}${Math.abs(deviationMonths)} міс.`}
                  </b>
                </div>
              </>
            ) : (
              <p className="economics-month-muted">
                Вкажіть «Бізнес-план окупності» в параметрах тарифів, щоб порівняти прогноз із планом.
              </p>
            )}
          </aside>
        </div>
      </div>

      <section className="economics-banner economics-payback-note" role="note">
        Прогноз базується на фактичному темпі EBITDA за {model.totalMonthsWithData} міс. експлуатації і може
        змінюватися залежно від виробітку електроенергії, цін РДН, тарифів та операційних витрат.
      </section>
    </>
  )
}
