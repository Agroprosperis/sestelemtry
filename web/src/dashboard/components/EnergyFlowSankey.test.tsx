import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { EnergyFlowSankey } from './EnergyFlowSankey'
import { EMPTY_FLOWS, flowsFromTotals } from '../transforms/flows'

afterEach(cleanup)

describe('EnergyFlowSankey', () => {
  it('renders all four nodes and seven legend rows for a populated period', () => {
    const flows = flowsFromTotals({
      accumulated_pv_energy_yield_kwh: 100,
      accumulated_electricity_purchased_kwh: 30,
      accumulated_electricity_sold_kwh: 20,
      total_energy_charged_kwh: 25,
      total_energy_discharged_kwh: 50,
      pv_to_ess_kwh: 20,
      grid_to_ess_kwh: 5,
      ess_to_load_kwh: 40,
      ess_to_grid_kwh: 10,
    })
    render(<EnergyFlowSankey flows={flows} />)

    // Four nodes — labels appear once each in the SVG.
    expect(screen.getAllByText('СЕС').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('УЗЕ').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Мережа').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Споживання').length).toBeGreaterThanOrEqual(1)

    // Each of the seven flows shows up in the legend with its label.
    expect(screen.getByText('СЕС → Споживання')).toBeInTheDocument()
    expect(screen.getByText('СЕС → УЗЕ')).toBeInTheDocument()
    expect(screen.getByText('СЕС → Мережа')).toBeInTheDocument()
    expect(screen.getByText('Мережа → Споживання')).toBeInTheDocument()
    expect(screen.getByText('Мережа → УЗЕ')).toBeInTheDocument()
    expect(screen.getByText('УЗЕ → Споживання')).toBeInTheDocument()
    expect(screen.getByText('УЗЕ → Мережа')).toBeInTheDocument()
  })

  it('renders without crashing when all flows are zero', () => {
    const { container } = render(<EnergyFlowSankey flows={EMPTY_FLOWS} />)
    // The SVG is always rendered (degraded but valid layout) and no
    // `<path>` carries a non-zero stroke-width.
    const paths = container.querySelectorAll('path')
    paths.forEach((p) => {
      expect(p.getAttribute('stroke-width')).toBe(null)
    })
  })

  it('shows the missing-aggregator hint when only the SmartLogger accumulators are present', () => {
    const flows = flowsFromTotals({
      accumulated_pv_energy_yield_kwh: 100,
      accumulated_electricity_purchased_kwh: 50,
    })
    render(<EnergyFlowSankey flows={flows} />)
    expect(
      screen.getByText(/Дані з лічильників УЗЕ ще не зібрані/),
    ).toBeInTheDocument()
  })
})
