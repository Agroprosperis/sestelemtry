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
import { toChartRows } from './chart'

type RangePreset = 'day' | 'week' | 'month'
const knownOrganizations = ['demo-org', 'pe']
const periodEnergyMetricKeys = new Set(['total_energy_charged_kwh', 'total_energy_discharged_kwh'])
const dayEnergyMetricKeys = new Set([
  'accumulated_pv_energy_yield_kwh',
  'accumulated_power_consumption_kwh',
  'accumulated_electricity_purchased_kwh',
])
const dashboardRefreshMs = 1000

const fallbackConfig: DashboardConfig = {
  cards: [
    { key: 'load_power_kw', label: 'Load Power', unit: 'kW' },
    { key: 'active_pv_power_kw', label: 'Active PV Power', unit: 'kW' },
    { key: 'active_ess_power_kw', label: 'Active ESS Power', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Grid Active Power', unit: 'kW' },
    { key: 'soc_percent', label: 'SOC', unit: '%' },
    { key: 'accumulated_pv_energy_yield_kwh', label: 'Inverter energy yield of current day', unit: 'kWh' },
    { key: 'accumulated_electricity_purchased_kwh', label: 'Current Day Supply From Grid', unit: 'kWh' },
    { key: 'accumulated_electricity_sold_kwh', label: 'Accumulated Grid Export', unit: 'kWh' },
    { key: 'accumulated_power_consumption_kwh', label: 'Current Day Consumption', unit: 'kWh' },
    { key: 'total_power_supply_from_grid_kwh', label: 'Total Supply From Grid', unit: 'kWh' },
  ],
  power_chart: [
    { key: 'active_pv_power_kw', label: 'PV Power', unit: 'kW' },
    { key: 'load_power_kw', label: 'Load Power', unit: 'kW' },
    { key: 'grid_connected_active_power_kw', label: 'Grid Active Power', unit: 'kW' },
  ],
  energy_chart: [
    { key: 'pv_energy_yield_day_kwh', label: 'PV Daily Yield', unit: 'kWh' },
    { key: 'total_energy_charged_kwh', label: 'Energy Charged', unit: 'kWh' },
    { key: 'total_energy_discharged_kwh', label: 'Energy Discharged', unit: 'kWh' },
  ],
}

function toISO(date: Date) {
  return date.toISOString()
}

function rangeParams(preset: RangePreset) {
  const to = new Date()
  const from = new Date(to)
  let bucket: string
  if (preset === 'week') {
    from.setDate(to.getDate() - 7)
    bucket = '1 hour'
  } else if (preset === 'month') {
    from.setDate(1)
    from.setHours(0, 0, 0, 0)
    bucket = '1 day'
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

function normalizePeriodEnergyPoints(points: TimeseriesPoint[]): TimeseriesPoint[] {
  const baselines = new Map<string, { time: number; value: number }>()
  for (const p of points) {
    if (!periodEnergyMetricKeys.has(p.metric_key)) continue
    const t = new Date(p.time).getTime()
    if (!Number.isFinite(t) || !Number.isFinite(p.value)) continue
    const baseline = baselines.get(p.metric_key)
    if (!baseline || t < baseline.time) {
      baselines.set(p.metric_key, { time: t, value: p.value })
    }
  }

  return points.map((p) => {
    if (!periodEnergyMetricKeys.has(p.metric_key)) {
      return p
    }
    const baseline = baselines.get(p.metric_key)
    if (!baseline) {
      return p
    }
    return { ...p, value: p.value - baseline.value }
  })
}

function energyBucketDeltaRows(points: TimeseriesPoint[], preset: RangePreset) {
  const metricKeys = ['pv_energy_yield_day_kwh', 'total_energy_charged_kwh', 'total_energy_discharged_kwh']
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
    const timeLabel =
      preset === 'month'
        ? dt.toLocaleDateString(undefined, { day: '2-digit' })
        : dt.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
    const out: Record<string, string | number> = { time: timeLabel }
    for (const key of metricKeys) {
      const current = row.values[key]
      if (!Number.isFinite(current)) {
        out[key] = 0
        continue
      }
      const previous = prev.get(key)
      let delta = 0
      if (Number.isFinite(previous)) {
        delta = current - (previous as number)
      }
      if (delta < 0) delta = 0
      prev.set(key, current)
      out[key] = key === 'total_energy_discharged_kwh' ? -delta : delta
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

function App() {
  const [preset, setPreset] = useState<RangePreset>('day')
  const [config, setConfig] = useState<DashboardConfig>(fallbackConfig)
  const [current, setCurrent] = useState<CurrentResponse | null>(null)
  const [powerSeries, setPowerSeries] = useState<Record<string, string | number>[]>([])
  const [energySeries, setEnergySeries] = useState<Record<string, string | number>[]>([])
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

        const [cur, power, energy, dayEnergy] = await Promise.all([
          fetchCurrent(organizationID),
          fetchTimeseries({
            organizationID,
            metricKeys: cfg.power_chart.map((m) => m.key),
            ...rangeParams(preset),
          }),
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
        setPowerSeries(toChartRows(power.points, cfg.power_chart.map((m) => m.key)))
        setEnergySeries(toChartRows(normalizePeriodEnergyPoints(energy.points), cfg.energy_chart.map((m) => m.key)))
        setEnergyBarSeries(energyBucketDeltaRows(energy.points, preset))
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
            <button type="button" onClick={() => setPreset('week')} className={preset === 'week' ? 'active' : ''}>
              Week
            </button>
            <button type="button" onClick={() => setPreset('month')} className={preset === 'month' ? 'active' : ''}>
              Month
            </button>
          </div>
        </div>
      </header>

      {error && <section className="error-banner">Failed to load data: {error}</section>}

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

      <section className="chart-grid">
        <div className="chart-card">
          <h2>Power Trend</h2>
          <div className="chart-wrap">
            {loading ? (
              <p className="chart-placeholder">Loading...</p>
            ) : powerSeries.length === 0 ? (
              <p className="chart-placeholder">No data available for selected range.</p>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={powerSeries}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
                  <Tooltip formatter={(v) => formatChartNumber(Number(v))} />
                  <Legend />
                  {config.power_chart.map((m, idx) => (
                    <Line
                      key={m.key}
                      type="monotone"
                      dataKey={m.key}
                      name={m.label}
                      dot={false}
                      stroke={['#16a34a', '#2563eb', '#f97316', '#8b5cf6'][idx % 4]}
                    />
                  ))}
                </LineChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>

        <div className="chart-card">
          <h2>Energy Trend</h2>
          <div className="chart-wrap">
            {loading ? (
              <p className="chart-placeholder">Loading...</p>
            ) : (preset === 'day' || preset === 'month') && energyBarSeries.length === 0 ? (
              <p className="chart-placeholder">No data available for selected range.</p>
            ) : energySeries.length === 0 ? (
              <p className="chart-placeholder">No data available for selected range.</p>
            ) : preset === 'day' || preset === 'month' ? (
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={energyBarSeries}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
                  <Tooltip formatter={(v) => formatChartNumber(Number(v))} />
                  <Legend />
                  <ReferenceLine y={0} stroke="#64748b" />
                  <Bar dataKey="pv_energy_yield_day_kwh" name="PV Daily Yield" stackId="energy" fill="#16a34a" />
                  <Bar dataKey="total_energy_charged_kwh" name="Energy Charged" stackId="energy" fill="#0ea5e9" />
                  <Bar dataKey="total_energy_discharged_kwh" name="Energy Discharged" stackId="energy" fill="#f97316" />
                </BarChart>
              </ResponsiveContainer>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={energySeries}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
                  <Tooltip formatter={(v) => formatChartNumber(Number(v))} />
                  <Legend />
                  {config.energy_chart.map((m, idx) => (
                    <Line
                      key={m.key}
                      type="monotone"
                      dataKey={m.key}
                      name={m.label}
                      dot={false}
                      stroke={['#8b5cf6', '#0ea5e9', '#e11d48', '#16a34a'][idx % 4]}
                    />
                  ))}
                </LineChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>
      </section>
    </main>
  )
}

export default App
