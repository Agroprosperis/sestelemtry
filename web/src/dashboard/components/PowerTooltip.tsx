import { formatChartNumber } from '../format'
import { DAY_POWER_METRIC_KEYS, DAY_POWER_METRIC_LABELS } from '../metrics'

type PowerTooltipEntry = {
  dataKey?: string | number | ((obj: unknown) => unknown)
  name?: string | number
  value?: unknown
  color?: string
}

type Props = {
  active?: boolean
  label?: string | number
  payload?: readonly PowerTooltipEntry[]
}

const DAM_PRICE_KEY = 'dam_price_uah_per_mwh'
const SOC_KEY = 'soc_percent'
const PV_FORECAST_KEY = 'planned_ac_kw'

export function PowerTooltip({ active, label, payload }: Props) {
  if (!active || !payload || payload.length === 0) {
    return null
  }

  const byKey = new Map<string, PowerTooltipEntry>()
  for (const entry of payload) {
    if (typeof entry.dataKey === 'string') byKey.set(entry.dataKey, entry)
  }

  const damEntry = byKey.get(DAM_PRICE_KEY)
  const damValue = Number(damEntry?.value)
  const showDam = Number.isFinite(damValue)
  const socEntry = byKey.get(SOC_KEY)
  const socValue = Number(socEntry?.value)
  const showSoc = Number.isFinite(socValue)
  // Forecast values land on a single bucket per hour (HH:30); on the other
  // 11 buckets the entry exists in the payload but with `value` undefined,
  // so we only render the row when there's an actual number to show.
  const pvForecastEntry = byKey.get(PV_FORECAST_KEY)
  const pvForecastValue = Number(pvForecastEntry?.value)
  const showPvForecast = Number.isFinite(pvForecastValue)

  return (
    <div className="energy-tooltip">
      <div className="energy-tooltip-label">{label}</div>
      <div>
        {DAY_POWER_METRIC_KEYS.map((key) => {
          const entry = byKey.get(key)
          const raw = Number(entry?.value)
          const value = Number.isFinite(raw) ? raw : null
          const name = entry?.name ? String(entry.name) : (DAY_POWER_METRIC_LABELS[key] ?? key)
          return (
            <div key={key} className="energy-tooltip-row">
              <span
                className="energy-tooltip-dot"
                style={{ backgroundColor: entry?.color ?? '#94a3b8' }}
              />
              <span className="energy-tooltip-name">{name}</span>
              <span className="energy-tooltip-value">
                {value === null ? '--' : `${formatChartNumber(value)} kW`}
              </span>
            </div>
          )
        })}
      </div>
      {showPvForecast && (
        <div className="energy-tooltip-row energy-tooltip-dam">
          <span
            className="energy-tooltip-dot"
            style={{ backgroundColor: pvForecastEntry?.color ?? '#16a34a' }}
          />
          <span className="energy-tooltip-name">Прогноз СЕС</span>
          <span className="energy-tooltip-value">
            {formatChartNumber(pvForecastValue)} кВт
          </span>
        </div>
      )}
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
