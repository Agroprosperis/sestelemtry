import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { formatChartNumber } from '../format'
import type { RangePreset } from '../range'
import { averagePrice, type DAMChartRow } from '../transforms/dam'

type Props = {
  series: DAMChartRow[]
  preset: RangePreset
}

const PRICE_LINE_COLOR = '#0ea5e9'
const PRICE_FILL_COLOR = '#bae6fd'

const PRESET_LABEL: Record<RangePreset, string> = {
  day: 'погодинно',
  month: 'середньодобово',
  year: 'середньомісячно',
}

function xAxisInterval(preset: RangePreset): number {
  if (preset === 'day') return 1
  if (preset === 'month') return 2
  return 0
}

export function DAMPriceChart({ series, preset }: Props) {
  const avg = averagePrice(series)
  const tickInterval = xAxisInterval(preset)
  return (
    <div className="chart-card">
      <div className="dam-chart-head">
        <h2>Ціни РДН</h2>
        <span className="dam-chart-meta">
          {PRESET_LABEL[preset]}
          {avg !== null && (
            <>
              {' · '}
              <strong>середня {formatChartNumber(avg)} грн/МВт·год</strong>
            </>
          )}
        </span>
      </div>
      <div className="chart-wrap">
        {series.length === 0 ? (
          <p className="chart-placeholder">No price data available for selected range.</p>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={series}>
              <defs>
                <linearGradient id="dam-price-fill" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="0%" stopColor={PRICE_FILL_COLOR} stopOpacity={0.7} />
                  <stop offset="100%" stopColor={PRICE_FILL_COLOR} stopOpacity={0.05} />
                </linearGradient>
              </defs>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="time" interval={tickInterval} />
              <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
              <Tooltip
                formatter={(v) => [`${formatChartNumber(Number(v))} грн/МВт·год`, 'Ціна РДН']}
                labelFormatter={(label) => String(label)}
              />
              <Area
                type="monotone"
                dataKey="price"
                name="Ціна РДН"
                stroke={PRICE_LINE_COLOR}
                strokeWidth={2}
                fill="url(#dam-price-fill)"
                dot={false}
                connectNulls
              />
            </AreaChart>
          </ResponsiveContainer>
        )}
      </div>
    </div>
  )
}
