import { formatChartNumber, formatEnergyCompactKWhUk } from '../format'
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

const DAM_PRICE_KEY = 'dam_price_uah_per_mwh'
const SOC_KEY = 'soc_percent'

export function EnergyTooltip({ active, label, payload, preset }: Props) {
  if (!active || !payload || payload.length === 0) {
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
          {value === null ? '--' : formatEnergyCompactKWhUk(value)}
        </span>
      </div>
    )
  }

  const damEntry = byKey.get(DAM_PRICE_KEY)
  const damValue = Number(damEntry?.value)
  const showDam = preset === 'day' && Number.isFinite(damValue)
  const socEntry = byKey.get(SOC_KEY)
  const socValue = Number(socEntry?.value)
  const showSoc = preset === 'day' && Number.isFinite(socValue)

  return (
    <div className="energy-tooltip">
      <div className="energy-tooltip-label">{label}</div>
      <div className="energy-tooltip-grid">
        <div>
          <div className="energy-tooltip-head">
            <span>Джерела енергії</span>
            <span>{formatEnergyCompactKWhUk(sourceTotal)}</span>
          </div>
          {SOURCE_ENERGY_METRIC_KEYS.map((key) => row(key))}
        </div>
        <div>
          <div className="energy-tooltip-head">
            <span>Стоки енергії</span>
            <span>{formatEnergyCompactKWhUk(sinkTotal)}</span>
          </div>
          {SINK_ENERGY_METRIC_KEYS.map((key) => row(key, true))}
        </div>
      </div>
      {showSoc && (
        <div className="energy-tooltip-row energy-tooltip-dam">
          <span className="energy-tooltip-dot" style={{ backgroundColor: socEntry?.color ?? '#a855f7' }} />
          <span className="energy-tooltip-name">SOC</span>
          <span className="energy-tooltip-value">{formatChartNumber(socValue)} %</span>
        </div>
      )}
      {showDam && (
        <div className="energy-tooltip-row energy-tooltip-dam">
          <span className="energy-tooltip-dot" style={{ backgroundColor: damEntry?.color ?? '#0ea5e9' }} />
          <span className="energy-tooltip-name">Ціна РДН</span>
          <span className="energy-tooltip-value">{formatChartNumber(damValue)} грн/МВт·год</span>
        </div>
      )}
    </div>
  )
}
