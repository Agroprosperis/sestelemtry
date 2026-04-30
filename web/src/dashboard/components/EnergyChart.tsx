import { useCallback } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { DashboardMetric } from '../../types'
import { energyColor } from '../colors'
import { formatChartNumber } from '../format'
import type { RangePreset } from '../range'
import type { EnergyRow } from '../transforms/buckets'
import type { EnergySummary as Summary } from '../transforms/summary'
import { EnergySummary } from './EnergySummary'
import { EnergyTooltip } from './EnergyTooltip'

type Props = {
  metrics: DashboardMetric[]
  series: EnergyRow[]
  preset: RangePreset
  summary: Summary
  loading: boolean
}

export function EnergyChart({ metrics, series, preset, summary, loading }: Props) {
  const tooltipContent = useCallback(
    (props: Omit<React.ComponentProps<typeof EnergyTooltip>, 'preset'>) => (
      <EnergyTooltip {...props} preset={preset} />
    ),
    [preset],
  )

  return (
    <div className="chart-card">
      <h2>Energy Trend</h2>
      <EnergySummary summary={summary} />
      <div className="chart-wrap">
        {loading ? (
          <p className="chart-placeholder">Loading...</p>
        ) : series.length === 0 ? (
          <p className="chart-placeholder">No data available for selected range.</p>
        ) : preset === 'day' ? (
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={series}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="time" />
              <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
              <Tooltip formatter={(v) => formatChartNumber(Number(v))} />
              <Legend />
              <ReferenceLine y={0} stroke="#64748b" />
              {metrics.map((m) => (
                <Line
                  key={m.key}
                  type="monotone"
                  dataKey={m.key}
                  name={m.label}
                  dot={false}
                  stroke={energyColor(m.key, preset)}
                />
              ))}
            </LineChart>
          </ResponsiveContainer>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={series} stackOffset="sign">
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="time" />
              <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
              <Tooltip content={tooltipContent} />
              <Legend />
              <ReferenceLine y={0} stroke="#64748b" />
              {metrics.map((m) => (
                <Bar
                  key={m.key}
                  dataKey={m.key}
                  name={m.label}
                  stackId="energy"
                  fill={energyColor(m.key, preset)}
                />
              ))}
            </BarChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  )
}
