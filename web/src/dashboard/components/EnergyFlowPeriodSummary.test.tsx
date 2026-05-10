import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { EnergyFlowPeriodSummary } from './EnergyFlowPeriodSummary'
import { EMPTY_FLOWS, flowsFromTotals } from '../transforms/flows'

afterEach(cleanup)

function renderCard(
  override: Partial<React.ComponentProps<typeof EnergyFlowPeriodSummary>> = {},
) {
  const flows =
    override.flows ??
    flowsFromTotals({
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
  const props = {
    flows,
    preset: 'day' as const,
    refreshing: false,
    onRefresh: vi.fn(),
    ...override,
  }
  return { ...render(<EnergyFlowPeriodSummary {...props} />), props }
}

describe('EnergyFlowPeriodSummary', () => {
  it('renders the four narrative rows with kWh values from flowsFromTotals', () => {
    renderCard()

    expect(screen.getByText(/УЗЕ зарядилось від сонця/)).toBeInTheDocument()
    expect(screen.getByText(/УЗЕ зарядилось від мережі/)).toBeInTheDocument()
    expect(screen.getByText(/УЗЕ віддало на споживання/)).toBeInTheDocument()
    expect(screen.getByText(/УЗЕ віддало в мережу/)).toBeInTheDocument()

    // Compact Ukrainian formatter: 20 кВт·год.
    expect(screen.getByText(/20\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/5\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/40\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/10\s*кВт·год/)).toBeInTheDocument()
  })

  it('renders inside the metrics-group container so it stacks with other left-panel cards', () => {
    const { container } = renderCard({
      flows: flowsFromTotals({
        total_energy_charged_kwh: 5,
        total_energy_discharged_kwh: 5,
        pv_to_ess_kwh: 5,
        ess_to_load_kwh: 5,
      }),
    })
    expect(container.querySelector('section.metrics-group')).not.toBeNull()
    expect(container.querySelector('ul.daily-narrative-list')).not.toBeNull()
  })

  it('shows the missing-aggregator hint when no synthetic samples are present', () => {
    renderCard({ flows: EMPTY_FLOWS })
    expect(
      screen.getByText(/Дані з лічильників УЗЕ ще не зібрані/),
    ).toBeInTheDocument()
  })

  it('does not show the hint when the synthetic counters are present', () => {
    renderCard({
      flows: flowsFromTotals({
        total_energy_charged_kwh: 5,
        total_energy_discharged_kwh: 5,
        pv_to_ess_kwh: 5,
        ess_to_load_kwh: 5,
      }),
    })
    expect(
      screen.queryByText(/Дані з лічильників УЗЕ ще не зібрані/),
    ).not.toBeInTheDocument()
  })

  it('renders a period-aware title from the preset', () => {
    renderCard({ preset: 'month' })
    expect(screen.getByText('Перетік за місяць')).toBeInTheDocument()
  })

  it('calls onRefresh when the refresh button is clicked', () => {
    const onRefresh = vi.fn()
    renderCard({ onRefresh })
    fireEvent.click(screen.getByRole('button', { name: /Оновити перетік/i }))
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  it('disables the refresh button and marks it spinning while refreshing', () => {
    const onRefresh = vi.fn()
    renderCard({ refreshing: true, onRefresh })
    const button = screen.getByRole('button', { name: /Оновити перетік/i })
    expect(button).toBeDisabled()
    expect(button.className).toMatch(/is-spinning/)
    fireEvent.click(button)
    expect(onRefresh).not.toHaveBeenCalled()
  })
})
