import { useMemo, useState } from 'react'
import type { MouseEvent as ReactMouseEvent, ReactNode } from 'react'
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
  EconomicsMonthlyDay,
  EconomicsMonthlyDayMargin,
  EconomicsMonthlyResponse,
  EconomicsMonthlyTotals,
} from '../../api'
import { formatOrganizationLabel } from '../../dashboard/config'
import {
  formatCycles,
  formatDayLabel,
  formatDayOfMonth,
  formatKwh,
  formatMwh,
  formatMwhNumber,
  formatPercent,
  formatPrice,
  formatShare,
  formatUah,
} from './format'

type Props = {
  data: EconomicsMonthlyResponse
  organizationID: string
}

// signedUah prints an explicit +/− prefix based on the value's sign,
// using the absolute amount so the sign is never doubled (e.g. a
// negative delta renders "−123 грн", a positive one "+123 грн").
function signedUah(delta: number): string {
  const sign = delta < 0 ? '−' : '+'
  return `${sign}${formatUah(Math.abs(delta))}`
}

// signClass tints a currency cell green/red by sign so a loss-making
// day (negative EBITDA / effect) is never shown in the "good" colour.
function signClass(v: number): string {
  return v >= 0 ? 'cell-positive' : 'cell-negative'
}

export function EconomicsMonthlyView({ data, organizationID }: Props) {
  const t = data.totals
  return (
    <>
      <MonthlyKpis totals={t} />
      <div className="economics-month-grid2">
        <MonthlyFinance totals={t} />
        <MonthlyWaterfall totals={t} />
      </div>
      <div className="economics-month-grid2">
        <MonthlyBalance totals={t} />
        <MonthlyOptimum totals={t} days={data.days} />
      </div>
      <div className="economics-month-grid2">
        <MonthlyTrend days={data.days} totals={t} />
        <MonthlyHeatmap margins={data.hourly_margin} />
      </div>
      <MonthlyDailyTable days={data.days} totals={t} organizationID={organizationID} month={data.month} />
    </>
  )
}

// --- KPI strip ---

function MonthlyKpis({ totals }: { totals: EconomicsMonthlyTotals }) {
  const avoidedImportKwh = totals.pv_to_load_kwh + totals.ess_to_load_kwh
  const pvSelfConsumed = totals.pv_to_load_kwh + totals.pv_to_ess_kwh
  const pvSelfShare = totals.pv_kwh > 0 ? pvSelfConsumed / totals.pv_kwh : 0
  const effectClass = totals.effect_uah >= 0 ? 'kpi-card kpi-card-success' : 'kpi-card kpi-card-danger'
  const essClass = totals.ess_net_uah >= 0 ? 'kpi-card kpi-card-info' : 'kpi-card kpi-card-warning'
  const savingShare = totals.baseline_cost_uah > 0 ? totals.effect_uah / totals.baseline_cost_uah : 0

  return (
    <section className="economics-kpis" aria-label="Ключові показники місяця">
      <div className="kpi-strip">
        <div className="kpi-card">
          <span className="kpi-label">Базова вартість (без проєкту)</span>
          <span className="kpi-value">{formatUah(totals.baseline_cost_uah)}</span>
          <span className="kpi-sub">100% споживання з мережі · {formatMwh(totals.load_kwh)}</span>
        </div>
        <div className="kpi-card">
          <span className="kpi-label">Фактична вартість</span>
          <span className="kpi-value">{formatUah(totals.actual_cost_uah)}</span>
          <span className="kpi-sub">імпорт − експорт + знос УЗЕ</span>
        </div>
        <div className={effectClass}>
          <span className="kpi-label">Ефект проєкту за місяць</span>
          <span className="kpi-value">{formatUah(totals.effect_uah)}</span>
          <span className="kpi-sub">економія {formatPercent(savingShare)} від бази</span>
        </div>
        <div className={essClass}>
          <span className="kpi-label">Реалізований ефект УЗЕ</span>
          <span className="kpi-value">{formatUah(totals.ess_net_uah)}</span>
          <span className="kpi-sub">арбітраж РДН, пік, зменшення імпорту</span>
        </div>
      </div>

      <div className="kpi-secondary-strip">
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">Уникнутий імпорт</span>
          <span className="kpi-value">{formatMwh(avoidedImportKwh)}</span>
          <span className="kpi-sub">СЕС→споживання + УЗЕ→споживання</span>
        </div>
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">Самоспоживання СЕС</span>
          <span className="kpi-value">{formatPercent(pvSelfShare)}</span>
          <span className="kpi-sub">
            {formatMwh(pvSelfConsumed)} з {formatMwh(totals.pv_kwh)}
          </span>
        </div>
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">Еквівалентні цикли УЗЕ</span>
          <span className="kpi-value">{formatCycles(totals.equivalent_cycles)}</span>
          <span className="kpi-sub">розряд {formatMwh(totals.ess_discharged_kwh)}</span>
        </div>
        <div className="kpi-card kpi-card-secondary">
          <span className="kpi-label">РДН: середня / max ціна</span>
          <span className="kpi-value">
            {formatPrice(totals.rdn_avg_uah_per_kwh)} / {formatPrice(totals.rdn_max_uah_per_kwh)}
          </span>
          <span className="kpi-sub">грн/кВт·год · зважено за обсягом імпорту</span>
        </div>
      </div>
    </section>
  )
}

// --- Finance breakdown + mini grid ---

function MonthlyFinance({ totals }: { totals: EconomicsMonthlyTotals }) {
  const revenueLines = [
    { label: 'СЕС → споживання', amount: totals.revenue_pv_self_uah },
    { label: 'СЕС → мережа', amount: totals.revenue_pv_export_uah },
    { label: 'УЗЕ → споживання', amount: totals.revenue_ess_self_uah },
    { label: 'УЗЕ → мережа', amount: totals.revenue_ess_export_uah },
  ]
  const expenseLines = [
    { label: 'Заряд УЗЕ із мережі', amount: totals.expense_grid_charge_uah },
    { label: 'Знос / ресурс УЗЕ', amount: Math.max(totals.ess_degradation_cost_uah, 0) },
  ]
  const expenseTotal = expenseLines.reduce((acc, l) => acc + l.amount, 0)
  const savingShare = totals.baseline_cost_uah > 0 ? totals.ebitda_uah / totals.baseline_cost_uah : 0

  return (
    <section className="economics-card economics-month-section" aria-label="Фінансова розкладка за місяць">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Фінансова розкладка за місяць</h3>
        <div className="economics-month-legend">
          <span><i style={{ background: '#16a34a' }} />дохід / економія</span>
          <span><i style={{ background: '#dc2626' }} />витрати</span>
        </div>
      </div>
      <div className="economics-finance">
        <FinanceBox title="Дохід / економія" total={totals.revenue_total_uah} lines={revenueLines} />
        <FinanceBox title="Витрати" total={expenseTotal} lines={expenseLines} />
      </div>
      <div className="economics-month-mini-grid">
        <MiniCard label="EBITDA" value={formatUah(totals.ebitda_uah)} note={`${formatPercent(savingShare)} від бази`} good />
        <MiniCard
          label="Найкращий день"
          value={formatUah(totals.best_day.effect_uah)}
          note={totals.best_day.date ? formatDayLabel(totals.best_day.date) : '—'}
          good
        />
        <MiniCard
          label="Мінімальний ефект"
          value={formatUah(totals.min_effect_day.effect_uah)}
          note={totals.min_effect_day.date ? formatDayLabel(totals.min_effect_day.date) : '—'}
        />
        <MiniCard label="Окупність за темпом" value="—" note="потрібні дані CAPEX" />
      </div>
    </section>
  )
}

function FinanceBox({
  title,
  total,
  lines,
}: {
  title: string
  total: number
  lines: { label: string; amount: number }[]
}) {
  return (
    <div className="economics-finance-box">
      <div className="economics-finance-title">
        <span>{title}</span>
        <span>{formatUah(total)}</span>
        <span>100%</span>
      </div>
      {lines.map((line) => (
        <div key={line.label} className="economics-metric-row">
          <span>{line.label}</span>
          <span className="economics-money">{formatUah(line.amount)}</span>
          <span className="economics-month-muted">{formatShare(line.amount, total)}</span>
        </div>
      ))}
    </div>
  )
}

function MiniCard({ label, value, note, good }: { label: string; value: string; note: string; good?: boolean }) {
  return (
    <div className="economics-month-mini">
      <span className="economics-month-mini-label">{label}</span>
      <span className={good ? 'economics-month-mini-value good' : 'economics-month-mini-value'}>{value}</span>
      <span className="economics-month-mini-note">{note}</span>
    </div>
  )
}

// --- Waterfall ---

function MonthlyWaterfall({ totals }: { totals: EconomicsMonthlyTotals }) {
  const base = totals.baseline_cost_uah
  // `value` is each step's signed effect on cost (negative = reduces
  // cost). `display` derives its sign from `value` so a negative ESS
  // effect renders "+…" (a cost increase) instead of a double minus.
  const items = [
    { name: 'база без проєкту', value: base, color: 'gray', display: formatUah(base) },
    { name: 'СЕС самоспоживання', value: -totals.revenue_pv_self_uah, color: 'green', display: signedUah(-totals.revenue_pv_self_uah) },
    { name: 'експорт СЕС', value: -totals.revenue_pv_export_uah, color: 'blue', display: signedUah(-totals.revenue_pv_export_uah) },
    { name: 'ефект УЗЕ', value: -totals.ess_net_uah, color: 'violet', display: signedUah(-totals.ess_net_uah) },
    { name: 'заряд і знос', value: totals.expense_total_uah, color: 'red', display: signedUah(totals.expense_total_uah) },
    { name: 'факт. вартість', value: totals.actual_cost_uah, color: 'gray', display: formatUah(totals.actual_cost_uah) },
  ]
  const maxMagnitude = Math.max(base, ...items.map((i) => Math.abs(i.value)), 1)

  return (
    <section className="economics-card economics-month-section" aria-label="Водоспад економіки">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Водоспад економіки</h3>
        <span className="economics-month-muted">грн за місяць</span>
      </div>
      <div className="economics-waterfall">
        {items.map((item) => (
          <div className="economics-wf-item" key={item.name}>
            <div
              className={`economics-wf-bar economics-wf-${item.color}`}
              style={{ height: `${Math.max((Math.abs(item.value) / maxMagnitude) * 100, 6)}%` }}
            />
            <div className="economics-wf-num">{item.display}</div>
            <div className="economics-wf-name">{item.name}</div>
          </div>
        ))}
      </div>
    </section>
  )
}

// --- Daily energy trend ---

type TrendRow = {
  label: string
  gridImport: number
  essDischarge: number
  pv: number
  gridExport: number
  essCharge: number
  load: number
}

const trendNumberFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })

// Trend palette: generation/sources are shades of green (above the axis),
// sinks/exports are shades of orange (below). Order matches the legend.
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
  label?: string | number
  payload?: { payload: TrendRow }[]
}

function TrendTooltip({ active, payload, label }: TrendTooltipProps) {
  if (!active || !payload?.length) return null
  const row = payload[0].payload
  return (
    <div className="economics-trend-tip">
      <div className="economics-trend-tip-day">{label}</div>
      {TREND_SERIES.map((s) => (
        <div className="economics-trend-tip-row" key={s.key}>
          <i style={{ background: s.color }} />
          <span>{s.name}</span>
          <b>{trendNumberFmt.format(Math.abs(row[s.key]))}</b>
        </div>
      ))}
    </div>
  )
}

function MonthlyTrend({ days, totals }: { days: EconomicsMonthlyDay[]; totals: EconomicsMonthlyTotals }) {
  const rows = useMemo<TrendRow[]>(
    () =>
      days.map((d) => ({
        label: formatDayOfMonth(d.date),
        gridImport: d.grid_import_kwh / 1000,
        essDischarge: d.ess_discharged_kwh / 1000,
        pv: d.pv_kwh / 1000,
        gridExport: -d.grid_export_kwh / 1000,
        essCharge: -d.ess_charged_kwh / 1000,
        load: -d.load_kwh / 1000,
      })),
    [days],
  )

  // PV fate: directly self-consumed vs exported + stored.
  const pvSelf = totals.pv_to_load_kwh
  const pvOther = totals.pv_to_grid_kwh + totals.pv_to_ess_kwh
  const pvSelfShare = totals.pv_kwh > 0 ? pvSelf / totals.pv_kwh : 0
  // Load coverage: served by PV+ESS vs taken from the grid. Consumption
  // is the sum of the three load-serving flows (matching the boundary
  // balance widget), not the raw load meter, so both blocks agree.
  const loadFromRenewable = totals.pv_to_load_kwh + totals.ess_to_load_kwh
  const loadFromGrid = totals.grid_to_load_kwh
  const consumption = loadFromRenewable + loadFromGrid
  const loadRenewableShare = consumption > 0 ? loadFromRenewable / consumption : 0

  return (
    <section className="economics-card economics-month-section" aria-label="Енергетичний тренд по днях">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Енергетичний тренд по днях</h3>
        <span className="economics-month-muted">МВт·год/день</span>
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
          <BarChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: 0 }} stackOffset="sign" barCategoryGap="12%">
            <CartesianGrid strokeDasharray="2 5" stroke="#e7ecf2" vertical={false} />
            <XAxis dataKey="label" tick={{ fontSize: 10, fill: '#8a94a6' }} interval={0} tickLine={false} axisLine={false} />
            <YAxis tick={{ fontSize: 11, fill: '#98a2b3' }} width={40} tickLine={false} axisLine={false} />
            <Tooltip content={<TrendTooltip />} cursor={{ fill: 'rgba(148, 163, 184, 0.12)' }} />
            <ReferenceLine y={0} stroke="#98a2b3" />
            <Bar dataKey="pv" name="виробіток СЕС" stackId="pos" fill="#91d9aa" maxBarSize={28} />
            <Bar dataKey="essDischarge" name="розряд УЗЕ" stackId="pos" fill="#5fc993" maxBarSize={28} />
            <Bar dataKey="gridImport" name="з мережі" stackId="pos" fill="#12b76a" maxBarSize={28} radius={[3, 3, 0, 0]} />
            <Bar dataKey="load" name="споживання" stackId="neg" fill="#fdba74" maxBarSize={28} />
            <Bar dataKey="essCharge" name="заряд УЗЕ" stackId="neg" fill="#fb923c" maxBarSize={28} />
            <Bar dataKey="gridExport" name="експорт у мережу" stackId="neg" fill="#f97316" maxBarSize={28} radius={[0, 0, 3, 3]} />
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

// --- ESS fact vs optimum ---
//
// "Optimum" is the best dispatch the battery could have achieved within
// its demonstrated operating envelope (power, SOC range and round-trip
// efficiency derived from the month's own telemetry), valued with the
// same essNet objective as the realised figure. Reserve = optimum − fact
// is the under-used opportunity (not a loss).
const TOP_RESERVE_ROWS = 8

function OptimumInfo({ tip }: { tip: string }) {
  return (
    <span className="economics-info" data-tip={tip} role="img" aria-label={tip}>
      i
    </span>
  )
}

function MonthlyOptimum({ totals, days }: { totals: EconomicsMonthlyTotals; days: EconomicsMonthlyDay[] }) {
  const captured = totals.ess_captured_share
  const hasOptimum = totals.ess_optimum_uah > 0
  const [openDate, setOpenDate] = useState<string | null>(null)

  const rows = useMemo(
    () =>
      days
        .filter((d) => d.ess_optimum_uah > 0)
        .sort((a, b) => b.ess_reserve_uah - a.ess_reserve_uah)
        .slice(0, TOP_RESERVE_ROWS),
    [days],
  )

  return (
    <section className="economics-card economics-month-section" aria-label="УЗЕ: факт vs оптимум">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">УЗЕ: факт vs оптимум</h3>
        <div className="economics-month-muted">ефект, грн</div>
      </div>
      <div className="economics-optimum-kpis">
        <div className="economics-optimum-kpi">
          <span className="economics-optimum-kpi-label">
            Оптимум
            <OptimumInfo tip="Модельний максимум ефекту УЗЕ за фактичних цін РДН, генерації СЕС, споживання, ємності, SOC, потужності, ККД та зносу. Заряд від СЕС має собівартість 0." />
          </span>
          <span className="economics-optimum-kpi-value">{formatUah(totals.ess_optimum_uah)}</span>
        </div>
        <div className="economics-optimum-kpi">
          <span className="economics-optimum-kpi-label">
            Факт
            <OptimumInfo tip="Фактичний ефект УЗЕ = УЗЕ → споживання + УЗЕ → мережа − заряд УЗЕ з мережі − втрати та знос (заряд від СЕС безкоштовний)." />
          </span>
          <span className="economics-optimum-kpi-value good">{formatUah(totals.ess_fact_uah)}</span>
        </div>
        <div className="economics-optimum-kpi">
          <span className="economics-optimum-kpi-label">
            Захоплено
            <OptimumInfo tip="Факт / Оптимум. Показує, яку частку доступної можливості УЗЕ реально використала." />
          </span>
          <span className="economics-optimum-kpi-value">{hasOptimum ? formatPercent(captured) : '—'}</span>
        </div>
        <div className="economics-optimum-kpi">
          <span className="economics-optimum-kpi-label">
            Резерв
            <OptimumInfo tip="Оптимум − Факт. Це не збиток, а оцінка недовикористаної можливості за місяць." />
          </span>
          <span className="economics-optimum-kpi-value amber">{formatUah(totals.ess_reserve_uah)}</span>
        </div>
      </div>

      {rows.length > 0 ? (
        <>
          <div className="economics-optimum-legend">
            <span><i style={{ background: '#7c3aed' }} />фактичний ефект</span>
            <span><i style={{ background: '#f59e0b' }} />недовикористано</span>
            <span><i style={{ background: '#e5e7eb' }} />оптимум</span>
          </div>
          <div className="economics-optimum-list">
            <div className="economics-optimum-row head">
              <span>Дата</span>
              <span>захоплення</span>
              <span>опт.</span>
              <span>факт</span>
              <span>резерв</span>
            </div>
            {rows.map((d) => {
              const factShare = d.ess_optimum_uah > 0 ? Math.max(0, Math.min(1, d.ess_fact_uah / d.ess_optimum_uah)) : 0
              const open = openDate === d.date
              return (
                <div key={d.date} className="economics-optimum-rowgroup">
                  <button
                    type="button"
                    className={`economics-optimum-row clickable${open ? ' open' : ''}`}
                    aria-expanded={open}
                    onClick={() => setOpenDate(open ? null : d.date)}
                  >
                    <strong>
                      <span className="economics-optimum-caret">{open ? '▾' : '▸'}</span>
                      {formatDayLabel(d.date)}
                    </strong>
                    <div className="economics-capture-bar">
                      <span className="fact" style={{ width: `${factShare * 100}%` }} />
                      <span className="missed" style={{ width: `${(1 - factShare) * 100}%` }} />
                    </div>
                    <span>{formatUah(d.ess_optimum_uah)}</span>
                    <span className="good">{formatUah(d.ess_fact_uah)}</span>
                    <span className="amber">{formatUah(d.ess_reserve_uah)}</span>
                  </button>
                  {open ? <OptimumReasons day={d} /> : null}
                </div>
              )
            })}
          </div>
          <p className="economics-month-muted economics-optimum-note">
            Оптимум — найкращий диспетчинг у межах фактично продемонстрованих
            можливостей УЗЕ (потужність, діапазон SOC, ККД виведені з даних місяця).
          </p>
        </>
      ) : (
        <p className="economics-month-empty-note">Недостатньо активності УЗЕ в місяці для оцінки оптимуму.</p>
      )}
    </section>
  )
}

// OptimumReasons breaks a day's reserve into the three causes the backend
// attributes it to (timing of discharge, pre-peak SOC / charge timing, and
// missed PV charging). The three values sum to that day's reserve.
function OptimumReasons({ day }: { day: EconomicsMonthlyDay }) {
  return (
    <div className="economics-optimum-detail">
      <div className="economics-reason-grid">
        <div className="economics-reason">
          <strong>Розряд не в пік</strong>
          <span>
            {formatKwh(day.ess_discharged_kwh)} розряду пройшли повз найдорожчі години: резерв{' '}
            {formatUah(day.ess_reserve_timing_uah)}.
          </span>
        </div>
        <div className="economics-reason">
          <strong>Заряд від СЕС</strong>
          <span>
            {formatKwh(day.ess_pv_missed_kwh)} доступного надлишку СЕС не потрапили в УЗЕ: резерв{' '}
            {formatUah(day.ess_reserve_pv_uah)}.
          </span>
        </div>
        <div className="economics-reason">
          <strong>SOC перед піком</strong>
          <span>
            Бракувало заряду батареї перед вечірнім піком: резерв {formatUah(day.ess_reserve_soc_uah)}.
          </span>
        </div>
      </div>
    </div>
  )
}

// --- Energy balance ---
//
// Boundary balance of the month: what entered the object (Джерела =
// PV + grid import) must equal where it went (Напрямки = consumption +
// export + net ESS storage). Both bars and the flow table read straight
// off the monthly totals; no figure is derived as a residual.

type BalanceSegClass = 'green' | 'blue' | 'amber' | 'violet' | 'red'
type TipLine = { k: string; v: string }
type BalanceSeg = { cls: BalanceSegClass; kwh: number; title: string; lines: TipLine[] }
type BalanceBar = { name: string; total: number; segs: BalanceSeg[] }
type SegTip = { title: string; lines: TipLine[]; x: number; y: number }
type FlowMode = 'detail' | 'source' | 'destination'

function MonthlyBalance({ totals }: { totals: EconomicsMonthlyTotals }) {
  const [flowMode, setFlowMode] = useState<FlowMode>('detail')
  const [tip, setTip] = useState<SegTip | null>(null)

  // Sources entering the object boundary.
  const sourcesTotal = totals.pv_kwh + totals.grid_import_kwh
  // Directions leaving the boundary. Consumption and export are sums of
  // their measured/modelled flows (never a residual), ESS is the net
  // amount that stayed in storage over the month.
  const consumption = totals.pv_to_load_kwh + totals.ess_to_load_kwh + totals.grid_to_load_kwh
  const exportTotal = totals.pv_to_grid_kwh + totals.ess_to_grid_kwh
  const essNet = totals.ess_charged_kwh - totals.ess_discharged_kwh
  const checkTotal = consumption + exportTotal + essNet

  const gridToLoadCost = totals.grid_to_load_kwh * totals.avg_import_price_uah_per_kwh

  const bars: BalanceBar[] = [
    {
      name: 'Джерела',
      total: sourcesTotal,
      segs: [
        {
          cls: 'green',
          kwh: totals.pv_kwh,
          title: 'СЕС',
          lines: [
            { k: 'Обсяг', v: formatMwh(totals.pv_kwh) },
            { k: 'Частка', v: formatShare(totals.pv_kwh, sourcesTotal) },
          ],
        },
        {
          cls: 'blue',
          kwh: totals.grid_import_kwh,
          title: 'Імпорт з мережі',
          lines: [
            { k: 'Обсяг', v: formatMwh(totals.grid_import_kwh) },
            { k: 'Частка', v: formatShare(totals.grid_import_kwh, sourcesTotal) },
            { k: 'На споживання', v: formatMwh(totals.grid_to_load_kwh) },
            { k: 'На заряд УЗЕ', v: formatMwh(totals.grid_to_ess_kwh) },
          ],
        },
      ],
    },
    {
      name: 'Напрямки',
      total: checkTotal,
      segs: [
        {
          cls: 'amber',
          kwh: consumption,
          title: "Споживання об'єкта",
          lines: [
            { k: 'Разом', v: formatMwh(consumption) },
            { k: 'від СЕС', v: formatMwh(totals.pv_to_load_kwh) },
            { k: 'від УЗЕ', v: formatMwh(totals.ess_to_load_kwh) },
            { k: 'від мережі', v: formatMwh(totals.grid_to_load_kwh) },
          ],
        },
        {
          cls: 'red',
          kwh: exportTotal,
          title: 'Експорт у мережу',
          lines: [
            { k: 'Разом', v: formatMwh(exportTotal) },
            { k: 'від СЕС', v: formatMwh(totals.pv_to_grid_kwh) },
            { k: 'від УЗЕ', v: formatMwh(totals.ess_to_grid_kwh) },
          ],
        },
        {
          cls: 'violet',
          kwh: Math.max(essNet, 0),
          title: 'Накопичено в УЗЕ (нетто)',
          lines: [
            { k: 'Заряд', v: formatMwh(totals.ess_charged_kwh) },
            { k: 'Розряд', v: formatMwh(totals.ess_discharged_kwh) },
            { k: 'Нетто', v: formatMwh(essNet) },
          ],
        },
      ],
    },
  ]

  // The seven detailed flows. Energy through the battery is counted twice
  // (charge + discharge), so their sum exceeds the sources total — the
  // table share is therefore relative to this sum, not to the boundary.
  const flows = [
    { id: 'pv_to_load', label: 'СЕС → споживання', source: 'СЕС', destination: 'споживання', dot: 'consume', kwh: totals.pv_to_load_kwh, effect: totals.revenue_pv_self_uah, cls: 'good' },
    { id: 'pv_to_grid', label: 'СЕС → мережа', source: 'СЕС', destination: 'мережа', dot: 'export', kwh: totals.pv_to_grid_kwh, effect: totals.revenue_pv_export_uah, cls: 'good' },
    { id: 'pv_to_ess', label: 'СЕС → УЗЕ', source: 'СЕС', destination: 'УЗЕ', dot: 'ess', kwh: totals.pv_to_ess_kwh, effect: 0, cls: 'neutral' },
    { id: 'ess_to_load', label: 'УЗЕ → споживання', source: 'УЗЕ', destination: 'споживання', dot: 'consume', kwh: totals.ess_to_load_kwh, effect: totals.revenue_ess_self_uah, cls: 'good' },
    { id: 'ess_to_grid', label: 'УЗЕ → мережа', source: 'УЗЕ', destination: 'мережа', dot: 'export', kwh: totals.ess_to_grid_kwh, effect: totals.revenue_ess_export_uah, cls: 'good' },
    { id: 'grid_to_load', label: 'Мережа → споживання', source: 'Мережа', destination: 'споживання', dot: 'grid', kwh: totals.grid_to_load_kwh, effect: -gridToLoadCost, cls: 'bad' },
    { id: 'grid_to_ess', label: 'Мережа → УЗЕ', source: 'Мережа', destination: 'УЗЕ', dot: 'ess', kwh: totals.grid_to_ess_kwh, effect: -totals.expense_grid_charge_uah, cls: 'bad' },
  ]
  const flowTotalKwh = flows.reduce((acc, f) => acc + f.kwh, 0)

  const groupBy = (field: 'source' | 'destination') => {
    const order: string[] = []
    const sums = new Map<string, number>()
    for (const f of flows) {
      const key = f[field]
      if (!sums.has(key)) order.push(key)
      sums.set(key, (sums.get(key) ?? 0) + f.kwh)
    }
    return order.map((name) => ({ name, kwh: sums.get(name) ?? 0 }))
  }

  const showTip = (s: BalanceSeg) => (e: ReactMouseEvent<HTMLSpanElement>) =>
    setTip({ title: s.title, lines: s.lines, x: e.clientX + 14, y: e.clientY + 14 })
  const moveTip = (e: ReactMouseEvent<HTMLSpanElement>) =>
    setTip((t) => (t ? { ...t, x: e.clientX + 14, y: e.clientY + 14 } : t))
  const hideTip = () => setTip(null)

  return (
    <section className="economics-card economics-month-section" aria-label="Енергетичний баланс за місяць">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Енергетичний баланс за місяць</h3>
        <span className="economics-month-muted">МВт·год за місяць</span>
      </div>
      <div className="economics-flow-legend">
        <span><i className="economics-seg-dot green" />СЕС</span>
        <span><i className="economics-seg-dot blue" />імпорт</span>
        <span><i className="economics-seg-dot amber" />споживання</span>
        <span><i className="economics-seg-dot red" />експорт</span>
        <span><i className="economics-seg-dot violet" />УЗЕ нетто</span>
      </div>
      <div className="economics-month-balance">
        {bars.map((bar) => (
          <div className="economics-balance-row" key={bar.name}>
            <strong className="economics-balance-name">{bar.name}</strong>
            <div className="economics-balance-bar">
              {bar.segs.map((s) => (
                <BalanceSegment
                  key={s.title}
                  seg={s}
                  width={segWidth(s.kwh, bar.total)}
                  onEnter={showTip(s)}
                  onMove={moveTip}
                  onLeave={hideTip}
                />
              ))}
            </div>
            <span className="economics-balance-value">{formatMwh(bar.total)}</span>
          </div>
        ))}
      </div>
      <p className="economics-balance-note">
        Джерела {formatMwh(sourcesTotal)} = напрямки {formatMwh(checkTotal)} (споживання + експорт + нетто-заряд УЗЕ).
        Наведіть на сегмент для деталей.
      </p>

      <div className="economics-balance-subtabs" role="tablist">
        <SubTab mode="detail" active={flowMode} onSelect={setFlowMode}>Детальні потоки</SubTab>
        <SubTab mode="source" active={flowMode} onSelect={setFlowMode}>За джерелом</SubTab>
        <SubTab mode="destination" active={flowMode} onSelect={setFlowMode}>За призначенням</SubTab>
      </div>
      <div className="economics-table-scroll" style={{ marginTop: 10 }}>
        {flowMode === 'detail' ? (
          <table className="economics-table economics-month-table">
            <thead>
              <tr>
                <th>Потік</th>
                <th>МВт·год</th>
                <th>Частка</th>
                <th>Фін. ефект</th>
              </tr>
            </thead>
            <tbody>
              {flows.map((f) => (
                <tr key={f.id}>
                  <td className="economics-month-table-left">
                    <span className={`economics-flow-dot ${f.dot}`} aria-hidden="true" />
                    {f.label}
                  </td>
                  <td>{formatMwhNumber(f.kwh)}</td>
                  <td>{formatShare(f.kwh, flowTotalKwh)}</td>
                  <td className={`cell-${f.cls === 'good' ? 'positive' : f.cls === 'bad' ? 'negative' : 'neutral'}`}>
                    {f.effect === 0 ? '0 грн' : formatUah(f.effect)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <table className="economics-table economics-month-table">
            <thead>
              <tr>
                <th>{flowMode === 'source' ? 'Джерело' : 'Призначення'}</th>
                <th>МВт·год</th>
                <th>Частка</th>
              </tr>
            </thead>
            <tbody>
              {groupBy(flowMode).map((g) => (
                <tr key={g.name}>
                  <td className="economics-month-table-left">{g.name}</td>
                  <td>{formatMwhNumber(g.kwh)}</td>
                  <td>{formatShare(g.kwh, flowTotalKwh)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {tip ? (
        <div className="economics-seg-tip" style={{ left: tip.x, top: tip.y }} role="tooltip">
          <strong>{tip.title}</strong>
          {tip.lines.map((l) => (
            <div className="economics-seg-tip-line" key={l.k}>
              <span>{l.k}</span>
              <b>{l.v}</b>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  )
}

// segWidth keeps a hairline-visible slice for any non-zero flow while
// scaling the rest proportionally to the row total.
function segWidth(kwh: number, total: number): number {
  if (kwh <= 0 || total <= 0) return 0
  return Math.max((kwh / total) * 100, 0.4)
}

function SubTab({
  mode,
  active,
  onSelect,
  children,
}: {
  mode: FlowMode
  active: FlowMode
  onSelect: (m: FlowMode) => void
  children: ReactNode
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active === mode}
      className={`economics-balance-subtab${active === mode ? ' active' : ''}`}
      onClick={() => onSelect(mode)}
    >
      {children}
    </button>
  )
}

// BalanceSegment is one coloured slice of a balance bar. Zero-width
// slices are skipped so an empty flow leaves no stray sliver. Hovering a
// slice raises a cursor-following breakdown tooltip.
function BalanceSegment({
  seg,
  width,
  onEnter,
  onMove,
  onLeave,
}: {
  seg: BalanceSeg
  width: number
  onEnter: (e: ReactMouseEvent<HTMLSpanElement>) => void
  onMove: (e: ReactMouseEvent<HTMLSpanElement>) => void
  onLeave: () => void
}) {
  if (width <= 0) return null
  return (
    <span
      className={`economics-seg ${seg.cls} economics-seg-hover`}
      style={{ width: `${width}%` }}
      onMouseEnter={onEnter}
      onMouseMove={onMove}
      onMouseLeave={onLeave}
    />
  )
}

// --- ESS marginality heatmap ---

function heatTier(v: number | null): string {
  if (v === null || !Number.isFinite(v)) return 'economics-hm-empty'
  if (v < 2) return 'economics-hm0'
  if (v < 6) return 'economics-hm1'
  if (v < 12) return 'economics-hm2'
  if (v < 18) return 'economics-hm3'
  return 'economics-hm4'
}

const HOURS = Array.from({ length: 24 }, (_, h) => h)

function MonthlyHeatmap({ margins }: { margins: EconomicsMonthlyDayMargin[] }) {
  return (
    <section className="economics-card economics-month-section" aria-label="Маржинальність УЗЕ">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Heatmap: маржинальність УЗЕ</h3>
        <span className="economics-month-muted">грн/кВт·год розряду</span>
      </div>
      <div className="economics-heatmap-scroll">
        <table className="economics-heatmap">
          <thead>
            <tr>
              <th className="economics-heatmap-corner">Дата</th>
              {HOURS.map((h) => (
                <th key={h}>{String(h).padStart(2, '0')}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {margins.map((row) => (
              <tr key={row.date}>
                <th scope="row">{formatDayOfMonth(row.date)}</th>
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
        Колір показує маржу розряду УЗЕ: сірий 0–1, світло-зелений 2–5, зелений 6–11, темно-зелений понад 12 грн/кВт·год.
      </p>
    </section>
  )
}

// --- Daily detail table + Excel export ---

const COLUMNS = [
  'Дата',
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

function dayRowValues(d: EconomicsMonthlyDay): string[] {
  const pvSelf = d.pv_to_load_kwh + d.pv_to_ess_kwh
  const selfShare = d.pv_kwh > 0 ? pvSelf / d.pv_kwh : 0
  return [
    formatDayLabel(d.date),
    formatPrice(d.rdn_avg_uah_per_kwh),
    formatKwh(d.pv_kwh),
    formatKwh(d.load_kwh),
    formatKwh(d.grid_import_kwh),
    formatKwh(d.grid_export_kwh),
    formatPercent(selfShare),
    formatCycles(d.equivalent_cycles),
    formatUah(d.ebitda_uah),
    formatUah(d.effect_uah),
    formatUah(d.ess_net_uah),
  ]
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function exportToExcel(days: EconomicsMonthlyDay[], month: string) {
  const head = `<tr>${COLUMNS.map((c) => `<th>${escapeHtml(c)}</th>`).join('')}</tr>`
  const body = days
    .map((d) => `<tr>${dayRowValues(d).map((v) => `<td>${escapeHtml(v)}</td>`).join('')}</tr>`)
    .join('')
  const html = `<!doctype html><html><head><meta charset="utf-8"></head><body><table>${head}${body}</table></body></html>`
  const blob = new Blob([html], { type: 'application/vnd.ms-excel;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `economics-monthly-${month}.xls`
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}

function MonthlyDailyTable({
  days,
  totals,
  organizationID,
  month,
}: {
  days: EconomicsMonthlyDay[]
  totals: EconomicsMonthlyTotals
  organizationID: string
  month: string
}) {
  const pvSelf = totals.pv_to_load_kwh + totals.pv_to_ess_kwh
  const selfShare = totals.pv_kwh > 0 ? pvSelf / totals.pv_kwh : 0
  return (
    <section className="economics-table-wrap" aria-label="Денна деталізація місяця">
      <div className="economics-month-section-head">
        <h3>
          Денна деталізація місяця
          <span className="economics-table-context"> · {formatOrganizationLabel(organizationID)}</span>
        </h3>
        <button type="button" className="economics-export-btn" onClick={() => exportToExcel(days, month)}>
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
            {days.map((d) => (
              <tr key={d.date}>
                <td className="economics-month-table-left">{formatDayLabel(d.date)}</td>
                <td>{formatPrice(d.rdn_avg_uah_per_kwh)}</td>
                <td>{formatKwh(d.pv_kwh)}</td>
                <td>{formatKwh(d.load_kwh)}</td>
                <td>{formatKwh(d.grid_import_kwh)}</td>
                <td>{formatKwh(d.grid_export_kwh)}</td>
                <td>{formatPercent(d.pv_kwh > 0 ? (d.pv_to_load_kwh + d.pv_to_ess_kwh) / d.pv_kwh : 0)}</td>
                <td>{formatCycles(d.equivalent_cycles)}</td>
                <td className={signClass(d.ebitda_uah)}>{formatUah(d.ebitda_uah)}</td>
                <td className={signClass(d.effect_uah)}>{formatUah(d.effect_uah)}</td>
                <td>{formatUah(d.ess_net_uah)}</td>
              </tr>
            ))}
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
