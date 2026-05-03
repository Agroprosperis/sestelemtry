import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Dashboard } from './Dashboard'

vi.mock('./hooks/useDashboardData', () => ({
  useDashboardData: vi.fn(() => ({
    config: {
      cards: [{ key: 'soc_percent', label: 'SOC', unit: '%' }],
      power_chart: [{ key: 'active_pv_power_kw', label: 'PV Power', unit: 'kW' }],
      energy_chart: [{ key: 'accumulated_pv_energy_yield_kwh', label: 'PV Daily Yield', unit: 'kWh' }],
    },
    current: {
      organization_id: 'demo-org',
      metrics: {
        soc_percent: {
          metric_key: 'soc_percent',
          value: 88.5,
          time: '2026-04-26T10:00:00Z',
        },
      },
    },
    energySeries: [],
    damSeries: [],
    socSeries: [],
    powerSeries: [],
    energySummary: {
      pvProduced: 0,
      gridExport: 0,
      pvConsumed: 0,
      consumption: 0,
      fromGrid: 0,
      fromPV: 0,
      fromBattery: 0,
      batteryCharged: 0,
      batteryDischarged: 0,
      pvConsumedPct: 0,
      pvExportPct: 0,
      loadFromPVPct: 0,
      loadFromBatteryPct: 0,
      loadFromGridPct: 0,
      selfSufficiencyPct: 0,
    },
    loading: false,
    cardsLoading: false,
    error: null,
  })),
}))

describe('Dashboard', () => {
  it('renders KPI card values from the data hook', () => {
    render(<Dashboard />)
    expect(screen.getByText('SOC')).toBeInTheDocument()
    expect(screen.getByText(/88[.,]5/)).toBeInTheDocument()
  })
})
