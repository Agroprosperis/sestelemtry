import { formatChartNumber } from '../format'
import { SINK_ENERGY_METRIC_KEYS, SOURCE_ENERGY_METRIC_KEYS } from '../metrics'
import type { RangePreset } from '../range'

type EnergyTooltipEntry = {
  dataKey?: string | number | ((obj: unknown) => unknown)
  name?: string | number
  value?: unknown
  color?: string
}

type Props = {
  active?: boolean
  label?: string | number
  payload?: readonly EnergyTooltipEntry[]
  preset: RangePreset
}

export function EnergyTooltip({ active, label, payload, preset }: Props) {
  if (!active || !payload || payload.length === 0 || preset === 'day') {
    return null
  }

  const byKey = new Map<string, EnergyTooltipEntry>()
  for (const entry of payload) {
    if (typeof entry.dataKey === 'string') byKey.set(entry.dataKey, entry)
  }

  const sourceTotal = SOURCE_ENERGY_METRIC_KEYS.reduce((sum, key) => {
    const v = Number(byKey.get(key)?.value)
    if (!Number.isFinite(v)) return sum
    return sum + Math.max(v, 0)
  }, 0)

  const sinkTotal = SINK_ENERGY_METRIC_KEYS.reduce((sum, key) => {
    const v = Number(byKey.get(key)?.value)
    if (!Number.isFinite(v)) return sum
    return sum + Math.abs(v)
  }, 0)

  function row(key: string, asAbs = false) {
    const entry = byKey.get(key)
    const raw = Number(entry?.value)
    const value = Number.isFinite(raw) ? (asAbs ? Math.abs(raw) : raw) : null
    return (
      <div key={key} className="energy-tooltip-row">
        <span className="energy-tooltip-dot" style={{ backgroundColor: entry?.color ?? '#94a3b8' }} />
        <span className="energy-tooltip-name">{entry?.name ? String(entry.name) : key}</span>
        <span className="energy-tooltip-value">
          {value === null ? '--' : `${formatChartNumber(value)} kWh`}
        </span>
      </div>
    )
  }

  return (
    <div className="energy-tooltip">
      <div className="energy-tooltip-label">{label}</div>
      <div className="energy-tooltip-grid">
        <div>
          <div className="energy-tooltip-head">
            <span>Джерела енергії</span>
            <span>{formatChartNumber(sourceTotal)} kWh</span>
          </div>
          {SOURCE_ENERGY_METRIC_KEYS.map((key) => row(key))}
        </div>
        <div>
          <div className="energy-tooltip-head">
            <span>Стоки енергії</span>
            <span>{formatChartNumber(sinkTotal)} kWh</span>
          </div>
          {SINK_ENERGY_METRIC_KEYS.map((key) => row(key, true))}
        </div>
      </div>
    </div>
  )
}
