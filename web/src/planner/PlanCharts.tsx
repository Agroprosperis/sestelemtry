import type { ReactNode } from 'react'
import {
  Bar,
  ComposedChart,
  Line,
  ReferenceArea,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import type { PlanPreview, PlanPreviewHour } from './plannerClient'

const COLORS = {
  rdn: '#3b82f6',
  pv: '#f59e0b',
  load: '#ea580c',
  tomorrow: '#8b5cf6',
  todayZone: '#f59e0b',
}

function hourLabel(ts: string, timezone: string): string {
  return new Intl.DateTimeFormat('uk-UA', {
    timeZone: timezone,
    hour: '2-digit',
    hour12: false,
  }).format(new Date(ts))
}

function firstTomorrowTs(hours: PlanPreviewHour[]): string | undefined {
  return hours.find((h) => h.tomorrow)?.ts
}

function lastTodayTs(hours: PlanPreviewHour[]): string | undefined {
  const today = hours.filter((h) => !h.tomorrow)
  return today.length > 0 ? today[today.length - 1].ts : undefined
}

const fmt1 = (v: number) => (Math.round(v * 10) / 10).toLocaleString('uk-UA')

function weatherIcon(h: PlanPreviewHour | undefined): string {
  const w = h?.weather
  if (!w) return ''
  if (!w.is_day) return '🌙'
  const cloud = w.cloud_pct ?? 0
  if (cloud < 25) return '☀️'
  if (cloud < 60) return '🌤️'
  return '☁️'
}

// weatherTick renders the context chart's X ticks as a three-line
// column — hour, weather icon, temperature — so the hourly weather
// strip is part of the chart itself and stays pixel-aligned with the
// bars (mockup: погодна смуга всередині forecast-графіка).
function makeWeatherTick(preview: PlanPreview) {
  const byTs = new Map(preview.hours.map((h) => [h.ts, h]))
  return function WeatherTick(props: unknown) {
    const { x, y, payload } = props as { x?: number; y?: number; payload?: { value?: string } }
    const ts = payload?.value ?? ''
    const h = byTs.get(ts)
    const temp = h?.weather?.temp_c != null ? `${Math.round(h.weather.temp_c)}°` : ''
    return (
      <g transform={`translate(${x ?? 0},${y ?? 0})`}>
        <text y={10} textAnchor="middle" fontSize={10} fill="#64748b">
          {hourLabel(ts, preview.timezone)}
        </text>
        <text y={26} textAnchor="middle" fontSize={11}>
          {weatherIcon(h)}
        </text>
        <text y={40} textAnchor="middle" fontSize={9} fill="#475569">
          {temp}
        </text>
      </g>
    )
  }
}

// ContextChart — «РДН + прогноз СЕС + load» (mockup plan-forecast-card):
// price columns on the right axis, PV forecast and planned load lines
// on the kW axis, the amber «решта сьогодні» zone, the violet «завтра»
// divider and the hourly weather baked into the X axis. Children render
// inside the card (legend/stats line).
export function ContextChart({ preview, children }: { preview: PlanPreview; children?: ReactNode }) {
  const data = preview.hours.map((h) => ({
    ts: h.ts,
    rdn: h.rdn_uah_per_kwh ?? null,
    pv: h.pv_kw,
    load: h.load_kw,
  }))
  const divider = firstTomorrowTs(preview.hours)
  const todayEnd = lastTodayTs(preview.hours)
  const hasWeather = preview.hours.some((h) => h.weather)
  return (
    <div className="planner-card">
      <h2>РДН + прогноз СЕС + load · від зараз до кінця завтра</h2>
      <ResponsiveContainer width="100%" height={hasWeather ? 262 : 230}>
        <ComposedChart data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <XAxis
            dataKey="ts"
            interval={1}
            height={hasWeather ? 48 : 30}
            tick={hasWeather ? makeWeatherTick(preview) : { fontSize: 10 }}
            tickFormatter={hasWeather ? undefined : (ts) => hourLabel(String(ts), preview.timezone)}
            tickLine={false}
          />
          <YAxis
            yAxisId="kw"
            tick={{ fontSize: 10 }}
            width={44}
            label={{ value: 'кВт', angle: -90, position: 'insideLeft', fontSize: 11 }}
          />
          <YAxis
            yAxisId="uah"
            orientation="right"
            tick={{ fontSize: 10 }}
            width={40}
            label={{ value: 'грн/кВт·год', angle: 90, position: 'insideRight', fontSize: 11 }}
          />
          <Tooltip
            formatter={(value, name) => {
              const v = typeof value === 'number' ? value : Number(value)
              if (name === 'РДН') return [`${fmt1(v)} грн/кВт·год`, name]
              return [`${fmt1(v)} кВт`, name]
            }}
            labelFormatter={(ts) => `${hourLabel(String(ts), preview.timezone)}:00`}
          />
          {todayEnd && (
            <ReferenceArea
              yAxisId="kw"
              x1={data[0].ts}
              x2={todayEnd}
              fill={COLORS.todayZone}
              fillOpacity={0.06}
            />
          )}
          <Bar yAxisId="uah" dataKey="rdn" name="РДН" fill={COLORS.rdn} opacity={0.3} />
          <Line yAxisId="kw" dataKey="pv" name="СЕС (прогноз)" stroke={COLORS.pv} strokeWidth={2} dot={false} />
          <Line yAxisId="kw" dataKey="load" name="Load (план)" stroke={COLORS.load} strokeWidth={2} dot={false} />
          {divider && (
            <ReferenceLine
              yAxisId="kw"
              x={divider}
              stroke={COLORS.tomorrow}
              strokeDasharray="5 4"
              label={{ value: 'завтра', fontSize: 11, fill: COLORS.tomorrow, position: 'top' }}
            />
          )}
        </ComposedChart>
      </ResponsiveContainer>
      {children}
    </div>
  )
}
