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
    expect(screen.getAllByText(/^експорт$/i).length).toBeGreaterThan(0)
  })

  it('prints the grid voltage on the grid node when the site reports one', () => {
    const allocation = buildAllocation({ grid_connected_active_power_kw: -45.88, load_power_kw: 45.88 })
    const { container, rerender } = render(<EnergyFlowLive allocation={allocation} />)
    expect(container.querySelector('.energy-flow-live-node-volt')).toBeNull()

    rerender(
      <EnergyFlowLive
        allocation={allocation}
        gridVoltage={{ kind: 'line', volts: [390.6, 394, 394.5], at: new Date('2026-05-10T12:00:00Z') }}
      />,
    )
    const volt = screen.getByText('Uл 391/394/395 В')
    expect(volt.closest('.energy-flow-live-node--grid')).not.toBeNull()
    expect(volt.getAttribute('title')).toContain('Лінійна')
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
    expect(screen.getByText(/без даних/i)).toBeInTheDocument()
  })

  it('shows importing balance when grid pulls energy', () => {
    const allocation = buildAllocation({
      active_pv_power_kw: 0,
      load_power_kw: 8,
      grid_connected_active_power_kw: 8,
      active_ess_power_kw: 0,
    })
    render(<EnergyFlowLive allocation={allocation} />)
    expect(screen.getAllByText(/імпорт/i).length).toBeGreaterThan(0)
  })

  it('draws the generator as a fifth node and says the grid is gone', () => {
    const allocation = buildAllocation({
      active_pv_power_kw: 20,
      active_ess_power_kw: 0,
      grid_breakers_on_bits: 0,
      gen_breakers_on_bits: 1,
      active_gen_power_kw: 80,
      gen_loading_pct: 40,
    })
    const { container } = render(<EnergyFlowLive allocation={allocation} />)
    const genNode = container.querySelector('.energy-flow-live-node--gen')
    expect(genNode).not.toBeNull()
    expect(genNode?.getAttribute('data-state')).toBe('running')
    expect(screen.getByText('Генератор')).toBeInTheDocument()
    expect(screen.getByText('Працює · 40%')).toBeInTheDocument()
    expect(screen.getByText('Мережі немає')).toBeInTheDocument()
    expect(screen.getByText('Від генератора')).toBeInTheDocument()
    const edges = Array.from(container.querySelectorAll('.energy-flow-live-path')).map((p) => p.getAttribute('data-edge'))
    expect(edges).toContain('gen')
  })

  it('captions the hub by the supply mode', () => {
    const island = buildAllocation({
      active_pv_power_kw: 0,
      active_ess_power_kw: 40,
      grid_breakers_on_bits: 0,
      gen_breakers_on_bits: 0,
      gen_bus_voltage_a_v: 230,
    })
    const { rerender } = render(<EnergyFlowLive allocation={island} />)
    expect(screen.getByText('Острів: живлення від УЗЕ')).toBeInTheDocument()

    const blackout = buildAllocation({
      active_pv_power_kw: 0,
      active_ess_power_kw: 0,
      grid_breakers_on_bits: 0,
      gen_breakers_on_bits: 0,
      gen_bus_voltage_a_v: 0,
    })
    rerender(<EnergyFlowLive allocation={blackout} />)
    expect(screen.getAllByText('Без живлення').length).toBeGreaterThan(0)
  })

  it('keeps four nodes for a site without a generator', () => {
    const allocation = buildAllocation({ active_pv_power_kw: 5, active_ess_power_kw: 0, grid_connected_active_power_kw: 0 })
    const { container } = render(<EnergyFlowLive allocation={allocation} />)
    expect(container.querySelector('.energy-flow-live-node--gen')).toBeNull()
    expect(container.querySelector('.energy-flow-live-stage.has-gen')).toBeNull()
  })
})
