import {
  ArrowDownLeft,
  ArrowUpRight,
  BatteryFull,
  Buildings,
  Sun,
} from '@phosphor-icons/react'
import type { RegisterMeta } from '../../types'
import type { RangePreset } from '../range'
import type { EnergySummary } from '../transforms/summary'
import { formatEnergyCompactKWhUk, formatPeriodLabel } from '../format'
import { ModbusAddr } from './ModbusAddr'

type Props = {
  summary: EnergySummary
  preset: RangePreset
  anchor: Date
  debug: boolean
  registers: Record<string, RegisterMeta> | null
  // pvForecastTotal is the planned generation for the period in kWh.
  // null = forecast not available (no n8n mapping for the org, or
  // non-day preset where we don't fetch hourly forecasts). When
  // populated, the "СЕС згенерувала" line gains a "vs plan" suffix.
  pvForecastTotal: number | null
}

const TITLES: Record<RangePreset, string> = {
  day: 'Підсумок за день',
  month: 'Підсумок за місяць',
  year: 'Підсумок за рік',
}

const formatKWhUk = formatEnergyCompactKWhUk
const ICON_SIZE = 20

export function DailySummaryNarrative({
  summary,
  preset,
  anchor,
  debug,
  registers,
  pvForecastTotal,
}: Props) {
  const exportIsTiny = summary.gridExport > 0 && summary.gridExport < 1
  const periodLabel = formatPeriodLabel(preset, anchor)
  // Forecast comparison is only meaningful when we have a non-zero
  // plan; pvForecastTotal is null when the org has no forecast (e.g.
  // demo-org) or the preset isn't day. We treat exactly-zero as
  // "no plan" too because the n8n flow sometimes returns an empty
  // forecast as 0 kWh and a 0% completion bar would be misleading.
  const hasForecast =
    pvForecastTotal !== null &&
    Number.isFinite(pvForecastTotal) &&
    pvForecastTotal > 0
  const forecastCompletionPct =
    hasForecast && pvForecastTotal! > 0
      ? Math.round((summary.pvProduced / pvForecastTotal!) * 100)
      : null
  return (
    <section
      className="metrics-group daily-narrative"
      aria-labelledby="daily-narrative-title"
    >
      <h2 id="daily-narrative-title" className="metrics-group-title">
        {TITLES[preset]}
        <span className="metrics-group-subtitle"> · {periodLabel}</span>
      </h2>
      <ul className="daily-narrative-list">
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <Sun size={ICON_SIZE} weight="duotone" color="#f59e0b" />
          </span>
          <span>
            СЕС згенерувала
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_pv_energy_yield_kwh"
            />
            : <strong>{formatKWhUk(summary.pvProduced)}</strong>
            {hasForecast && (
              <span className="daily-narrative-note">
                {' '}· прогноз <strong>{formatKWhUk(pvForecastTotal!)}</strong>
                {forecastCompletionPct !== null && ` (${forecastCompletionPct}%)`}
              </span>
            )}
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <Buildings size={ICON_SIZE} weight="duotone" color="#7c3aed" />
          </span>
          <span>
            Споживання приладами
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_power_consumption_kwh"
            />
            : <strong>{formatKWhUk(summary.consumption)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <ArrowDownLeft size={ICON_SIZE} weight="bold" color="#3b82f6" />
          </span>
          <span>
            Взяли з мережі
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_electricity_purchased_kwh"
            />
            : <strong>{formatKWhUk(summary.fromGrid)}</strong>
          </span>
        </li>
        <li>
          <span className="daily-narrative-icon" aria-hidden="true">
            <ArrowUpRight size={ICON_SIZE} weight="bold" color="#22c55e" />
          </span>
          <span>
            Віддали в мережу
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys="accumulated_electricity_sold_kwh"
            />
            : <strong>{formatKWhUk(summary.gridExport)}</strong>
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
            Батарея
            <ModbusAddr
              debug={debug}
              registers={registers}
              keys={['total_energy_charged_kwh', 'total_energy_discharged_kwh']}
            />
            : заряд <strong>{formatKWhUk(summary.batteryCharged)}</strong>, розряд{' '}
            <strong>{formatKWhUk(summary.batteryDischarged)}</strong>
          </span>
        </li>
      </ul>
    </section>
  )
}
