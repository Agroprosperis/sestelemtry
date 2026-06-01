import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { CurrentResponse } from '../../types'
import { AccumulatedSnapshotNarrative } from './AccumulatedSnapshotNarrative'

// Snapshot of /api/v1/current that covers the seven metrics the
// card reads. Values are picked so each formats predictably in the
// MВт·год / кВт·год branches of formatEnergyCompactKWhUk.
function makeCurrent(): CurrentResponse {
  const t = '2026-06-01T07:00:00Z'
  const m = (key: string, value: number) => ({
    metric_key: key,
    value,
    time: t,
  })
  return {
    organization_id: 'ze',
    metrics: {
      accumulated_pv_energy_yield_kwh: m('accumulated_pv_energy_yield_kwh', 1_186_560),
      accumulated_power_consumption_kwh: m(
        'accumulated_power_consumption_kwh',
        1_823_730,
      ),
      accumulated_electricity_purchased_kwh: m(
        'accumulated_electricity_purchased_kwh',
        795_190,
      ),
      accumulated_electricity_sold_kwh: m(
        'accumulated_electricity_sold_kwh',
        158_010,
      ),
      total_energy_charged_kwh: m('total_energy_charged_kwh', 142_350),
      total_energy_discharged_kwh: m('total_energy_discharged_kwh', 127_540),
      total_power_supply_from_grid_kwh: m(
        'total_power_supply_from_grid_kwh',
        142_340,
      ),
    },
  }
}

describe('AccumulatedSnapshotNarrative', () => {
  it('shows placeholder values and the spinner when no /current has loaded yet', () => {
    render(
      <AccumulatedSnapshotNarrative
        current={null}
        loading
        debug={false}
        registers={null}
      />,
    )
    // Every metric row falls back to the em-dash placeholder when
    // the underlying value is missing.
    const dashes = screen.getAllByText('—')
    expect(dashes.length).toBeGreaterThanOrEqual(7)
    // The header LoadingSpinner is visible for first-load only —
    // its aria-label is the unique handle.
    expect(
      screen.getByLabelText('Завантаження лічильників'),
    ).toBeInTheDocument()
  })

  it('keeps the previous values on screen during a background refresh and hides the spinner', () => {
    // Stale-while-revalidate is the whole point of the new behavior.
    // The /current poll runs every 1s; showing a spinner each tick
    // would feel like the card is constantly broken, so once data
    // exists we silently refresh and leave the numbers in place.
    render(
      <AccumulatedSnapshotNarrative
        current={makeCurrent()}
        loading
        debug={false}
        registers={null}
      />,
    )
    expect(screen.getAllByText(/МВт·год/).length).toBeGreaterThanOrEqual(7)
    expect(screen.queryAllByText('—').length).toBe(0)
    expect(
      screen.queryByLabelText('Завантаження лічильників'),
    ).toBeNull()
  })

  it('renders values normally when not loading', () => {
    render(
      <AccumulatedSnapshotNarrative
        current={makeCurrent()}
        loading={false}
        debug={false}
        registers={null}
      />,
    )
    expect(screen.getAllByText(/МВт·год/).length).toBeGreaterThanOrEqual(7)
    expect(screen.queryAllByText('—').length).toBe(0)
    expect(
      screen.queryByLabelText('Завантаження лічильників'),
    ).toBeNull()
  })
})
