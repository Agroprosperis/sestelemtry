import { useMemo } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
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
        <MonthlyTrend days={data.days} totals={t} />
        <MonthlyOptimumStub />
      </div>
      <div className="economics-month-grid2">
        <MonthlyBalance totals={t} />
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
  // Load coverage: served by PV+ESS vs taken from the grid.
  const loadFromRenewable = totals.pv_to_load_kwh + totals.ess_to_load_kwh
  const loadFromGrid = totals.grid_to_load_kwh
  const loadRenewableShare = totals.load_kwh > 0 ? loadFromRenewable / totals.load_kwh : 0

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
            <strong>Споживання об'єкта: {formatMwhNumber(totals.load_kwh)}</strong>
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
        <ResponsiveContainer width="100%" height={340}>
          <BarChart data={rows} margin={{ top: 8, right: 12, bottom: 4, left: 0 }} stackOffset="sign">
            <CartesianGrid strokeDasharray="3 4" stroke="#e7ecf2" vertical={false} />
            <XAxis dataKey="label" tick={{ fontSize: 11, fill: '#7a8494' }} interval={0} />
            <YAxis tick={{ fontSize: 11, fill: '#7a8494' }} width={44} />
            <Tooltip formatter={(value) => `${trendNumberFmt.format(Math.abs(Number(value)))} МВт·год`} />
            <Legend wrapperStyle={{ fontSize: 12 }} />
            <ReferenceLine y={0} stroke="#98a2b3" />
            <Bar dataKey="pv" name="виробіток СЕС" stackId="pos" fill="#91d9aa" />
            <Bar dataKey="essDischarge" name="розряд УЗЕ" stackId="pos" fill="#5fc993" />
            <Bar dataKey="gridImport" name="з мережі" stackId="pos" fill="#12b76a" />
            <Bar dataKey="load" name="споживання" stackId="neg" fill="#fdba74" />
            <Bar dataKey="essCharge" name="заряд УЗЕ" stackId="neg" fill="#fb923c" />
            <Bar dataKey="gridExport" name="експорт у мережу" stackId="neg" fill="#f97316" />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </section>
  )
}

// --- ESS fact vs optimum (stub) ---
//
// Deferred in the MVP: the "captured vs reserve" comparison needs an
// optimiser that models the theoretical ESS maximum from realised RDN
// prices, PV, load, SOC, capacity, power and degradation. Until that
// model exists we render an explicit placeholder rather than fake
// numbers, so the panel still occupies its mockup slot.
function MonthlyOptimumStub() {
  const kpis = [
    { label: 'Оптимум', value: '—' },
    { label: 'Факт', value: '—' },
    { label: 'Захоплено', value: '—' },
    { label: 'Резерв', value: '—' },
  ]
  return (
    <section className="economics-card economics-month-section economics-optimum-stub" aria-label="УЗЕ: факт vs оптимум">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">УЗЕ: факт vs оптимум</h3>
        <div className="economics-month-muted">ефект, грн</div>
      </div>
      <div className="economics-optimum-kpis">
        {kpis.map((k) => (
          <div key={k.label} className="economics-optimum-kpi">
            <span className="economics-optimum-kpi-label">{k.label}</span>
            <span className="economics-optimum-kpi-value">{k.value}</span>
          </div>
        ))}
      </div>
      <p className="economics-month-empty-note">
        Панель потребує моделі оптимуму УЗЕ — максимального ефекту за фактичних
        цін РДН, генерації СЕС, споживання, ємності, SOC, потужності, ККД та
        зносу. У поточній версії (MVP) ще не реалізовано.
      </p>
    </section>
  )
}

// --- Energy balance ---

function MonthlyBalance({ totals }: { totals: EconomicsMonthlyTotals }) {
  const sourcesTotal = totals.pv_kwh + totals.grid_import_kwh
  const pct = (v: number) => (sourcesTotal > 0 ? (v / sourcesTotal) * 100 : 0)
  const gridToLoadCost = totals.grid_to_load_kwh * totals.avg_import_price_uah_per_kwh

  // Where the sourced energy is allocated. These three sinks sum to the
  // sources total (PV = load+ess+grid, grid import = load+ess), so the
  // "directions" bar is a clean split with no double counting.
  const toConsumption = totals.pv_to_load_kwh + totals.grid_to_load_kwh
  const toEss = totals.pv_to_ess_kwh + totals.grid_to_ess_kwh
  const toExport = totals.pv_to_grid_kwh

  const flows = [
    { label: 'СЕС → споживання', kwh: totals.pv_to_load_kwh, effect: totals.revenue_pv_self_uah, cls: 'good', dot: 'consume' },
    { label: 'СЕС → мережа', kwh: totals.pv_to_grid_kwh, effect: totals.revenue_pv_export_uah, cls: 'good', dot: 'export' },
    { label: 'СЕС → УЗЕ', kwh: totals.pv_to_ess_kwh, effect: 0, cls: 'neutral', dot: 'ess' },
    { label: 'УЗЕ → споживання', kwh: totals.ess_to_load_kwh, effect: totals.revenue_ess_self_uah, cls: 'good', dot: 'consume' },
    { label: 'УЗЕ → мережа', kwh: totals.ess_to_grid_kwh, effect: totals.revenue_ess_export_uah, cls: 'good', dot: 'export' },
    { label: 'Мережа → споживання', kwh: totals.grid_to_load_kwh, effect: -gridToLoadCost, cls: 'bad', dot: 'grid' },
    { label: 'Мережа → УЗЕ', kwh: totals.grid_to_ess_kwh, effect: -totals.expense_grid_charge_uah, cls: 'bad', dot: 'ess' },
  ]
  const flowTotalKwh = flows.reduce((acc, f) => acc + f.kwh, 0)

  return (
    <section className="economics-card economics-month-section" aria-label="Енергетичний баланс за місяць">
      <div className="economics-month-section-head">
        <h3 className="economics-month-section-title">Енергетичний баланс за місяць</h3>
        <span className="economics-month-muted">МВт·год за місяць</span>
      </div>
      <div className="economics-flow-legend">
        <span><i className="economics-seg-dot green" />СЕС</span>
        <span><i className="economics-seg-dot blue" />мережа</span>
        <span><i className="economics-seg-dot amber" />споживання</span>
        <span><i className="economics-seg-dot violet" />УЗЕ</span>
        <span><i className="economics-seg-dot red" />експорт</span>
      </div>
      <div className="economics-month-balance">
        <div className="economics-balance-row">
          <strong className="economics-balance-name">Джерела</strong>
          <div className="economics-balance-bar">
            <span className="economics-seg green" style={{ width: `${pct(totals.pv_kwh)}%` }} />
            <span className="economics-seg blue" style={{ width: `${pct(totals.grid_import_kwh)}%` }} />
          </div>
          <span className="economics-balance-value">{formatMwh(sourcesTotal)}</span>
        </div>
        <div className="economics-balance-row">
          <span className="economics-balance-name">СЕС</span>
          <div className="economics-balance-bar">
            <span className="economics-seg green" style={{ width: `${pct(totals.pv_kwh)}%` }} />
          </div>
          <span className="economics-balance-value">{formatMwh(totals.pv_kwh)}</span>
        </div>
        <div className="economics-balance-row">
          <span className="economics-balance-name">Мережа</span>
          <div className="economics-balance-bar">
            <span className="economics-seg blue" style={{ width: `${pct(totals.grid_import_kwh)}%` }} />
          </div>
          <span className="economics-balance-value">{formatMwh(totals.grid_import_kwh)}</span>
        </div>
        <div className="economics-balance-row">
          <strong className="economics-balance-name">Напрямки</strong>
          <div className="economics-balance-bar">
            <span className="economics-seg amber" style={{ width: `${pct(toConsumption)}%` }} />
            <span className="economics-seg red" style={{ width: `${pct(toExport)}%` }} />
            <span className="economics-seg violet" style={{ width: `${pct(toEss)}%` }} />
          </div>
          <span className="economics-balance-value">{formatMwh(sourcesTotal)}</span>
        </div>
      </div>
      <div className="economics-table-scroll" style={{ marginTop: 14 }}>
        <table className="economics-table economics-month-table">
          <thead>
            <tr>
              <th>Потік</th>
              <th>кВт·год</th>
              <th>Частка</th>
              <th>Фін. ефект</th>
            </tr>
          </thead>
          <tbody>
            {flows.map((f) => (
              <tr key={f.label}>
                <td className="economics-month-table-left">
                  <span className={`economics-flow-dot ${f.dot}`} aria-hidden="true" />
                  {f.label}
                </td>
                <td>{formatKwh(f.kwh)}</td>
                <td>{formatShare(f.kwh, flowTotalKwh)}</td>
                <td className={`cell-${f.cls === 'good' ? 'positive' : f.cls === 'bad' ? 'negative' : 'neutral'}`}>
                  {f.effect === 0 ? '0 грн' : formatUah(f.effect)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
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
