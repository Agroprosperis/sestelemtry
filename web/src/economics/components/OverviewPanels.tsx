import { Cell, Pie, PieChart, ResponsiveContainer } from 'recharts'
import { InfoDot, type CardTone } from './EconomicsTopSection'
import { KpiIcon } from './kpiIcons'

// CoverageSegment is one slice of the «Структура покриття споживання»
// donut: which source served the load, in kWh.
export type CoverageSegment = { key: string; name: string; kwh: number; color: string }

const shareFmt = new Intl.NumberFormat('uk-UA', {
  style: 'percent',
  minimumFractionDigits: 1,
  maximumFractionDigits: 1,
})

// CoverageDonutPanel is the coverage donut shared by the day and the
// month/year views. The legend is two lines per source — the name, then
// «value unit (share)» — so a long name wraps inside its own line
// instead of running into the numbers, and it drops under the donut
// when the panel is too narrow for both side by side.
export function CoverageDonutPanel({
  segments,
  unit,
  formatValue,
}: {
  segments: CoverageSegment[]
  unit: string
  formatValue: (kwh: number) => string
}) {
  const total = segments.reduce((acc, s) => acc + s.kwh, 0)
  return (
    <section
      className="economics-card eco-overview-panel eco-overview-donut"
      aria-label="Структура покриття споживання"
    >
      <h3 className="eco-overview-title">Структура покриття споживання</h3>
      <div className="eco-donut-wrap">
        <div className="eco-donut">
          <ResponsiveContainer width="100%" height={160}>
            <PieChart>
              <Pie
                data={segments}
                dataKey="kwh"
                nameKey="name"
                innerRadius={50}
                outerRadius={74}
                strokeWidth={0}
                paddingAngle={2}
              >
                {segments.map((s) => (
                  <Cell key={s.key} fill={s.color} />
                ))}
              </Pie>
            </PieChart>
          </ResponsiveContainer>
          <div className="eco-donut-center">
            <b>{formatValue(total)}</b>
            <span>{unit}</span>
            <span>усього</span>
          </div>
        </div>
        <div className="eco-donut-legend">
          {segments.map((s) => (
            <div className="eco-donut-legend-row" key={s.key}>
              <i style={{ background: s.color }} />
              <div className="eco-donut-legend-text">
                <span className="eco-donut-legend-name">{s.name}</span>
                <span className="eco-donut-legend-value">
                  {formatValue(s.kwh)} {unit} (<b>{total > 0 ? shareFmt.format(s.kwh / total) : '—'}</b>)
                </span>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}

// UzeWorkCard is one «Робота УЗЕ» mini-card. `unit` renders smaller
// next to the number and may wrap under it, so a narrow card never
// breaks the number itself.
export type UzeWorkCard = {
  label: string
  value: string
  unit?: string
  sub: string
  tone: CardTone
  icon: string
  tip?: string
}

export function UzeWorkPanel({ cards }: { cards: UzeWorkCard[] }) {
  return (
    <section className="economics-card eco-overview-panel eco-overview-uze" aria-label="Робота УЗЕ">
      <h3 className="eco-overview-title">Робота УЗЕ</h3>
      <div className="eco-uze-grid">
        {cards.map((c) => (
          <div className={`eco-uze-card eco-tone-${c.tone}`} key={c.label}>
            <span className="eco-uze-icon">
              <KpiIcon d={c.icon} />
            </span>
            <div className="eco-uze-body">
              <span className="eco-uze-label">
                {c.label}
                {c.tip ? <InfoDot tip={c.tip} /> : null}
              </span>
              <span className="eco-uze-value">
                <b>{c.value}</b>
                {c.unit ? <small>{c.unit}</small> : null}
              </span>
              <span className="eco-uze-sub">{c.sub}</span>
            </div>
          </div>
        ))}
      </div>
    </section>
  )
}
