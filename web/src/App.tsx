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
const energyTrendMetricKeys = new Set(Object.keys(energyTrendMetricDirections))
const applianceConsumptionMetricKey = 'accumulated_power_consumption_kwh'
const dayEnergyMetricKeys = new Set([
  'accumulated_pv_energy_yield_kwh',
  'accumulated_power_consumption_kwh',
  'accumulated_electricity_purchased_kwh',
])
const dashboardRefreshMs = 1000

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

function normalizePeriodEnergyPoints(points: TimeseriesPoint[]): TimeseriesPoint[] {
  const baselines = new Map<string, { time: number; value: number }>()
  for (const p of points) {
    if (!energyTrendMetricKeys.has(p.metric_key)) continue
    const t = new Date(p.time).getTime()
    if (!Number.isFinite(t) || !Number.isFinite(p.value)) continue
    const baseline = baselines.get(p.metric_key)
    if (!baseline || t < baseline.time) {
      baselines.set(p.metric_key, { time: t, value: p.value })
    }
  }

  const rawByTime = new Map<string, Record<string, number>>()
  for (const p of points) {
    if (!energyTrendMetricKeys.has(p.metric_key)) continue
    const baseline = baselines.get(p.metric_key)
    if (!baseline) continue
    const delta = p.value - baseline.value
    const safeDelta = delta > 0 ? delta : 0
    const iso = new Date(p.time).toISOString()
    const row = rawByTime.get(iso) || {}
    row[p.metric_key] = safeDelta
    rawByTime.set(iso, row)
  }

  return points.map((p) => {
    if (!energyTrendMetricKeys.has(p.metric_key)) {
      return p
    }
    const iso = new Date(p.time).toISOString()
    const row = rawByTime.get(iso) || {}
    let safeDelta = row[p.metric_key] ?? 0
    if (p.metric_key === applianceConsumptionMetricKey) {
      // Appliance consumption = from grid + PV production + ESS discharge - ESS charge.
      safeDelta =
        (row.accumulated_electricity_purchased_kwh ?? 0) +
        (row.pv_energy_yield_day_kwh ?? 0) +
        (row.total_energy_discharged_kwh ?? 0) -
        (row.total_energy_charged_kwh ?? 0)
      if (safeDelta < 0) safeDelta = 0
    }
    const direction = energyTrendMetricDirections[p.metric_key] ?? 1
    return {
      ...p,
      value: safeDelta * direction,
    }
  })
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

function buildDayFlowSeries(rows: Record<string, string | number>[]) {
  return rows.map((row) => {
    const grid = Number(row.grid_connected_active_power_kw ?? 0)
    const ess = Number(row.active_ess_power_kw ?? 0)
    const pv = Number(row.active_pv_power_kw ?? 0)
    const load = Number(row.load_power_kw ?? 0)
    return {
      time: row.time,
      grid_import_kw: Math.max(grid, 0),
      ess_discharging_kw: Math.max(-ess, 0),
      pv_output_kw: Math.max(pv, 0),
      grid_export_kw: -Math.max(-grid, 0),
      ess_charging_kw: -Math.max(ess, 0),
      appliance_consumption_kw: -Math.max(load, 0),
    }
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

function energyColor(metricKey: string): string {
  if (metricKey === 'grid_import_kw' || metricKey === 'grid_export_kw') return '#9ca3af'
  if (metricKey === 'ess_discharging_kw' || metricKey === 'ess_charging_kw') return '#2563eb'
  if (metricKey === 'pv_output_kw') return '#22c55e'
  if (metricKey === 'appliance_consumption_kw') return '#f59e0b'
  if (metricKey === 'accumulated_electricity_purchased_kwh') return '#16a34a'
  if (metricKey === 'pv_energy_yield_day_kwh') return '#22c55e'
  if (metricKey === 'total_energy_discharged_kwh') return '#6b7280'
  if (metricKey === 'accumulated_electricity_sold_kwh') return '#f59e0b'
  if (metricKey === 'total_energy_charged_kwh') return '#0ea5e9'
  if (metricKey === 'accumulated_power_consumption_kwh') return '#fb923c'
  return '#8b5cf6'
}

function App() {
  const [preset, setPreset] = useState<RangePreset>('day')
  const [config, setConfig] = useState<DashboardConfig>(fallbackConfig)
  const [current, setCurrent] = useState<CurrentResponse | null>(null)
  const [powerSeries, setPowerSeries] = useState<Record<string, string | number>[]>([])
  const [dayFlowSeries, setDayFlowSeries] = useState<Record<string, string | number>[]>([])
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
        const powerRows = toChartRows(power.points, cfg.power_chart.map((m) => m.key), (d) => formatTimeLabel(d, preset))
        setPowerSeries(powerRows)
        setDayFlowSeries(buildDayFlowSeries(powerRows))
        setEnergySeries(
          toChartRows(normalizePeriodEnergyPoints(energy.points), cfg.energy_chart.map((m) => m.key), (d) =>
            formatTimeLabel(d, preset),
          ),
        )
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
          <h2>Energy Trend</h2>
          <div className="chart-wrap">
            {loading ? (
              <p className="chart-placeholder">Loading...</p>
            ) : preset === 'day' && dayFlowSeries.length === 0 ? (
              <p className="chart-placeholder">No data available for selected range.</p>
            ) : preset !== 'day' && energyBarSeries.length === 0 ? (
              <p className="chart-placeholder">No data available for selected range.</p>
            ) : preset !== 'day' && energySeries.length === 0 ? (
              <p className="chart-placeholder">No data available for selected range.</p>
            ) : preset === 'day' ? (
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={dayFlowSeries}>
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
                  <Tooltip formatter={(v) => formatChartNumber(Number(v))} />
                  <Legend />
                  <ReferenceLine y={0} stroke="#64748b" />
                  <Line
                    type="monotone"
                    dataKey="grid_import_kw"
                    name="Енергія від електромережі"
                    dot={false}
                    stroke={energyColor('grid_import_kw')}
                  />
                  <Line
                    type="monotone"
                    dataKey="ess_discharging_kw"
                    name="Потужність розряджання ESS"
                    dot={false}
                    stroke={energyColor('ess_discharging_kw')}
                  />
                  <Line
                    type="monotone"
                    dataKey="pv_output_kw"
                    name="Вихід фотоелектричного обладнання"
                    dot={false}
                    stroke={energyColor('pv_output_kw')}
                  />
                  <Line
                    type="monotone"
                    dataKey="grid_export_kw"
                    name="Постачання енергії"
                    dot={false}
                    stroke={energyColor('grid_export_kw')}
                  />
                  <Line
                    type="monotone"
                    dataKey="ess_charging_kw"
                    name="Потужність заряджання ESS"
                    dot={false}
                    stroke={energyColor('ess_charging_kw')}
                  />
                  <Line
                    type="monotone"
                    dataKey="appliance_consumption_kw"
                    name="Споживання приладами"
                    dot={false}
                    stroke={energyColor('appliance_consumption_kw')}
                  />
                </LineChart>
              </ResponsiveContainer>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={energyBarSeries} stackOffset="sign">
                  <CartesianGrid strokeDasharray="3 3" />
                  <XAxis dataKey="time" />
                  <YAxis tickFormatter={(v) => formatChartNumber(Number(v))} />
                  <Tooltip formatter={(v) => formatChartNumber(Number(v))} />
                  <Legend />
                  <ReferenceLine y={0} stroke="#64748b" />
                  {config.energy_chart.map((m) => (
                    <Bar key={m.key} dataKey={m.key} name={m.label} stackId="energy" fill={energyColor(m.key)} />
                  ))}
                </BarChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>
      </section>
    </main>
  )
}

export default App
