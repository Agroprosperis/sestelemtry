import { formatChartNumber, formatEnergyCompactKWh } from '../format'
import type { EnergySummary as Summary } from '../transforms/summary'

type Props = {
  summary: Summary
}

export function EnergySummary({ summary }: Props) {
  return (
    <div className="energy-summary-grid">
      <section className="energy-summary-card">
        <div className="energy-summary-title">
          <span>Вироблено фотоелектричною установкою</span>
          <strong>{formatEnergyCompactKWh(summary.pvProduced)}</strong>
        </div>
        <div className="energy-summary-split">
          <span>{formatChartNumber(summary.pvConsumedPct)}%</span>
          <span>{formatChartNumber(summary.pvExportPct)}%</span>
        </div>
        <div className="energy-summary-bar">
          <span
            className="energy-summary-fill source"
            style={{ width: `${Math.min(summary.pvConsumedPct, 100)}%` }}
          />
        </div>
        <div className="energy-summary-rows">
          <div className="energy-summary-row">
            <span>Спожито</span>
            <strong>{formatEnergyCompactKWh(summary.pvConsumed)}</strong>
          </div>
          <div className="energy-summary-row">
            <span>Подано в електромережу</span>
            <strong>{formatEnergyCompactKWh(summary.gridExport)}</strong>
          </div>
        </div>
      </section>
      <section className="energy-summary-card">
        <div className="energy-summary-title">
          <span>Споживання приладами</span>
          <strong>{formatEnergyCompactKWh(summary.consumption)}</strong>
        </div>
        <div className="energy-summary-split">
          <span>{formatChartNumber(summary.selfSufficiencyPct)}%</span>
          <span>{formatChartNumber(summary.loadFromGridPct)}%</span>
        </div>
        <div className="energy-summary-bar sink">
          <span
            className="energy-summary-fill sink"
            style={{ width: `${Math.min(summary.selfSufficiencyPct, 100)}%` }}
          />
        </div>
        <div className="energy-summary-rows">
          <div className="energy-summary-row">
            <span>Від ФЕ та УЗЕ</span>
            <strong>
              {formatEnergyCompactKWh(summary.fromPV + summary.fromBattery)}
            </strong>
          </div>
          <div className="energy-summary-row">
            <span>З електромережі</span>
            <strong>{formatEnergyCompactKWh(summary.fromGrid)}</strong>
          </div>
        </div>
      </section>
    </div>
  )
}
