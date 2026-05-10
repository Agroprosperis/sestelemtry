import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { EnergyFlowPeriodSummary } from './EnergyFlowPeriodSummary'
import { EMPTY_FLOWS, flowsFromTotals } from '../transforms/flows'

afterEach(cleanup)

describe('EnergyFlowPeriodSummary', () => {
  it('renders the four narrative rows with kWh values from flowsFromTotals', () => {
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
    render(<EnergyFlowPeriodSummary flows={flows} />)

    expect(screen.getByText('УЗЕ зарядилось від сонця')).toBeInTheDocument()
    expect(screen.getByText('УЗЕ зарядилось від мережі')).toBeInTheDocument()
    expect(screen.getByText('УЗЕ віддало на споживання')).toBeInTheDocument()
    expect(screen.getByText('УЗЕ віддало в мережу')).toBeInTheDocument()

    // Compact Ukrainian formatter: 20 кВт·год.
    expect(screen.getByText(/20\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/5\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/40\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/10\s*кВт·год/)).toBeInTheDocument()
  })

  it('shows the missing-aggregator hint when no synthetic samples are present', () => {
    render(<EnergyFlowPeriodSummary flows={EMPTY_FLOWS} />)
    expect(
      screen.getByText(/Дані з лічильників УЗЕ ще не зібрані/),
    ).toBeInTheDocument()
  })

  it('does not show the hint when the synthetic counters are present', () => {
    const flows = flowsFromTotals({
      total_energy_charged_kwh: 5,
      total_energy_discharged_kwh: 5,
      pv_to_ess_kwh: 5,
      ess_to_load_kwh: 5,
    })
    render(<EnergyFlowPeriodSummary flows={flows} />)
    expect(
      screen.queryByText(/Дані з лічильників УЗЕ ще не зібрані/),
    ).not.toBeInTheDocument()
  })
})
