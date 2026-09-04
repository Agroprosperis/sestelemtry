import { useMemo, type ReactNode } from 'react'
import type { PlanPreview, PlanPreviewHour } from './plannerClient'

// ContextChart — «РДН + прогноз СЕС + load»: custom SVG ported 1:1
// from the mockup's buildPlanForecastChartSvg (cloud_console.html):
// the in-SVG weather strip (flat icons + °C, not emoji), RDN columns
// with tiered price labels, dashed green PV area, orange load line,
// the amber «решта сьогодні» zone and the violet «завтра» divider.

const PC = { W: 920, ML: 36, MR: 36, WEATHER: 36, MT: 12, MB: 26, PLOT_H: 198 }

// Flat SVG icon paths (16×16), copied from the mockup verbatim.
const WEATHER_ICON_PATHS: Record<string, string> = {
  night:
    '<path d="M11.2 2.2a4.2 4.2 0 1 0 5.4 5.4A3.2 3.2 0 1 1 11.2 2.2z" stroke="#eab308" stroke-width="1.3" fill="#fef9c3"/>',
  cloudy:
    '<path d="M4.2 11.2h7.6a2.8 2.8 0 0 0 .4-5.6 3.2 3.2 0 0 0-6.1-1 2.3 2.3 0 0 0-1.9 6.6z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.2"/>',
  partly:
    '<circle cx="11.5" cy="4.8" r="2" fill="#fde68a" stroke="#eab308" stroke-width="1"/><path d="M4.2 11.2h7.6a2.8 2.8 0 0 0 .4-5.6 3.2 3.2 0 0 0-6.1-1 2.3 2.3 0 0 0-1.9 6.6z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.2"/>',
  rain:
    '<path d="M4.2 8.8h7.6a2.8 2.8 0 0 0 .4-5.6 3.2 3.2 0 0 0-6.1-1 2.3 2.3 0 0 0-1.9 6.6z" fill="#f8fafc" stroke="#94a3b8" stroke-width="1.2"/><path d="M5.5 11.2v2M8 10.5v2.2M10.5 11.2v2" stroke="#38bdf8" stroke-width="1.3" stroke-linecap="round"/>',
  clear:
    '<circle cx="8" cy="8" r="3" fill="#fde68a" stroke="#eab308" stroke-width="1.2"/><path d="M8 2v1.6M8 12.4V14M2 8h1.6M12.4 8H14M4.1 4.1l1.1 1.1M10.8 10.8l1.1 1.1M4.1 11.9l1.1-1.1M10.8 5.2l1.1-1.1" stroke="#eab308" stroke-width="1.1" stroke-linecap="round"/>',
}

// Real forecast data instead of the mockup's demo heuristic: night from
// is_day, the rest from cloud cover.
function weatherKind(h: PlanPreviewHour): string {
  const w = h.weather
  if (!w) return 'cloudy'
  if (!w.is_day) return 'night'
  const cloud = w.cloud_pct ?? 50
  if (cloud < 25) return 'clear'
  if (cloud < 60) return 'partly'
  return 'cloudy'
}

function rdnTierText(p: number): string {
  if (p >= 8) return '#991b1b'
  if (p >= 4) return '#854d0e'
  if (p >= 1.5) return '#1e40af'
  return '#1d4ed8'
}

const fmtRdnLabel = (p: number) => (p >= 10 ? p.toFixed(0) : p.toFixed(1))

function cloudDescription(hours: PlanPreviewHour[]): string {
  const day = hours.filter((h) => h.weather?.is_day && h.weather.cloud_pct != null)
  if (day.length === 0) return ''
  const mean = day.reduce((s, h) => s + (h.weather?.cloud_pct ?? 0), 0) / day.length
  if (mean < 25) return 'Ясно'
  if (mean < 60) return 'Малохмарно'
  return 'Хмарно'
}

export function ContextChart({ preview, children }: { preview: PlanPreview; children?: ReactNode }) {
  const hours = preview.hours
  const n = hours.length

  const timeFmt = useMemo(
    () =>
      new Intl.DateTimeFormat('uk-UA', {
        timeZone: preview.timezone,
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }),
    [preview.timezone],
  )
  const dateFmt = useMemo(
    () =>
      new Intl.DateTimeFormat('uk-UA', {
        timeZone: preview.timezone,
        day: '2-digit',
        month: '2-digit',
        year: 'numeric',
      }),
    [preview.timezone],
  )

  if (n === 0) return null

  const { W, ML, MR, WEATHER, MT, MB, PLOT_H } = PC
  const H = WEATHER + MT + PLOT_H + MB
  const ix0 = ML
  const ix1 = W - MR
  const iy0 = WEATHER + MT
  const iy1 = iy0 + PLOT_H
  const iw = ix1 - ix0
  const ih = iy1 - iy0

  const rdn = hours.map((h) => h.rdn_uah_per_kwh ?? 0)
  const maxKw = Math.max(...hours.map((h) => Math.max(h.pv_kw, h.load_kw)), 1)
  const maxRdn = Math.max(...rdn, 0.1)
  const boundary = hours.findIndex((h) => h.tomorrow)
  const todayRest = boundary < 0 ? 0 : boundary
  const dense = n > 26
  const cx = (i: number) => ix0 + ((i + 0.5) / n) * iw
  const xAt = (i: number) => ix0 + (i / n) * iw
  const bw = Math.max(4, (iw / n) * (dense ? 0.72 : 0.52))

  const parts: ReactNode[] = []

  // Weather strip: light band, one icon + °C per hour on the bar grid.
  const hasWeather = hours.some((h) => h.weather)
  parts.push(
    <g key="wband">
      <rect x={ML} y={0} width={ix1 - ML} height={WEATHER} fill="#f0f9ff" />
      <line x1={ML} y1={WEATHER} x2={ix1} y2={WEATHER} stroke="#e2e8f0" />
    </g>,
  )
  if (hasWeather) {
    const step = n > 40 ? 2 : 1
    const iconScale = n > 20 ? 0.72 : 1
    const tempFs = n > 20 ? 8 : 10
    const io = 8 * iconScale
    hours.forEach((h, i) => {
      if (i % step !== 0) return
      const w = h.weather
      if (!w) return
      const kind = weatherKind(h)
      const night = !w.is_day
      parts.push(
        <g key={'wx' + i} transform={`translate(${cx(i).toFixed(1)}, 4)`} opacity={night ? 0.88 : 1}>
          <g
            transform={`translate(${(-io).toFixed(1)}, 0) scale(${iconScale})`}
            dangerouslySetInnerHTML={{ __html: WEATHER_ICON_PATHS[kind] ?? WEATHER_ICON_PATHS.cloudy }}
          />
          {w.temp_c != null && (
            <text x={0} y={26} textAnchor="middle" fontSize={tempFs} fontWeight={600} fill={night ? '#64748b' : '#475569'}>
              {Math.round(w.temp_c)}°
            </text>
          )}
        </g>,
      )
    })
  }

  // Axis captions inside the strip's bottom edge.
  parts.push(
    <g key="captions">
      <text x={ix0} y={WEATHER + 10} fontSize={9} fill="#16a34a" fontWeight={600}>
        кВт
      </text>
      <text x={ix1} y={WEATHER + 10} textAnchor="end" fontSize={9} fill="#1d4ed8" fontWeight={600}>
        ₴/кВт·год
      </text>
    </g>,
  )

  // Grid.
  for (let t = 0; t <= 4; t++) {
    const y = iy0 + (ih * t) / 4
    parts.push(<line key={'g' + t} x1={ix0} y1={y} x2={ix1} y2={y} stroke="#e2e8f0" strokeDasharray="3 4" />)
  }

  // «решта сьогодні» + «зараз» + «завтра».
  if (todayRest > 0) {
    parts.push(
      <rect
        key="rest"
        x={ix0}
        y={iy0}
        width={xAt(todayRest) - ix0}
        height={ih}
        fill="#f59e0b"
        opacity={0.06}
      />,
    )
  }
  parts.push(
    <text key="now" x={ix0 + 3} y={iy0 + 8} fontSize={9} fontWeight={700} fill="#16a34a">
      зараз
    </text>,
  )
  if (boundary > 0) {
    const xDay = xAt(boundary)
    parts.push(
      <g key="tomorrow">
        <line x1={xDay} y1={iy0 - 4} x2={xDay} y2={iy1} stroke="#7c3aed" strokeWidth={1.4} strokeDasharray="4 3" />
        <text x={xDay + 3} y={iy0 + 8} fontSize={9} fontWeight={700} fill="#7c3aed">
          завтра
        </text>
      </g>,
    )
  }

  // RDN columns + tiered price labels.
  hours.forEach((h, i) => {
    if (h.rdn_uah_per_kwh == null) return
    const p = rdn[i]
    const bh = Math.max(3, (p / maxRdn) * ih * 0.9)
    const y = iy1 - bh
    parts.push(
      <rect
        key={'b' + i}
        x={cx(i) - bw / 2}
        y={y}
        width={bw}
        height={bh}
        fill="#3b82f6"
        opacity={0.42}
        rx={1.5}
      />,
    )
    if (!dense || i % 3 === 0 || p >= 8) {
      parts.push(
        <text
          key={'bl' + i}
          x={cx(i)}
          y={Math.max(iy0 + 10, y - 5)}
          textAnchor="middle"
          fontSize={8.5}
          fontWeight={p >= 8 ? 700 : 600}
          fill={rdnTierText(p)}
        >
          {fmtRdnLabel(p)}
        </text>,
      )
    }
  })

  // Load: orange line + light area + «зараз» dot.
  const loadPts = hours.map((h, i) => ({ x: cx(i), y: iy1 - (h.load_kw / maxKw) * ih * 0.88 }))
  const loadLineD = loadPts.map((p, i) => `${i ? 'L' : 'M'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ')
  const loadAreaD = `${loadLineD} L${loadPts[n - 1].x.toFixed(1)},${iy1} L${loadPts[0].x.toFixed(1)},${iy1} Z`
  parts.push(<path key="la" d={loadAreaD} fill="#ea580c" opacity={0.1} />)
  parts.push(<path key="ll" d={loadLineD} fill="none" stroke="#ea580c" strokeWidth={2} />)
  parts.push(
    <circle key="ld" cx={loadPts[0].x} cy={loadPts[0].y} r={4} fill="#ea580c" stroke="#fff" strokeWidth={1.5} />,
  )

  // PV forecast: dashed green line + area + triangle markers.
  const pvPts = hours.map((h, i) => ({ x: cx(i), y: iy1 - (h.pv_kw / maxKw) * ih * 0.88 }))
  const pvLineD = pvPts.map((p, i) => `${i ? 'L' : 'M'}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ')
  const pvAreaD = `${pvLineD} L${pvPts[n - 1].x.toFixed(1)},${iy1} L${pvPts[0].x.toFixed(1)},${iy1} Z`
  parts.push(<path key="pa" d={pvAreaD} fill="#16a34a" opacity={0.12} />)
  parts.push(<path key="pl" d={pvLineD} fill="none" stroke="#16a34a" strokeWidth={1.5} strokeDasharray="4 3" />)
  if (!dense) {
    pvPts.forEach((p, i) =>
      parts.push(
        <path key={'pm' + i} d={`M${p.x.toFixed(1)},${(p.y - 3).toFixed(1)} l2.5,3 l-5,0 z`} fill="#16a34a" />,
      ),
    )
  }

  // X hour ticks.
  hours.forEach((h, i) => {
    const major = i % 3 === 0 || i === 0 || i === boundary
    parts.push(
      <text
        key={'x' + i}
        x={cx(i)}
        y={iy1 + 15}
        textAnchor="middle"
        fontSize={major ? 9 : 8}
        fontWeight={major ? 600 : 400}
        fill={major ? '#64748b' : '#94a3b8'}
      >
        {String(h.local_hour).padStart(2, '0')}
      </text>,
    )
  })

  // Y ticks: kW left (green), ₴ right (blue).
  for (let t = 0; t <= 4; t++) {
    const y = iy1 - (ih * t) / 4
    parts.push(
      <text key={'yl' + t} x={ix0 - 5} y={y + 3} textAnchor="end" fontSize={9} fill="#16a34a">
        {Math.round((maxKw * t) / 4)}
      </text>,
      <text key={'yr' + t} x={ix1 + 5} y={y + 3} textAnchor="start" fontSize={9} fill="#1d4ed8">
        {((maxRdn * t) / 4).toFixed(1)}
      </text>,
    )
  }
  parts.push(<line key="base" x1={ix0} y1={iy1} x2={ix1} y2={iy1} stroke="#cbd5e1" />)

  // Header meta: «зараз HH:MM → кінець DD.MM.YYYY · РДН … · Погода …».
  const nowLabel = timeFmt.format(new Date(preview.now))
  const endLabel = dateFmt.format(new Date(preview.tomorrow_start))
  const tomorrowPriced = hours.some((h) => h.tomorrow && h.rdn_uah_per_kwh != null)
  const temps = hours
    .map((h) => h.weather?.temp_c)
    .filter((v): v is number => v != null)
  const weatherMeta =
    temps.length > 0
      ? ` · Погода ${preview.site_id}: ${cloudDescription(hours)} ${Math.round(Math.min(...temps))}°…${Math.round(Math.max(...temps))}°C`
      : ''

  return (
    <div className="planner-card plan-forecast-card">
      <div className="dam-chart-head">
        <h2>РДН + прогноз СЕС + load · від зараз до кінця завтра</h2>
        <div className="dam-chart-meta">
          зараз {nowLabel} → кінець {endLabel} ·{' '}
          <strong>{tomorrowPriced ? 'РДН опубліковано' : 'РДН на завтра ще немає'}</strong>
          {weatherMeta}
        </div>
      </div>
      <div className="plan-forecast-chart-wrap">
        <svg
          viewBox={`0 0 ${W} ${H}`}
          className="plan-forecast-svg"
          preserveAspectRatio="xMidYMid meet"
          role="img"
          aria-label="РДН, прогноз СЕС і план load — від зараз до кінця завтра"
        >
          {parts}
        </svg>
      </div>
      {children}
    </div>
  )
}
