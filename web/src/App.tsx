import { useEffect, useMemo, useState } from 'react'
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
import { fetchCurrent, fetchDashboardConfig, fetchTimeseries } from './api'
import type { CurrentResponse, DashboardConfig, DashboardMetric, TimeseriesPoint } from './types'

type RangePreset = 'day' | 'month' | 'year'
const knownOrganizations = ['demo-org', 'pe']
const periodEnergyMetricKeys = new Set(['total_energy_charged_kwh', 'total_energy_discharged_kwh'])
const energyTrendMetricDirections: Record<string, 1 | -1> = {
  accumulated_electricity_purchased_kwh: 1,
  total_energy_discharged_kwh: 1,
  pv_energy_yield_day_kwh: 1,
  accumulated_electricity_sold_kwh: -1,
  total_energy_charged_kwh: -1,
  accumulated_power_consumption_kwh: -1,
}
const applianceConsumptionMetricKey = 'accumulated_power_consumption_kwh'
const dayEnergyMetricKeys = new Set([
  'accumulated_pv_energy_yield_kwh',
  'accumulated_power_consumption_kwh',
  'accumulated_electricity_purchased_kwh',
])
const dashboardRefreshMs = 1000
const sourceEnergyMetricKeys = [
  'accumulated_electricity_purchased_kwh',
  'total_energy_discharged_kwh',
  'pv_energy_yield_day_kwh',
]
const sinkEnergyMetricKeys = [
  'accumulated_electricity_sold_kwh',
  'total_energy_charged_kwh',
  'accumulated_power_consumption_kwh',
]

const fallbackConfig: DashboardConfig = {
  cards: [
    { key: 'pv_energy_yield_day_kwh', label: 'Виробіток за сьогодні', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Енергія, отримана ESS сьогодні', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Енергія, витрачена ESS сьогодні', unit: 'kWh' },
    { key: 'load_power_kw', label: 'Потужність навантаження', unit: 'kW' },
    { key: 'active_pv_power_kw', label: 'Активна потужність PV', unit: 'kW' },
    { key: 'active_ess_power_kw', label: 'Активна потужність ESS', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Активна потужність мережі', unit: 'kW' },
    { key: 'soc_percent', label: 'SOC', unit: '%' },
    { key: 'accumulated_pv_energy_yield_kwh', label: 'Виробіток інвертора за сьогодні', unit: 'kWh' },
    { key: 'accumulated_electricity_purchased_kwh', label: 'Отримано з мережі за сьогодні', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Подано в мережу (накопичувально)', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Споживання за сьогодні', unit: 'kWh' },
    { key: 'total_power_supply_from_grid_kwh', label: 'Загальне постачання з мережі', unit: 'kWh' },
  ],
  power_chart: [
    { key: 'active_pv_power_kw', label: 'Потужність PV', unit: 'kW' },
    { key: 'load_power_kw', label: 'Потужність навантаження', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Активна потужність мережі', unit: 'kW' },
  ],
  energy_chart: [
    { key: 'accumulated_electricity_purchased_kwh', label: 'З електромережі', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Розряджання системи накопичення енергії', unit: 'kWh' },
    { key: 'pv_energy_yield_day_kwh', label: 'Вироблено фотоелектричною установкою', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Подано в електромережу', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Заряджання системи накопичення енергії', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Споживання приладами', unit: 'kWh' },
  ],
}

function toISO(date: Date) {
  return date.toISOString()
}

function rangeParams(preset: RangePreset) {
  const to = new Date()
  const from = new Date(to)
  let bucket: string
  if (preset === 'month') {
    from.setDate(1)
    from.setHours(0, 0, 0, 0)
    bucket = '1 day'
  } else if (preset === 'year') {
    from.setMonth(0, 1)
    from.setHours(0, 0, 0, 0)
    bucket = '1 month'
  } else {
    from.setHours(0, 0, 0, 0)
    bucket = '1 hour'
  }
  return {
    from: toISO(from),
    to: toISO(to),
    bucket,
  }
}

function dayRangeParams() {
  const to = new Date()
  const from = new Date(to)
  from.setHours(0, 0, 0, 0)
  return {
    from: toISO(from),
    to: toISO(to),
    bucket: '15 minutes',
  }
}

function formatValue(metric: DashboardMetric, current: CurrentResponse | null) {
  const m = current?.metrics?.[metric.key]
  if (!m) {
    return '--'
  }
  return formatNumber(m.value, metric.unit)
}

function metricDeltas(points: TimeseriesPoint[], keys: Set<string>): Record<string, number> {
  const span = new Map<string, { firstTime: number; firstValue: number; lastTime: number; lastValue: number }>()
  for (const p of points) {
    if (!keys.has(p.metric_key)) continue
    const t = new Date(p.time).getTime()
    if (!Number.isFinite(t) || !Number.isFinite(p.value)) continue
    const current = span.get(p.metric_key)
    if (!current) {
      span.set(p.metric_key, { firstTime: t, firstValue: p.value, lastTime: t, lastValue: p.value })
      continue
    }
    if (t < current.firstTime) {
      current.firstTime = t
      current.firstValue = p.value
    }
    if (t > current.lastTime) {
      current.lastTime = t
      current.lastValue = p.value
    }
  }
  const out: Record<string, number> = {}
  for (const [metricKey, v] of span.entries()) {
    out[metricKey] = v.lastValue - v.firstValue
  }
  return out
}

function periodEnergyDeltas(points: TimeseriesPoint[]): Record<string, number> {
  return metricDeltas(points, periodEnergyMetricKeys)
}

function dayEnergyDeltas(points: TimeseriesPoint[]): Record<string, number> {
  return metricDeltas(points, dayEnergyMetricKeys)
}

function formatTimeLabel(date: Date, preset: RangePreset): string {
  if (preset === 'year') {
    return date.toLocaleDateString(undefined, { month: 'short' })
  }
  if (preset === 'month') {
    return date.toLocaleDateString(undefined, { day: '2-digit' })
  }
  return date.toLocaleTimeString(undefined, { hour: '2-digit' })
}

function energyBucketDeltaRows(points: TimeseriesPoint[], metricKeys: string[], preset: RangePreset) {
  const keyed = new Map<string, { t: number; values: Record<string, number> }>()
  for (const p of points) {
    if (!metricKeys.includes(p.metric_key)) continue
    const t = new Date(p.time).getTime()
    if (!Number.isFinite(t) || !Number.isFinite(p.value)) continue
    const k = new Date(p.time).toISOString()
    const row = keyed.get(k) || { t, values: {} }
    row.values[p.metric_key] = p.value
    keyed.set(k, row)
  }

  const sorted = Array.from(keyed.values()).sort((a, b) => a.t - b.t)
  const prev = new Map<string, number>()

  return sorted.map((row) => {
    const dt = new Date(row.t)
    const timeLabel = formatTimeLabel(dt, preset)
    const out: Record<string, string | number> = { time: timeLabel }
    const rawDeltas: Record<string, number> = {}
    for (const key of metricKeys) {
      const current = row.values[key]
      if (!Number.isFinite(current)) {
        rawDeltas[key] = 0
        continue
      }
      const previous = prev.get(key)
      let delta = 0
      if (Number.isFinite(previous)) {
        delta = current - (previous as number)
      }
      if (delta < 0) delta = 0
      prev.set(key, current)
      rawDeltas[key] = delta
    }

    if (applianceConsumptionMetricKey in rawDeltas) {
      rawDeltas[applianceConsumptionMetricKey] =
        (rawDeltas.accumulated_electricity_purchased_kwh ?? 0) +
        (rawDeltas.pv_energy_yield_day_kwh ?? 0) +
        (rawDeltas.total_energy_discharged_kwh ?? 0) -
        (rawDeltas.total_energy_charged_kwh ?? 0)
      if (rawDeltas[applianceConsumptionMetricKey] < 0) {
        rawDeltas[applianceConsumptionMetricKey] = 0
      }
    }

    for (const key of metricKeys) {
      const direction = energyTrendMetricDirections[key] ?? 1
      out[key] = (rawDeltas[key] ?? 0) * direction
    }
    return out
  })
}

function formatNumber(value: number, unit: string) {
  const decimals = unit === '%' ? 1 : 2
  const factor = 10 ** decimals
  const rounded = Math.round((value + Number.EPSILON) * factor) / factor
  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits: decimals,
    maximumFractionDigits: decimals,
  }).format(rounded)
}

function formatChartNumber(value: number) {
  const rounded = Math.round((value + Number.EPSILON) * 100) / 100
  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  }).format(rounded)
}

type EnergyTooltipEntry = {
  dataKey?: string | number | ((obj: unknown) => unknown)
  name?: string | number
  value?: unknown
  color?: string
}

function energyTooltipContent(props: {
  active?: boolean
  label?: string | number
  payload?: readonly EnergyTooltipEntry[]
  preset: RangePreset
}) {
  const { active, label, payload, preset } = props
  if (!active || !payload || payload.length === 0 || preset === 'day') {
    return null
  }

  const byKey = new Map<string, EnergyTooltipEntry>()
  for (const entry of payload) {
    if (typeof entry.dataKey === 'string') byKey.set(entry.dataKey, entry)
  }

  const sourceTotal = sourceEnergyMetricKeys.reduce((sum, key) => {
    const v = Number(byKey.get(key)?.value)
    if (!Number.isFinite(v)) return sum
    return sum + Math.max(v, 0)
  }, 0)

  const sinkTotal = sinkEnergyMetricKeys.reduce((sum, key) => {
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
        <span className="energy-tooltip-value">{value === null ? '--' : `${formatChartNumber(value)} kWh`}</span>
      </div>
    )
  }

  return (
    <div className="energy-tooltip">
      <div className="energy-tooltip-label">{label}</div>
      <div className="energy-tooltip-grid">
        <div>
          <div className="energy-tooltip-head">
            <span>Джерела енергії</span>
            <span>{formatChartNumber(sourceTotal)} kWh</span>
          </div>
          {sourceEnergyMetricKeys.map((key) => row(key))}
        </div>
        <div>
          <div className="energy-tooltip-head">
            <span>Стоки енергії</span>
            <span>{formatChartNumber(sinkTotal)} kWh</span>
          </div>
          {sinkEnergyMetricKeys.map((key) => row(key, true))}
        </div>
      </div>
    </div>
  )
}

function energyColor(metricKey: string, preset: RangePreset): string {
  if (preset === 'day') {
    if (metricKey === 'accumulated_electricity_purchased_kwh') return '#9ca3af'
    if (metricKey === 'total_energy_discharged_kwh') return '#2563eb'
    if (metricKey === 'pv_energy_yield_day_kwh') return '#22c55e'
    if (metricKey === 'accumulated_electricity_sold_kwh') return '#f97316'
    if (metricKey === 'total_energy_charged_kwh') return '#2563eb'
    if (metricKey === 'accumulated_power_consumption_kwh') return '#f59e0b'
  }
  if (metricKey === 'accumulated_electricity_purchased_kwh') return '#16a34a'
  if (metricKey === 'total_energy_discharged_kwh') return '#4ade80'
  if (metricKey === 'pv_energy_yield_day_kwh') return '#86efac'
  if (metricKey === 'accumulated_electricity_sold_kwh') return '#f97316'
  if (metricKey === 'total_energy_charged_kwh') return '#fb923c'
  if (metricKey === 'accumulated_power_consumption_kwh') return '#fdba74'
  return '#8b5cf6'
}

function App() {
  const [preset, setPreset] = useState<RangePreset>('day')
  const [config, setConfig] = useState<DashboardConfig>(fallbackConfig)
  const [current, setCurrent] = useState<CurrentResponse | null>(null)
  const [energyBarSeries, setEnergyBarSeries] = useState<Record<string, string | number>[]>([])
  const [periodEnergyValues, setPeriodEnergyValues] = useState<Record<string, number>>({})
  const [dayEnergyValues, setDayEnergyValues] = useState<Record<string, number>>({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const initialOrganizationID = useMemo(() => {
    const search = new URLSearchParams(window.location.search)
    return search.get('organization_id') || 'demo-org'
  }, [])
  const [organizationID, setOrganizationID] = useState(initialOrganizationID)
  const organizationOptions = useMemo(() => {
    if (knownOrganizations.includes(organizationID)) {
      return knownOrganizations
    }
    return [organizationID, ...knownOrganizations]
  }, [organizationID])

  function onOrganizationChange(nextID: string) {
    setOrganizationID(nextID)
    const url = new URL(window.location.href)
    url.searchParams.set('organization_id', nextID)
    window.history.replaceState({}, '', url)
  }

  useEffect(() => {
    let active = true
    async function load(showLoading: boolean) {
      if (showLoading) {
        setLoading(true)
      }
      setError(null)
      try {
        const cfg = await fetchDashboardConfig()
        if (!active) return
        setConfig(cfg)

        const [cur, energy, dayEnergy] = await Promise.all([
          fetchCurrent(organizationID),
          fetchTimeseries({
            organizationID,
            metricKeys: cfg.energy_chart.map((m) => m.key),
            ...rangeParams(preset),
          }),
          fetchTimeseries({
            organizationID,
            metricKeys: Array.from(dayEnergyMetricKeys),
            ...dayRangeParams(),
          }),
        ])
        if (!active) return
        setCurrent(cur)
        setEnergyBarSeries(energyBucketDeltaRows(energy.points, cfg.energy_chart.map((m) => m.key), preset))
        setPeriodEnergyValues(periodEnergyDeltas(energy.points))
        setDayEnergyValues(dayEnergyDeltas(dayEnergy.points))
      } catch (e) {
        if (!active) return
        setError(e instanceof Error ? e.message : 'Failed to load dashboard data')
      } finally {
        if (active && showLoading) setLoading(false)
      }
    }
    void load(true)
    const timer = window.setInterval(() => {
      void load(false)
    }, dashboardRefreshMs)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [organizationID, preset])

  return (
    <main className="dashboard-page">
      <header className="dashboard-header">
        <div>
          <h1>Telemetry Dashboard</h1>
          <p>Organization: {organizationID}</p>
        </div>
        <div className="header-controls">
          <label className="org-select" htmlFor="org-select">
            <span>Organization</span>
            <select id="org-select" value={organizationID} onChange={(e) => onOrganizationChange(e.target.value)}>
              {organizationOptions.map((orgID) => (
                <option key={orgID} value={orgID}>
                  {orgID}
                </option>
              ))}
            </select>
          </label>
          <div className="range-switch">
            <button type="button" onClick={() => setPreset('day')} className={preset === 'day' ? 'active' : ''}>
              Day
            </button>
            <button type="button" onClick={() => setPreset('month')} className={preset === 'month' ? 'active' : ''}>
              Month
            </button>
            <button type="button" onClick={() => setPreset('year')} className={preset === 'year' ? 'active' : ''}>
              Year
            </button>
          </div>
        </div>
      </header>

      {error && <section className="error-banner">Failed to load data: {error}</section>}

      <section className="dashboard-main">
        <div className="chart-card">
          <h2>Energy Trend</h2>
          <div className="chart-wrap">
            {loading ? (
              <p className="chart-placeholder">Loading...</p>
            ) : energyBarSeries.length === 0 ? (
              <p className="chart-placeholder">No data available for selected range.</p>
            ) : preset === 'day' ? (
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={energyBarSeries}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
                  <Tooltip formatter={(v) => formatChartNumber(Number(v))} />
                  <Legend />
                  <ReferenceLine y={0} stroke="#64748b" />
                  {config.energy_chart.map((m) => (
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
                <BarChart data={energyBarSeries} stackOffset="sign">
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
                  <Tooltip content={(p) => energyTooltipContent({ ...p, preset })} />
                  <Legend />
                  <ReferenceLine y={0} stroke="#64748b" />
                  {config.energy_chart.map((m) => (
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

        <aside className="metrics-panel">
          <h2>Поточні показники</h2>
          <section className="cards-grid">
            {config.cards.map((card) => (
              <article key={card.key} className="card" aria-busy={loading}>
                <p className="card-label">{card.label}</p>
                <p className="card-value">
                  {loading
                    ? '...'
                    : dayEnergyMetricKeys.has(card.key)
                      ? formatNumber(dayEnergyValues[card.key] ?? 0, card.unit)
                      : periodEnergyMetricKeys.has(card.key)
                      ? formatNumber(periodEnergyValues[card.key] ?? 0, card.unit)
                      : formatValue(card, current)}{' '}
                  <span>{card.unit}</span>
                </p>
              </article>
            ))}
          </section>
        </aside>
      </section>
    </main>
  )
}

export default App
