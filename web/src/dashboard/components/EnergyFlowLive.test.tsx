import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { EnergyFlowLive } from './EnergyFlowLive'
import {
  liveAllocationFromCurrent,
  NO_DATA_ALLOCATION,
} from '../transforms/liveAllocation'
import type { CurrentResponse } from '../../types'

afterEach(cleanup)

function buildAllocation(metrics: Record<string, number>) {
  const current: CurrentResponse = {
    organization_id: 'demo',
    metrics: {},
  }
  for (const [k, v] of Object.entries(metrics)) {
    current.metrics[k] = {
      metric_key: k,
      value: v,
      time: '2026-05-10T12:00:00Z',
    }
  }
  return liveAllocationFromCurrent(current)
}

describe('EnergyFlowLive', () => {
  it('renders all four corner cards and the central hub', () => {
    const allocation = buildAllocation({
      active_pv_power_kw: 42.6,
      load_power_kw: 51.4,
      grid_connected_active_power_kw: -1.8,
      active_ess_power_kw: 8.6,
      soc_percent: 84,
    })
    render(<EnergyFlowLive allocation={allocation} />)

    expect(screen.getByText('СЕС')).toBeInTheDocument()
    expect(screen.getByText('Споживання')).toBeInTheDocument()
    expect(screen.getByText('УЗЕ')).toBeInTheDocument()
    expect(screen.getByText('Мережа')).toBeInTheDocument()
    expect(screen.getByText(/SOC 84%/)).toBeInTheDocument()
    expect(screen.getByText(/експорт у мережу/i)).toBeInTheDocument()
  })

  it('renders four animated edges', () => {
    const allocation = buildAllocation({
      active_pv_power_kw: 5,
      load_power_kw: 5,
      grid_connected_active_power_kw: 0,
      active_ess_power_kw: 0,
    })
    const { container } = render(<EnergyFlowLive allocation={allocation} />)
    const edges = container.querySelectorAll('.energy-flow-live-path')
    expect(edges.length).toBe(4)
    const dataIds = Array.from(edges).map((p) => p.getAttribute('data-edge'))
    expect(dataIds).toEqual(expect.arrayContaining(['pv', 'load', 'ess', 'grid']))
  })

  it('marks idle edges so the marching animation does not run', () => {
    const allocation = buildAllocation({
      active_pv_power_kw: 0,
      load_power_kw: 0,
      grid_connected_active_power_kw: 0,
      active_ess_power_kw: 0,
    })
    const { container } = render(<EnergyFlowLive allocation={allocation} />)
    const edges = container.querySelectorAll('.energy-flow-live-path.is-idle')
    expect(edges.length).toBe(4)
  })

  it('renders no_data state without crashing', () => {
    const { container } = render(<EnergyFlowLive allocation={NO_DATA_ALLOCATION} />)
    expect(container.querySelector('.energy-flow-live-status.is-stale')).toBeTruthy()
    expect(container.querySelector('.energy-flow-live-hub.is-stale')).toBeTruthy()
    expect(screen.getAllByText(/Немає даних/).length).toBeGreaterThan(0)
    expect(screen.getByText(/очікуємо опитування/i)).toBeInTheDocument()
  })

  it('shows importing balance when grid pulls energy', () => {
    const allocation = buildAllocation({
      active_pv_power_kw: 0,
      load_power_kw: 8,
      grid_connected_active_power_kw: 8,
      active_ess_power_kw: 0,
    })
    render(<EnergyFlowLive allocation={allocation} />)
    expect(screen.getByText(/імпорт з мережі/i)).toBeInTheDocument()
    expect(screen.getByText(/Імпорт/)).toBeInTheDocument()
  })
})
