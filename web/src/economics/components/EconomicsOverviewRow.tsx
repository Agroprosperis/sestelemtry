import { Cell, Line, LineChart, CartesianGrid, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import type { EconomicsMonthlyTotals } from '../../api'
import { useChartChrome } from '../../theme/useChartChrome'
import { formatCycles, formatMwh, formatMwhNumber, formatPercent } from '../monthly/format'
import { PERIOD_WORDS, type PeriodScope } from '../monthly/rollup'

// ProfilePoint is one X-axis bucket of the consumption/generation
// profile: a day of the month view or a month of the annual view.
// All energies are in kWh; the chart divides down to МВт·год itself.
export type ProfilePoint = {
  label: string
  loadKwh: number
  pvKwh: number
  gridImportKwh: number
  essDischargeKwh: number
}

type Props = {
  totals: EconomicsMonthlyTotals
  scope?: PeriodScope
  profile: ProfilePoint[]
}

// Series palette matches the rest of the economics charts: СЕС green,
// імпорт blue, УЗЕ violet; the load line takes the theme text colour.
const COLOR_PV = '#12b76a'
const COLOR_IMPORT = '#2f6fed'
const COLOR_ESS = '#7c3aed'

const mwhFmt = new Intl.NumberFormat('uk-UA', { minimumFractionDigits: 1, maximumFractionDigits: 1 })

type ProfileTooltipProps = {
  active?: boolean
  label?: string | number
  payload?: Array<{ name?: string; value?: number | string; color?: string }>
}

function ProfileTooltip({ active, payload, label }: ProfileTooltipProps) {
  if (!active || !payload?.length) return null
  return (
    <div className="economics-trend-tip">
      <div className="economics-trend-tip-day">{label}</div>
      {payload.map((p) => (
        <div className="economics-trend-tip-row" key={p.name}>
          <i style={{ background: p.color }} />
          <span>{p.name}</span>
          <b>{mwhFmt.format(Number(p.value))}</b>
        </div>
      ))}
    </div>
  )
}

// EconomicsOverviewRow is the third strip of the redesigned dashboard
// top (mock bottom row): consumption-coverage donut, the period's
// consumption/generation profile, and the «Робота УЗЕ» mini-cards.
export function EconomicsOverviewRow({ totals, scope = 'month', profile }: Props) {
  const chrome = useChartChrome()
  const w = PERIOD_WORDS[scope]

  // Coverage: who served the load. Segments sum to the served load, so
  // the donut shares and the legend МВт·год always agree.
  const coverage = [
    { key: 'pv', name: 'СЕС → споживання', kwh: totals.pv_to_load_kwh, color: COLOR_PV },
    { key: 'ess', name: 'УЗЕ → споживання', kwh: totals.ess_to_load_kwh, color: COLOR_ESS },
    { key: 'grid', name: 'Імпорт з мережі', kwh: totals.grid_to_load_kwh, color: COLOR_IMPORT },
  ]
  const coverageTotal = coverage.reduce((acc, s) => acc + s.kwh, 0)

  const rows = profile.map((p) => ({
    label: p.label,
    'Споживання об\'єкта': p.loadKwh / 1000,
    'Генерація СЕС': p.pvKwh / 1000,
    'Імпорт з мережі': p.gridImportKwh / 1000,
    'Розряд УЗЕ': p.essDischargeKwh / 1000,
  }))

  // УЗЕ mini-cards. RTE is the plain energy round-trip of the period:
  // discharged / charged; the cost-side of the battery lives in the
  // fact-vs-optimum and cost-basis sections below.
  const days = Math.max(1, totals.days_with_data)
  const rte = totals.ess_charged_kwh > 0 ? totals.ess_discharged_kwh / totals.ess_charged_kwh : NaN
  const uzeCards = [
    {
      label: 'Еквівалентні цикли',
      value: formatCycles(totals.equivalent_cycles),
      sub: `${formatCycles(totals.equivalent_cycles / days)} на добу`,
    },
    {
      label: 'Розряд УЗЕ',
      value: formatMwh(totals.ess_discharged_kwh),
      sub: `середньо ${mwhFmt.format(totals.ess_discharged_kwh / 1000 / days)} МВт·год/добу`,
    },
    {
      label: 'Заряд УЗЕ',
      value: formatMwh(totals.ess_charged_kwh),
      sub: `СЕС: ${formatMwhNumber(totals.pv_to_ess_kwh)} | Мережа: ${formatMwhNumber(totals.grid_to_ess_kwh)}`,
    },
    {
      label: 'Ефективність (RTE)',
      value: formatPercent(rte),
      sub: 'розряд / заряд за період',
    },
  ]

  return (
    <div className="eco-overview">
      <section className="economics-card eco-overview-panel" aria-label="Структура покриття споживання">
        <h3 className="eco-overview-title">Структура покриття споживання</h3>
        <div className="eco-donut-wrap">
          <div className="eco-donut">
            <ResponsiveContainer width="100%" height={170}>
              <PieChart>
                <Pie
                  data={coverage}
                  dataKey="kwh"
                  nameKey="name"
                  innerRadius={52}
                  outerRadius={78}
                  strokeWidth={0}
                  paddingAngle={2}
                >
                  {coverage.map((s) => (
                    <Cell key={s.key} fill={s.color} />
                  ))}
                </Pie>
              </PieChart>
            </ResponsiveContainer>
            <div className="eco-donut-center">
              <b>{formatMwhNumber(coverageTotal)}</b>
              <span>МВт·год</span>
              <span>всього</span>
            </div>
          </div>
          <div className="eco-donut-legend">
            {coverage.map((s) => (
              <div className="eco-donut-legend-row" key={s.key}>
                <i style={{ background: s.color }} />
                <span className="eco-donut-legend-name">{s.name}</span>
                <b>{coverageTotal > 0 ? formatPercent(s.kwh / coverageTotal) : '—'}</b>
                <span className="eco-donut-legend-mwh">{formatMwhNumber(s.kwh)}</span>
              </div>
            ))}
          </div>
        </div>
      </section>

      <section className="economics-card eco-overview-panel" aria-label="Профіль споживання та генерації">
        <div className="eco-overview-head">
          <h3 className="eco-overview-title">Профіль споживання та генерації</h3>
          <span className="economics-month-muted">МВт·год {w.per}</span>
        </div>
        <div className="eco-profile-chart">
          <ResponsiveContainer width="100%" height={190}>
            <LineChart data={rows} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
              <CartesianGrid strokeDasharray="2 5" stroke={chrome.grid} vertical={false} />
              <XAxis dataKey="label" tick={{ fontSize: 10, fill: chrome.tick }} tickLine={false} axisLine={false} />
              <YAxis tick={{ fontSize: 11, fill: chrome.axis }} width={38} tickLine={false} axisLine={false} />
              <Tooltip content={<ProfileTooltip />} cursor={{ stroke: chrome.cursorStroke, strokeDasharray: '3 3' }} />
              <Line type="monotone" dataKey="Споживання об'єкта" stroke={chrome.label} strokeWidth={2} dot={false} />
              <Line type="monotone" dataKey="Генерація СЕС" stroke={COLOR_PV} strokeWidth={2} dot={false} />
              <Line type="monotone" dataKey="Імпорт з мережі" stroke={COLOR_IMPORT} strokeWidth={2} dot={false} />
              <Line type="monotone" dataKey="Розряд УЗЕ" stroke={COLOR_ESS} strokeWidth={2} dot={false} />
            </LineChart>
          </ResponsiveContainer>
        </div>
        <div className="economics-trend-legend">
          <span><i style={{ background: chrome.label }} />споживання об'єкта</span>
          <span><i style={{ background: COLOR_PV }} />генерація СЕС</span>
          <span><i style={{ background: COLOR_IMPORT }} />імпорт з мережі</span>
          <span><i style={{ background: COLOR_ESS }} />розряд УЗЕ</span>
        </div>
      </section>

      <section className="economics-card eco-overview-panel" aria-label="Робота УЗЕ">
        <h3 className="eco-overview-title">Робота УЗЕ</h3>
        <div className="eco-uze-grid">
          {uzeCards.map((c) => (
            <div className="eco-uze-card" key={c.label}>
              <span className="eco-uze-label">{c.label}</span>
              <span className="eco-uze-value">{c.value}</span>
              <span className="eco-uze-sub">{c.sub}</span>
            </div>
          ))}
        </div>
      </section>
    </div>
  )
}
