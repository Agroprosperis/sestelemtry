import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EMPTY_FLOWS, type EnergyFlows } from '../transforms/flows'
import { DailySummaryNarrative } from './DailySummaryNarrative'

function makeFlows(): EnergyFlows {
  return {
    ...EMPTY_FLOWS,
    pvProducedKwh: 555,
    pvToLoadKwh: 185,
    pvToGridKwh: 317,
    pvToEssKwh: 53.9,
    gridToLoadKwh: 91,
    gridToEssKwh: 3.47,
    essToLoadKwh: 2.06,
  }
}

const anchor = new Date('2026-06-01T00:00:00+03:00')

describe('DailySummaryNarrative', () => {
  it('shows placeholders and the spinner before /energy-summary has returned', () => {
    render(
      <DailySummaryNarrative
        flows={EMPTY_FLOWS}
        preset="day"
        anchor={anchor}
        debug={false}
        registers={null}
        pvForecastTotal={null}
        loading
        flowsLoaded={false}
      />,
    )
    expect(screen.getByText(/прогноз —/)).toBeInTheDocument()
    expect(screen.getAllByText('—').length).toBeGreaterThan(2)
    expect(
      screen.getByLabelText('Завантаження підсумку'),
    ).toBeInTheDocument()
  })

  it('keeps the period numbers visible during a background refresh and suppresses the spinner', () => {
    // Stale-while-revalidate: the on-the-fly allocator can take
    // 5–15 s on a busy day. Once we have one good response we hold
    // the numbers on screen and refresh silently — the spinner
    // would otherwise animate on every periodic re-fetch and feel
    // like a permanent loading state.
    render(
      <DailySummaryNarrative
        flows={makeFlows()}
        preset="day"
        anchor={anchor}
        debug={false}
        registers={null}
        pvForecastTotal={600}
        loading
        flowsLoaded
      />,
    )
    expect(screen.getAllByText(/кВт·год/).length).toBeGreaterThanOrEqual(6)
    expect(screen.queryAllByText('—').length).toBe(0)
    expect(screen.queryByLabelText('Завантаження підсумку')).toBeNull()
  })
})
