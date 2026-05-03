import type { RangePreset } from '../range'
import type { EnergySummary } from '../transforms/summary'
import { formatEnergyCompactKWhUk } from '../format'

type Props = {
  summary: EnergySummary
  preset: RangePreset
}

const TITLES: Record<RangePreset, string> = {
  day: 'Підсумок за день',
  month: 'Підсумок за місяць',
  year: 'Підсумок за рік',
}

const formatKWhUk = formatEnergyCompactKWhUk

export function DailySummaryNarrative({ summary, preset }: Props) {
  const exportIsTiny = summary.gridExport > 0 && summary.gridExport < 1
  return (
    <section
      className="metrics-group daily-narrative"
      aria-labelledby="daily-narrative-title"
    >
      <h2 id="daily-narrative-title" className="metrics-group-title">
        {TITLES[preset]}
      </h2>
      <ul className="daily-narrative-list">
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            ☀
          </span>
          <span className="daily-narrative-label">СЕС згенерувала</span>
          <strong className="daily-narrative-value">
            {formatKWhUk(summary.pvProduced)}
          </strong>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            ⚡
          </span>
          <span className="daily-narrative-label">Споживання приладами</span>
          <strong className="daily-narrative-value">
            {formatKWhUk(summary.consumption)}
          </strong>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            🔌
          </span>
          <span className="daily-narrative-label">Взяли з мережі</span>
          <strong className="daily-narrative-value">
            {formatKWhUk(summary.fromGrid)}
          </strong>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            🌐
          </span>
          <span className="daily-narrative-label">Віддали в мережу</span>
          <strong className="daily-narrative-value">
            {formatKWhUk(summary.gridExport)}
            {exportIsTiny && (
              <span className="daily-narrative-note"> (майже 0)</span>
            )}
          </strong>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            🔋
          </span>
          <span className="daily-narrative-label">Заряд батареї</span>
          <strong className="daily-narrative-value">
            {formatKWhUk(summary.batteryCharged)}
          </strong>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            🔋
          </span>
          <span className="daily-narrative-label">Розряд батареї</span>
          <strong className="daily-narrative-value">
            {formatKWhUk(summary.batteryDischarged)}
          </strong>
        </li>
      </ul>
    </section>
  )
}
