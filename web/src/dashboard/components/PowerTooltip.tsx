import { formatChartNumber } from '../format'
import { DAY_POWER_METRIC_KEYS, DAY_POWER_METRIC_LABELS } from '../metrics'

type PowerTooltipEntry = {
  dataKey?: string | number | ((obj: unknown) => unknown)
  name?: string | number
  value?: unknown
  color?: string
  // The whole chart row recharts hit-tested, which carries the values we
  // display but don't draw as their own series (e.g. the plan's reason).
  payload?: Record<string, unknown>
}

type Props = {
  active?: boolean
  label?: string | number
  payload?: readonly PowerTooltipEntry[]
}

const DAM_PRICE_KEY = 'dam_price_uah_per_mwh'
const SOC_KEY = 'soc_percent'
const PV_FORECAST_KEY = 'planned_ac_kw'
const AI_ESS_KEY = 'ai_ess_power_kw'
const AI_SOC_KEY = 'ai_soc_pct'
const AI_LOAD_KEY = 'ai_load_kw'
const AI_REASON_KEY = 'ai_reason_text'

// Anything closer to zero than this is treated as "idle" — both the
// ESS and the grid meter routinely sit at ±tens of watts even when
// nothing is happening (inverter standby losses), and flipping the
// label between Заряд/Розряд / Імпорт/Експорт on every poll noise
// just makes the tooltip flicker. 50 W is small enough to call
// "idle" without hiding genuine activity.
const IDLE_KW = 0.05

// directionalLabel produces a sign-aware tooltip name for the two
// metrics whose direction matters operationally: ESS (charge vs
// discharge) and the grid meter (import vs export). The convention
// matches the production SmartLogger firmware on PE/ZE:
//   * active_ess_power_kw > 0 → battery is delivering power
//     (розряд / discharge); < 0 → battery is taking power in
//     (заряд / charge). Confirmed against live readings on
//     2026-05-09 (PV 97 < load 198, ESS slowly discharging at
//     -0.82 was actually a charge, the value reads negative when
//     charging).
//   * grid_connected_active_power_kw > 0 → site pulling power
//     from the external grid (import / купівля); < 0 → exporting.
// Around zero we fall back to a neutral label so the tooltip doesn't
// flicker between charge/discharge on inverter standby noise.
function directionalLabel(metricKey: string, value: number): string | null {
  if (metricKey === 'active_ess_power_kw') {
    if (value > IDLE_KW) return 'Розряд УЗЕ'
    if (value < -IDLE_KW) return 'Заряд УЗЕ'
    return 'УЗЕ в очікуванні'
  }
  if (metricKey === 'grid_connected_active_power_kw') {
    if (value > IDLE_KW) return 'Імпорт з мережі'
    if (value < -IDLE_KW) return 'Експорт у мережу'
    return 'Точка приєднання (без обміну)'
  }
  return null
}

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

  // The recommendation is signed like the ESS metric, so it gets the same
  // charge/discharge treatment: named by direction, shown as a magnitude.
  const aiEssEntry = byKey.get(AI_ESS_KEY)
  const aiEssValue = Number(aiEssEntry?.value)
  const showAiEss = Number.isFinite(aiEssValue)
  const aiEssName =
    aiEssValue > IDLE_KW
      ? 'ШІ: розряд УЗЕ'
      : aiEssValue < -IDLE_KW
        ? 'ШІ: заряд УЗЕ'
        : 'ШІ: утримувати'
  // SOC lands on the hour's closing bucket only (it's the end-of-hour
  // state), same as the PV forecast above.
  const aiSocEntry = byKey.get(AI_SOC_KEY)
  const aiSocValue = Number(aiSocEntry?.value)
  const showAiSoc = Number.isFinite(aiSocValue)
  // The recommended load is stored negated on the row (drawn as a sink);
  // flip it back to a positive consumption number, like the load row.
  const aiLoadEntry = byKey.get(AI_LOAD_KEY)
  const aiLoadValue = Number(aiLoadEntry?.value)
  const showAiLoad = Number.isFinite(aiLoadValue)
  const reasonRaw = payload.find((e) => e.payload?.[AI_REASON_KEY] != null)?.payload?.[AI_REASON_KEY]
  const reasonText = typeof reasonRaw === 'string' ? reasonRaw : ''

  return (
    <div className="energy-tooltip">
      <div className="energy-tooltip-label">{label}</div>
      <div>
        {DAY_POWER_METRIC_KEYS.map((key) => {
          const entry = byKey.get(key)
          const raw = Number(entry?.value)
          const value = Number.isFinite(raw) ? raw : null
          // Sign-aware metrics get a direction-specific label and the
          // displayed magnitude is the absolute value — analysts read
          // "Заряд УЗЕ: 0.82 кВт" much faster than "УЗЕ: -0.82 kW"
          // and don't have to remember the firmware's sign rule.
          // load_power_kw is stored negated on the row so the chart
          // draws it as a sink below zero, but the tooltip flips it
          // back to a positive consumption number.
          const dirLabel = value !== null ? directionalLabel(key, value) : null
          const name =
            dirLabel ??
            (entry?.name ? String(entry.name) : (DAY_POWER_METRIC_LABELS[key] ?? key))
          const isLoad = key === 'load_power_kw'
          const displayed =
            value === null ? null : dirLabel || isLoad ? Math.abs(value) : value
          return (
            <div key={key} className="energy-tooltip-row">
              <span
                className="energy-tooltip-dot"
                style={{ backgroundColor: entry?.color ?? '#94a3b8' }}
              />
              <span className="energy-tooltip-name">{name}</span>
              <span className="energy-tooltip-value">
                {displayed === null ? '--' : `${formatChartNumber(displayed)} kW`}
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
      {showAiEss && (
        <div className="energy-tooltip-row energy-tooltip-dam">
          <span
            className="energy-tooltip-dot"
            style={{ backgroundColor: aiEssEntry?.color ?? '#db2777' }}
          />
          <span className="energy-tooltip-name">{aiEssName}</span>
          <span className="energy-tooltip-value">
            {formatChartNumber(Math.abs(aiEssValue))} кВт
          </span>
        </div>
      )}
      {showAiSoc && (
        <div className="energy-tooltip-row energy-tooltip-dam">
          <span
            className="energy-tooltip-dot"
            style={{ backgroundColor: aiSocEntry?.color ?? '#9333ea' }}
          />
          <span className="energy-tooltip-name">SOC за планом ШІ</span>
          <span className="energy-tooltip-value">{formatChartNumber(aiSocValue)} %</span>
        </div>
      )}
      {showAiLoad && (
        <div className="energy-tooltip-row energy-tooltip-dam">
          <span
            className="energy-tooltip-dot"
            style={{ backgroundColor: aiLoadEntry?.color ?? '#d97706' }}
          />
          <span className="energy-tooltip-name">ШІ: споживання</span>
          <span className="energy-tooltip-value">
            {formatChartNumber(Math.abs(aiLoadValue))} кВт
          </span>
        </div>
      )}
      {showAiEss && reasonText && <div className="energy-tooltip-reason">{reasonText}</div>}
    </div>
  )
}
