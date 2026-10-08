import type { SeriesToggles } from './chartSvg'
import { flowMeta, fmt, hasValue, timeLabel, type ActionGroup, type Clock } from './model'

export type ChartLayoutMode = 'split' | 'combined'

const seriesKeys = [
  ['price', 'd-price', 'РДН'],
  ['pv', 'd-pv', 'СЕС'],
  ['load', 'd-load', 'Споживання'],
  ['grid', 'd-grid', 'Мережа PCC'],
  ['bess', 'd-bess', 'УЗЕ'],
  ['soc', 'd-soc', 'SOC'],
  ['before', 'd-old', 'До зміни'],
  ['plan', 'd-plan', 'План (історія)'],
] as const

export function ChartToolbar({
  stale,
  layout,
  onLayout,
  series,
  onToggle,
}: {
  stale: boolean
  layout: ChartLayoutMode
  onLayout: (l: ChartLayoutMode) => void
  series: SeriesToggles
  onToggle: (key: keyof SeriesToggles) => void
}) {
  return (
    <>
      <div className="d-chart-head">
        <h3>Прогноз і керування{stale ? ' · розрахунок…' : ''}</h3>
        <div className="d-chart-modes" role="group" aria-label="Компонування графіка">
          {(['split', 'combined'] as const).map((l) => (
            <button key={l} type="button" aria-pressed={layout === l} onClick={() => onLayout(l)}>
              {l === 'split' ? 'Розділено' : 'Разом'}
            </button>
          ))}
        </div>
      </div>
      <div className="d-series-controls" role="group" aria-label="Показники на графіку">
        {seriesKeys.map(([key, cls, label]) => (
          <button
            key={key}
            type="button"
            className="d-key"
            aria-pressed={series[key]}
            aria-label={key === 'bess' ? 'УЗЕ: заряд від СЕС жовтим, із мережі сірим; споживання зеленим, експорт помаранчевим' : undefined}
            onClick={() => onToggle(key)}
          >
            <b className={cls} />
            {label}
          </button>
        ))}
      </div>
    </>
  )
}

export function ChartNotes({
  series,
  overflow,
  clock,
  span,
  startSoc,
  endSoc,
  selectedSoc,
  completeSoc,
  reserve,
}: {
  series: SeriesToggles
  overflow: ActionGroup[]
  clock: Clock
  span: string
  startSoc: number | null
  endSoc: number | null
  selectedSoc: (number | null)[]
  completeSoc: boolean
  reserve: number
}) {
  return (
    <>
      {series.grid && (
        <div className="d-grid-legend">
          <span>
            <b />
            Мережа PCC · весь об’єкт, кВт
          </span>
          <span>вище нуля — імпорт із мережі</span>
          <span>нижче нуля — експорт у мережу</span>
        </div>
      )}
      {overflow.length > 0 && (
        <div className="d-action-overflow" aria-label="Команди коротких інтервалів">
          {overflow.map((g, k) => (
            <span key={k} className={g.status === 'preview' ? 'is-preview' : ''}>
              {k + 1}. {timeLabel(clock, g.start)}–{timeLabel(clock, g.end)} · {g.mark.detail}
              {g.status === 'preview' ? ' · перегляд' : ''}
            </span>
          ))}
        </div>
      )}
      <div className="d-chart-note">
        {series.bess && (
          <span className="d-flow-legend" aria-label="Джерела заряду та призначення розряду УЗЕ">
            {Object.entries(flowMeta).map(([key, meta]) => (
              <span className="d-key" key={key}>
                <b style={{ background: meta.color }} />
                {meta.name}
              </span>
            ))}
          </span>
        )}
        {series.soc && (
          <span className="d-soc-readout" aria-live="polite">
            <span>SOC · {span}</span>
            {completeSoc ? (
              <>
                <strong>
                  {fmt(startSoc)}% → {fmt(endSoc)}%
                </strong>
                <span>
                  мін. {fmt(Math.min(startSoc as number, ...(selectedSoc as number[])))}% · резерв {fmt(reserve)}%
                </span>
              </>
            ) : (
              <>
                <strong>Немає прогнозу</strong>
                <span>
                  {hasValue(startSoc) ? 'на початку ' + fmt(startSoc) + '% · ' : ''}резерв {fmt(reserve)}%
                </span>
              </>
            )}
          </span>
        )}
        <span>Клік — деталі · протягніть — інтервал</span>
      </div>
    </>
  )
}
