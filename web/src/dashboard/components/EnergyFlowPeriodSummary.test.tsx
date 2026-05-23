import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { EnergyFlowPeriodSummary } from './EnergyFlowPeriodSummary'
import { flowsFromTotals } from '../transforms/flows'

afterEach(cleanup)

function renderCard(
  override: Partial<React.ComponentProps<typeof EnergyFlowPeriodSummary>> = {},
) {
  const flows =
    override.flows ??
    flowsFromTotals(
      {
        accumulated_pv_energy_yield_kwh: 100,
        accumulated_electricity_purchased_kwh: 30,
        accumulated_electricity_sold_kwh: 20,
        total_energy_charged_kwh: 25,
        total_energy_discharged_kwh: 50,
      },
      {
        pv_to_ess_kwh: 20,
        grid_to_ess_kwh: 5,
        ess_to_load_kwh: 40,
        ess_to_grid_kwh: 10,
      },
    )
  const props = {
    flows,
    preset: 'day' as const,
    anchor: new Date(Date.UTC(2026, 4, 10)),
    refreshing: false,
    onRefresh: vi.fn(),
    ...override,
  }
  return { ...render(<EnergyFlowPeriodSummary {...props} />), props }
}

describe('EnergyFlowPeriodSummary', () => {
  it('renders the four flow rows with kWh values from flowsFromTotals', () => {
    renderCard()

    // The card now uses the icon · label · value · bar · % layout
    // (mirrors the overview's BatteryFlowsCard) — labels read as
    // arrows instead of full sentences.
    expect(screen.getByText(/Від сонця → УЗЕ/)).toBeInTheDocument()
    expect(screen.getByText(/З мережі → УЗЕ/)).toBeInTheDocument()
    expect(screen.getByText(/УЗЕ → споживання/)).toBeInTheDocument()
    expect(screen.getByText(/УЗЕ → мережа/)).toBeInTheDocument()

    // Compact Ukrainian formatter: 20 кВт·год. Anchor the regex
    // with a non-digit boundary so "5" doesn't also match the
    // "-25 кВт·год" balance footer.
    expect(screen.getByText(/(^|[^\d])20\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/(^|[^\d])5\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/(^|[^\d])40\s*кВт·год/)).toBeInTheDocument()
    expect(screen.getByText(/(^|[^\d])10\s*кВт·год/)).toBeInTheDocument()
  })

  it('renders inside the metrics-group container so it stacks with other left-panel cards', () => {
    const { container } = renderCard({
      flows: flowsFromTotals(
        {
          total_energy_charged_kwh: 5,
          total_energy_discharged_kwh: 5,
        },
        { pv_to_ess_kwh: 5, grid_to_ess_kwh: 0, ess_to_load_kwh: 5, ess_to_grid_kwh: 0 },
      ),
    })
    expect(container.querySelector('section.metrics-group')).not.toBeNull()
    expect(container.querySelector('ul.accum-narrative-list')).not.toBeNull()
  })

  it('renders a battery balance footer summing inflow vs outflow', () => {
    renderCard()
    // totalIn=20+5=25, totalOut=40+10=50, balance=-25 → "-25 кВт·год".
    expect(screen.getByText(/Баланс батареї/)).toBeInTheDocument()
    expect(screen.getByText(/-25\s*кВт·год/)).toBeInTheDocument()
  })

  it('renders a period-aware title from the preset', () => {
    renderCard({ preset: 'month' })
    expect(screen.getByText(/Перетік за місяць/)).toBeInTheDocument()
  })

  it('decodes the concrete anchor period in the header', () => {
    renderCard({ preset: 'day', anchor: new Date(Date.UTC(2026, 4, 10)) })
    // Ukrainian locale renders "10 травня 2026 р." — match the day
    // and month name regardless of the trailing punctuation/format.
    expect(screen.getByText(/10 травня 2026/)).toBeInTheDocument()
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
