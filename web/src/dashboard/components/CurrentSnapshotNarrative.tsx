import type { CurrentResponse } from '../../types'
import { formatChartNumber } from '../format'

type Row = {
  key: string
  icon: string
  label: string
  unit: string
}

// ROWS hard-codes the five live metrics that make up the "Поточне
// енергоспоживання" snapshot. Labels are intentionally shorter than the
// backend DashboardMetric labels (which include English glosses) so each
// row fits comfortably on one line in the narrative layout.
const ROWS: Row[] = [
  { key: 'active_pv_power_kw', icon: '☀', label: 'СЕС', unit: 'кВт' },
  { key: 'active_ess_power_kw', icon: '🔋', label: 'УЗЕ', unit: 'кВт' },
  { key: 'load_power_kw', icon: '⚡', label: 'Навантаження', unit: 'кВт' },
  { key: 'grid_connected_active_power_kw', icon: '🔌', label: 'Точка приєднання', unit: 'кВт' },
  { key: 'soc_percent', icon: '📊', label: 'SOC', unit: '%' },
]

type Props = {
  current: CurrentResponse | null
  loading: boolean
}

function formatValue(value: number | null | undefined, unit: string, loading: boolean): string {
  if (loading) return '...'
  if (value == null || !Number.isFinite(value)) return '--'
  return `${formatChartNumber(value)} ${unit}`
}

export function CurrentSnapshotNarrative({ current, loading }: Props) {
  return (
    <section
      className="metrics-group daily-narrative"
      aria-labelledby="current-snapshot-title"
      aria-busy={loading}
    >
      <h2 id="current-snapshot-title" className="metrics-group-title">
        Поточне енергоспоживання
      </h2>
      <ul className="daily-narrative-list">
        {ROWS.map((row) => {
          const value = current?.metrics?.[row.key]?.value ?? null
          return (
            <li key={row.key}>
              <span className="daily-narrative-icon" aria-hidden="true">
                {row.icon}
              </span>
              <span className="daily-narrative-label">{row.label}</span>
              <strong className="daily-narrative-value">
                {formatValue(value, row.unit, loading)}
              </strong>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
