import {
  ArrowDownLeft,
  ArrowUpRight,
  BatteryFull,
  Buildings,
  Sun,
} from '@phosphor-icons/react'
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
const ICON_SIZE = 20

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
            <Sun size={ICON_SIZE} weight="duotone" color="#f59e0b" />
          </span>
          <span>
            СЕС згенерувала: <strong>{formatKWhUk(summary.pvProduced)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <Buildings size={ICON_SIZE} weight="duotone" color="#7c3aed" />
          </span>
          <span>
            Споживання приладами:{' '}
            <strong>{formatKWhUk(summary.consumption)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <ArrowDownLeft size={ICON_SIZE} weight="bold" color="#3b82f6" />
          </span>
          <span>
            Взяли з мережі: <strong>{formatKWhUk(summary.fromGrid)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <ArrowUpRight size={ICON_SIZE} weight="bold" color="#22c55e" />
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
            <BatteryFull size={ICON_SIZE} weight="duotone" color="#22c55e" />
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
