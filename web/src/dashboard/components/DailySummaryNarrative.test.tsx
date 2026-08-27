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

  it('compares a month against its plan', () => {
    render(
      <DailySummaryNarrative
        flows={makeFlows()}
        preset="month"
        anchor={anchor}
        debug={false}
        registers={null}
        pvForecastTotal={1200}
        flowsLoaded
      />,
    )
    expect(screen.getByText(/прогноз 1,20 МВт·год/)).toBeInTheDocument()
  })

  // The plan for a month lands after the flows do. Reading "прогноз
  // недоступний" in between would be wrong, not just early.
  it('holds the placeholder while the period plan is still loading', () => {
    render(
      <DailySummaryNarrative
        flows={makeFlows()}
        preset="month"
        anchor={anchor}
        debug={false}
        registers={null}
        pvForecastTotal={null}
        pvForecastLoading
        flowsLoaded
      />,
    )
    expect(screen.getByText(/прогноз —/)).toBeInTheDocument()
    expect(screen.queryByText(/прогноз недоступний/)).toBeNull()
  })

  it('says so when the plan covers only part of the period', () => {
    render(
      <DailySummaryNarrative
        flows={makeFlows()}
        preset="month"
        anchor={anchor}
        debug={false}
        registers={null}
        pvForecastTotal={900}
        pvForecastCoverage={{ covered: 18, expected: 31 }}
        flowsLoaded
      />,
    )
    expect(screen.getByText(/Прогноз відомий за 18 з 31 днів/)).toBeInTheDocument()
  })
})
