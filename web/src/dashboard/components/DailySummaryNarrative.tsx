import type { RangePreset } from '../range'
import type { EnergySummary } from '../transforms/summary'
import { formatChartNumber } from '../format'

type Props = {
  summary: EnergySummary
  preset: RangePreset
}

const TITLES: Record<RangePreset, string> = {
  day: 'Підсумок за день',
  month: 'Підсумок за місяць',
  year: 'Підсумок за рік',
}

// formatKWhUk renders an energy total in Ukrainian units (кВт·год / МВт·год).
// The chart-side EnergySummary block uses English "kWh"/"MWh" because it
// has to fit in a tight column with bars and percentages; the narrative
// reads as a sentence so Cyrillic units feel more natural here.
function formatKWhUk(value: number): string {
  if (!Number.isFinite(value)) return '--'
  if (value >= 1000) return `${formatChartNumber(value / 1000)} МВт·год`
  return `${formatChartNumber(value)} кВт·год`
}

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
          <span>
            СЕС згенерувала: <strong>{formatKWhUk(summary.pvProduced)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            ⚡
          </span>
          <span>
            Споживання приладами:{' '}
            <strong>{formatKWhUk(summary.consumption)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            🔌
          </span>
          <span>
            Взяли з мережі: <strong>{formatKWhUk(summary.fromGrid)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            🌐
          </span>
          <span>
            Віддали в мережу:{' '}
            <strong>{formatKWhUk(summary.gridExport)}</strong>
            {exportIsTiny && (
              <span className="daily-narrative-note"> (майже 0)</span>
            )}
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            🔋
          </span>
          <span>
            Батарея: заряд{' '}
            <strong>{formatKWhUk(summary.batteryCharged)}</strong>, розряд{' '}
            <strong>{formatKWhUk(summary.batteryDischarged)}</strong>
          </span>
        </li>
      </ul>
    </section>
  )
}
