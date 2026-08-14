import { useMemo } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
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
  unitCostUahPerKwh,
} from '../monthly/EconomicsMonthlyView'
import {
  buildAiPanel,
  HEATMAP_METRIC_TIP,
  HOURS,
  heatCellTip,
  heatTier,
  type PeriodScope,
  reserveSplit,
  signClass,
  uahShort,
} from '../monthly/rollup'
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
  // onSelectMonth drills down to the month view of the clicked YYYY-MM.
  onSelectMonth: (month: string) => void
}

export function EconomicsAnnualView({ data, organizationID, onSelectMonth }: Props) {
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

const HEATMAP_TIP =
  `${HEATMAP_METRIC_TIP} Клітинка підсумовує цю годину за всі дні місяця, ` +
  'тому великі дні важать більше за малі. Наведіть — покаже розрахунок.'

export function MonthHourHeatmap({ margins }: { margins: EconomicsAnnualMonthMargin[] }) {
  return (
    <section className="economics-card economics-month-section" aria-label="Маржинальність УЗЕ по місяцях">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">
          Heatmap: маржинальність УЗЕ (місяць × година)
          <OptimumInfo tip={HEATMAP_TIP} />
        </h3>
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
                  const c = row.hours[h] ?? null
                  return (
                    <td
                      key={h}
                      className={heatTier(c === null ? null : c.margin_uah_per_kwh)}
                      title={
                        c === null
                          ? undefined
                          : heatCellTip(
                              `${formatMonthTitle(row.month)}, ${String(h).padStart(2, '0')}:00 за всі дні`,
                              c,
                            )
                      }
                    >
                      {c === null ? '' : Math.round(c.margin_uah_per_kwh)}
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="economics-month-explain">
        Клітинка — скільки заробила ця година доби за весь місяць на кожній відданій кВт·год: виручка мінус
        собівартість саме тієї енергії, що була в батареї, мінус знос, поділено на сумарний розряд години.
        Наведіть на клітинку, щоб побачити цей розрахунок у гривнях. Порожньо там, де УЗЕ не розряджався.
        Колір: сірий 0–1, світло-зелений 2–5, зелений 6–11, темно-зелений понад 12 грн/кВт·год.
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
  // Same pair and order as the monthly daily table: the all-in cost of
  // one consumed kWh first, the market component beside it for context.
  'Факт. ціна (грн/кВт·год)',
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
    formatPrice(unitCostUahPerKwh(o.import_cost_uah, o.load_kwh)),
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
                  <td className="economics-month-table-left">
                    {formatMonthName(m.month)}
                    {o.flagged_days > 0 && (
                      <span
                        className="economics-info economics-info-warn"
                        title={`${o.flagged_days} дн. із замерзлим або відстаючим лічильником FusionSolar — потоки та ціни цих днів приблизні. Деталі — у денній таблиці місяця.`}
                        role="img"
                        aria-label="Місяць містить дні з приблизними даними"
                      >
                        !
                      </span>
                    )}
                  </td>
                  <td>{formatPrice(unitCostUahPerKwh(o.import_cost_uah, o.load_kwh))}</td>
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
              <td>{formatPrice(unitCostUahPerKwh(totals.import_cost_uah, totals.load_kwh))}</td>
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
