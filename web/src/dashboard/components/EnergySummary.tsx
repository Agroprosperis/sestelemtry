import type { ReactNode } from 'react'
import { formatChartNumber, formatEnergyCompactKWh } from '../format'
import type { EnergySummary as Summary } from '../transforms/summary'

type Props = {
  summary: Summary
  // loading swaps every dynamic number/bar in the card for a
  // shimmer placeholder so switching dates surfaces the same
  // "data is in flight" cue the chart skeleton already provides
  // below. Static labels stay visible — they don't change with
  // the date and removing them would needlessly destabilise the
  // layout while the request resolves.
  loading?: boolean
}

// Skel renders a tabular-numeric placeholder block with the
// shimmer animation defined in dashboard.css. We use spans (not
// divs) so they can sit inline next to labels without breaking
// the existing flex layout. Width / class is supplied by the
// caller so the placeholder approximates the real value's
// footprint and the layout doesn't visibly hop on resolve.
function Skel({ className }: { className: string }) {
  return <span className={`energy-summary-skel ${className}`} aria-hidden="true" />
}

// renderValue picks between a static value and a skeleton
// placeholder based on `loading`. We keep this as a tiny helper
// so the JSX stays scannable: every dynamic slot reads
// `renderValue(loading, '…value…', 'skel-class')`.
function renderValue(
  loading: boolean,
  value: ReactNode,
  skelClass: string,
): ReactNode {
  if (loading) return <Skel className={skelClass} />
  return value
}

export function EnergySummary({ summary, loading = false }: Props) {
  const ariaBusy = loading ? true : undefined
  return (
    <div className="energy-summary-grid" aria-busy={ariaBusy}>
      <section className="energy-summary-card">
        <div className="energy-summary-title">
          <span>Вироблено фотоелектричною установкою</span>
          <strong>
            {renderValue(
              loading,
              formatEnergyCompactKWh(summary.pvProduced),
              'energy-summary-skel-value',
            )}
          </strong>
        </div>
        <div className="energy-summary-split">
          <span>
            {renderValue(
              loading,
              `${formatChartNumber(summary.pvConsumedPct)}%`,
              'energy-summary-skel-pct',
            )}
          </span>
          <span>
            {renderValue(
              loading,
              `${formatChartNumber(summary.pvExportPct)}%`,
              'energy-summary-skel-pct',
            )}
          </span>
        </div>
        <div className="energy-summary-bar">
          {loading ? (
            <span className="energy-summary-skel energy-summary-skel-bar" aria-hidden="true" />
          ) : (
            <span
              className="energy-summary-fill source"
              style={{ width: `${Math.min(summary.pvConsumedPct, 100)}%` }}
            />
          )}
        </div>
        <div className="energy-summary-rows">
          <div className="energy-summary-row">
            <span>Спожито</span>
            <strong>
              {renderValue(
                loading,
                formatEnergyCompactKWh(summary.pvConsumed),
                'energy-summary-skel-row',
              )}
            </strong>
          </div>
          <div className="energy-summary-row">
            <span>Подано в електромережу</span>
            <strong>
              {renderValue(
                loading,
                formatEnergyCompactKWh(summary.gridExport),
                'energy-summary-skel-row',
              )}
            </strong>
          </div>
        </div>
      </section>
      <section className="energy-summary-card">
        <div className="energy-summary-title">
          <span>Споживання приладами</span>
          <strong>
            {renderValue(
              loading,
              formatEnergyCompactKWh(summary.consumption),
              'energy-summary-skel-value',
            )}
          </strong>
        </div>
        <div className="energy-summary-split">
          <span>
            {renderValue(
              loading,
              `${formatChartNumber(summary.selfSufficiencyPct)}%`,
              'energy-summary-skel-pct',
            )}
          </span>
          <span>
            {renderValue(
              loading,
              `${formatChartNumber(summary.loadFromGridPct)}%`,
              'energy-summary-skel-pct',
            )}
          </span>
        </div>
        <div className="energy-summary-bar sink">
          {loading ? (
            <span className="energy-summary-skel energy-summary-skel-bar" aria-hidden="true" />
          ) : (
            <span
              className="energy-summary-fill sink"
              style={{ width: `${Math.min(summary.selfSufficiencyPct, 100)}%` }}
            />
          )}
        </div>
        <div className="energy-summary-rows">
          <div className="energy-summary-row">
            <span>Від ФЕ та УЗЕ</span>
            <strong>
              {renderValue(
                loading,
                formatEnergyCompactKWh(summary.fromPV + summary.fromBattery),
                'energy-summary-skel-row',
              )}
            </strong>
          </div>
          <div className="energy-summary-row">
            <span>З електромережі</span>
            <strong>
              {renderValue(
                loading,
                formatEnergyCompactKWh(summary.fromGrid),
                'energy-summary-skel-row',
              )}
            </strong>
          </div>
        </div>
      </section>
    </div>
  )
}
