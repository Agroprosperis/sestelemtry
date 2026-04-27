import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import App from './App'

vi.mock('./api', () => ({
  fetchDashboardConfig: vi.fn(async () => ({
    cards: [{ key: 'soc_percent', label: 'SOC', unit: '%' }],
    power_chart: [{ key: 'active_pv_power_kw', label: 'PV Power', unit: 'kW' }],
    energy_chart: [{ key: 'pv_energy_yield_day_kwh', label: 'PV Daily Yield', unit: 'kWh' }],
  })),
  fetchCurrent: vi.fn(async () => ({
    organization_id: 'demo-org',
    metrics: {
      soc_percent: {
        metric_key: 'soc_percent',
        value: 88.5,
        time: '2026-04-26T10:00:00Z',
      },
    },
  })),
  fetchTimeseries: vi.fn(async () => ({
    organization_id: 'demo-org',
    metric_keys: ['x'],
    bucket: '15 minutes',
    from: '2026-04-26T09:00:00Z',
    to: '2026-04-26T10:00:00Z',
    points: [{ time: '2026-04-26T10:00:00Z', metric_key: 'x', value: 1 }],
  })),
}))

describe('App', () => {
  it('renders loaded KPI card values', async () => {
    render(<App />)
    await waitFor(() => {
      expect(screen.getByText('SOC')).toBeInTheDocument()
      expect(screen.getByText(/88.5/)).toBeInTheDocument()
    })
  })
})
